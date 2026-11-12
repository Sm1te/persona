package persona.feature.settings

import android.content.Context
import android.content.res.Resources
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.runBlocking
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.Machine
import persona.core.contracts.api.*

/**
 * Editing a Persona-managed provider and its device scope (docs/integrations/launchers.md "Device scope and on-demand delivery",
 * "Provider model and gateway parameters"): the form opens prefilled with no secret in it, a PUT carries the base fields and only
 * what changed, the key / headers / environment are kept, replaced or cleared on purpose, and the scope's POST / PUT fields.
 */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
class ProviderEditStateTest {
    private val res: Resources get() = ApplicationProvider.getApplicationContext<Context>().resources

    private class Hub(private val scoped: Boolean = true) : ProviderSetup {
        override val launcherScope = flowOf(scoped)
        val added = mutableListOf<NewProvider>()
        val updates = mutableListOf<Pair<String, ProviderUpdate>>()
        var refuse: Exception? = null
        override suspend fun presets(): List<ProviderPreset> = emptyList()
        override suspend fun add(provider: NewProvider) { added += provider }
        override suspend fun update(name: String, changes: ProviderUpdate) { refuse?.let { throw it }; updates += name to changes }
    }

    private val machines = listOf(
        Machine("mac", "Work Mac", online = true),
        Machine("box", "Build box", online = true),
        Machine("pending", "New laptop", trust = "pending"),
        Machine("office", "Office PC", trust = "work"),
        // Another group's machine (a group suffix): not this hub's.
        Machine("far~g0123abcd", "Other hub's Mac"),
    )

    private val stored = HubLauncher(
        "jv_gw", source = "persona", baseUrl = "https://gateway.example.com", model = "gw-pro", smallModel = "gw-mini", args = listOf("--verbose"),
        hasKey = true, hasHeaders = true, hasEnv = true, auth = "bearer",
        models = listOf(PresetModel("gw-mini", contextWindow = 128000), PresetModel("gw-max", "GW Max", contextWindow = 1000000, efforts = listOf("high", "max"))),
        opusModel = "gw-max", subagentForce = true, effort = "high", noProxy = "gateway.example.com", timeoutMs = 600000,
        share = "selected", scopeMachines = listOf("mac"), delivery = "on_demand", ownService = "skip", keyMachines = listOf("mac"),
    )

    private fun editing(l: HubLauncher = stored, hub: Hub = Hub()) = NewProviderModel(hub).also { it.prepare(l, l.hasScope, machines) }

    @Test fun anExistingProviderOpensPrefilledWithoutItsSecrets() {
        val model = editing()
        val s = model.state.value
        val d = s.draft
        assertTrue(s.editing)
        assertEquals(WizardStep.Form, s.step)
        assertEquals("jv_gw", d.name)
        assertEquals("bearer", d.auth)
        assertEquals("--verbose", d.args)
        // The default model goes first when the list lacks it; the stored models keep their windows and levels.
        assertEquals(listOf("gw-pro", "gw-mini", "gw-max"), d.models.map { it.id })
        assertEquals(1000000, d.models.last().contextWindow)
        assertEquals("gw-max", d.opusModel)
        assertEquals("600000", d.timeoutMs)
        assertEquals("", d.compactWindow)
        // A bypass list of the gateway's host alone is No proxy.
        assertEquals(ProxyMode.Direct, d.proxyMode)
        assertEquals("", d.noProxy)
        // Never a secret: the key, headers and environment start empty.
        assertEquals("", d.apiKey)
        assertEquals("", d.headers)
        assertEquals("", d.env)
        assertEquals(ScopeForm(Share.Selected, machines = listOf("mac"), ownService = OwnService.Skip), s.scope)
        assertFalse("nothing changed yet", s.dirty)
        assertFalse(s.canSaveEdit)
    }

    @Test fun aPutCarriesTheBaseFieldsAndOnlyWhatChanged() = runBlocking {
        val hub = Hub()
        val model = editing(hub = hub)
        // Nothing changed: the base fields only, nothing that would overwrite a stored value.
        val same = model.state.value.update!!
        assertEquals(ProviderUpdate("jv_gw", "launcher", "messages", "https://gateway.example.com", "gw-pro", "gw-mini", "", "", "", "",
            listOf("--verbose"), auth = "bearer"), same)

        model.edit(model.state.value.draft.copy(sonnetModel = " gw-mid ", timeoutMs = "900000"))
        assertTrue(model.state.value.dirty && model.state.value.canSaveEdit)
        assertTrue(model.saveEdit())
        val (name, sent) = hub.updates.single()
        assertEquals("jv_gw", name)
        assertEquals("gw-mid", sent.sonnetModel)
        assertEquals(900000, sent.timeoutMs)
        assertNull("the key is kept", sent.apiKey)
        assertNull("the headers are kept", sent.headers)
        assertNull("the environment is kept", sent.env)
        assertNull(sent.models)
        assertNull(sent.opusModel)
        assertNull(sent.proxy)
        assertNull(sent.noProxy)
        assertNull(sent.subagentForce)
        assertNull("the scope is kept", sent.share)
        assertNull(sent.scopeMachines)
        assertNull(sent.machineKeys)
        assertTrue(model.state.value.saved)
        assertFalse("saved is what is open now", model.state.value.dirty)
    }

    /** A direct API connection's "Send through" goes only when it changed; a launcher never sends it (#4775). */
    @Test fun sendThroughGoesOnlyWhenChanged() {
        val api = HubLauncher("direct", source = "persona", kind = "api", baseUrl = "https://api.example.com", model = "m", viaMachine = "laptop")
        val d = providerDraftOf(api)
        assertEquals("laptop", d.viaMachine)
        assertNull(providerUpdate(d, api).viaMachine)
        assertEquals("", providerUpdate(d.copy(viaMachine = ""), api).viaMachine)
        assertNull(providerUpdate(providerDraftOf(stored).copy(viaMachine = "laptop"), stored).viaMachine)
    }

    @Test fun secretsAreKeptReplacedOrClearedOnPurpose() {
        val model = editing()
        val d = model.state.value.draft
        // A key typed replaces it; empty keeps it.
        assertEquals("sk-new", providerUpdate(d.copy(apiKey = "  sk-new "), stored).apiKey)
        assertNull(providerUpdate(d.copy(apiKey = "   "), stored).apiKey)
        // Headers: typed replace, "Clear saved" sends "", untouched keeps.
        assertEquals("x-team: a", providerUpdate(d.copy(headers = " x-team: a \n"), stored).headers)
        assertEquals("", providerUpdate(d.copy(clearHeaders = true), stored).headers)
        // The environment keeps its start (the hub's line numbers match the box), loses only its end.
        assertEquals("\nA=1", providerUpdate(d.copy(env = "\nA=1  \n"), stored).env)
        assertEquals("", providerUpdate(d.copy(clearEnv = true), stored).env)
        assertNull(providerUpdate(d, stored).env)
        // A stored key lets a probe go by name without typing it again.
        model.edit(d.copy(headers = "x-team: b"))
        val probe = model.state.value.formProbe!!
        assertEquals("jv_gw", probe.name)
        assertNull(probe.apiKey)
        assertEquals("x-team: b", probe.headers)
        // Without a stored key one is needed.
        val bare = editing(stored.copy(hasKey = false))
        bare.edit(bare.state.value.draft.copy(model = "gw-max"))
        assertEquals(R.string.settings_pedit_key_needed, bare.state.value.editProblem?.id)
        assertFalse(bare.state.value.canSaveEdit)
    }

    @Test fun theProxyAndTheModelsGoOnlyWhenChanged() {
        val d = providerDraftOf(stored)
        // No proxy stores the gateway's host; Follow with no list stores none.
        val follow = providerUpdate(d.copy(proxyMode = ProxyMode.Follow), stored)
        assertEquals("", follow.noProxy)
        assertNull(follow.proxy)
        val manual = d.copy(proxyMode = ProxyMode.Manual, proxy = "http://user:pw@127.0.0.1:7890")
        assertEquals(R.string.settings_pedit_proxy_credentials, manual.proxyProblem()?.id)
        assertEquals("socks5://127.0.0.1:1080", providerUpdate(manual.copy(proxy = "socks5://127.0.0.1:1080"), stored).proxy)
        // Unpicking a model sends the new list; the stored metadata goes with the rest.
        val fewer = providerUpdate(d.toggleModel(PresetModel("gw-mini")), stored)
        assertEquals(listOf("gw-pro", "gw-max"), fewer.models?.map { it.id })
        assertEquals(listOf("high", "max"), fewer.models?.last()?.efforts)
        // Bad lines are caught before the hub sees them, naming the line, never its value.
        assertEquals(ResText.of(R.string.settings_pedit_header_line, 2), headersProblem("a: 1\nno-colon"))
        assertEquals(ResText.of(R.string.settings_pedit_env_line, 1), envProblem("1A=x"))
        assertNull(envProblem("# a comment\nexport A=1\nB="))
    }

    @Test fun theScopeOfAPutSendsOnlyWhatChangedAndRemovesKeys() {
        val model = editing()
        val scope = model.state.value.scope!!
        // Only the delivery changed.
        model.editScope(scope.copy(delivery = Delivery.Push))
        var fields = model.state.value.scopeBody
        assertEquals(ScopeFields(delivery = Delivery.Push), fields)
        // Another computer chosen: scope_machines goes, the share stays.
        model.editScope(scope.toggle("box"))
        fields = model.state.value.scopeBody
        assertNull(fields.share)
        assertEquals(listOf("mac", "box"), fields.scopeMachines)
        // Removing a computer's key sends "" for it; Undo takes it back.
        model.editScope(scope.removeKey("mac"))
        assertEquals(mapOf("mac" to ""), model.state.value.scopeBody.machineKeys)
        model.editScope(scope.removeKey("mac").keepKey("mac"))
        assertNull(model.state.value.scopeBody.machineKeys)
        // A key typed for a computer in scope goes (trimmed); one for a computer outside it does not.
        model.editScope(scope.typeKey("mac", " k-mac ").typeKey("box", "k-box"))
        assertEquals(mapOf("mac" to "k-mac"), model.state.value.scopeBody.machineKeys)
        // Chosen computers with none chosen cannot be saved.
        model.editScope(scope.copy(machines = emptyList()))
        assertEquals(R.string.settings_pscope_pick_machine, model.state.value.editProblem?.id)
        assertFalse(model.state.value.canSaveEdit)
    }

    @Test fun aNewProvidersScopeSendsOnlyWhatDiffersFromTheDefaults() = runBlocking {
        val hub = Hub()
        val model = NewProviderModel(hub)
        model.prepare(null, scopeShown = true, machines = machines)
        model.load()
        model.edit(model.state.value.draft.copy(name = "jv_new", baseUrl = "https://gateway.example.com", apiKey = "k", model = "gw-pro"))
        // All my computers, on demand, own service not chosen: nothing of the scope goes.
        var request = model.state.value.request!!
        assertNull(request.share)
        assertNull(request.scopeMachines)
        assertNull(request.delivery)
        assertNull(request.ownService)
        assertNull(request.machineKeys)
        // Chosen computers and "skip" chosen where it is the default: the share and its computers only.
        model.editScope(ScopeForm(Share.Selected, machines = listOf("mac"), ownService = OwnService.Skip))
        request = model.state.value.request!!
        assertEquals(Share.Selected, request.share)
        assertEquals(listOf("mac"), request.scopeMachines)
        assertNull(request.ownService)
        // A direct API defaults to "use": choosing "skip" there is sent.
        model.edit(model.state.value.draft.copy(kind = "api"))
        assertEquals(OwnService.Skip, model.state.value.request!!.ownService)
        // A work computer added to all my computers goes as scope_machines.
        model.edit(model.state.value.draft.copy(kind = "launcher"))
        model.editScope(ScopeForm(work = listOf("office")))
        request = model.state.value.request!!
        assertNull(request.share)
        assertEquals(listOf("office"), request.scopeMachines)
        assertNotNull(model.save())
        assertEquals(listOf("office"), hub.added.single().scopeMachines)
    }

    @Test fun anOlderHubHasNoScopeAndSendsNone() {
        val model = NewProviderModel(Hub(scoped = false))
        model.prepare(null, scopeShown = false, machines = machines)
        assertNull(model.state.value.scope)
        val old = editing(stored.copy(share = null, scopeMachines = emptyList(), delivery = null, ownService = null, keyMachines = emptyList()))
        assertNull(old.state.value.scope)
        assertEquals(ScopeFields(), old.state.value.scopeBody)
    }

    @Test fun theComputersOfferedAreThisHubsConfirmedOnes() {
        assertEquals(listOf("mac", "box", "office"), choosable(machines, emptyList()))
        // A chosen one stays visible to uncheck, pending or gone.
        assertEquals(listOf("mac", "box", "pending", "office", "gone"), choosable(machines, listOf("pending", "gone")))
        assertEquals(listOf("office"), addableWork(machines, emptyList()))
        assertEquals(listOf("mac", "box"), keyMachines(ScopeForm(), machines))
        assertEquals(listOf("mac", "box", "office"), keyMachines(ScopeForm(work = listOf("office")), machines))
        assertTrue(keyMachines(ScopeForm(Share.None), machines).isEmpty())
        assertTrue(ownServiceDetected("launcher", "codex"))
        assertFalse(ownServiceDetected("launcher", "agy"))
        assertFalse(ownServiceDetected("api", null))
    }

    @Test fun theSummarySaysWhereHowAndWhetherSignedInComputersUseIt() {
        val name = machineName(machines)
        fun summary(l: HubLauncher) = scopeSummary(l, name, ", ").joinToString(" · ") { it.resolve(res) }
        assertEquals(listOf(R.string.settings_pscope_summary_none), scopeSummary(HubLauncher("a", share = Share.None), name, ", ").map { it.id })
        val chosen = scopeSummary(stored.copy(scopeMachines = listOf("mac", "box"), delivery = Delivery.Push, ownService = OwnService.Use), name, ", ")
        assertEquals(listOf(R.string.settings_pscope_summary_list, R.string.settings_pscope_summary_push, R.string.settings_pscope_summary_use), chosen.map { it.id })
        assertTrue(summary(stored.copy(scopeMachines = listOf("mac", "box"))).startsWith("Work Mac, Build box"))
        assertEquals(R.string.settings_pscope_summary_legacy, scopeSummary(stored.copy(share = Share.All, legacyScope = true), name, ", ").first().id)
        assertEquals(R.plurals.settings_pscope_summary_all_work, scopeSummary(stored.copy(share = Share.All, scopeMachines = listOf("office")), name, ", ").first().id)
    }

    @Test fun theListGroupsByHarnessInAFixedOrder() {
        val list = listOf(
            HubLauncher("api1", source = "persona", kind = "api"),
            HubLauncher("cx", source = "persona", agent = "codex"),
            HubLauncher("cc1", source = "persona"),
            HubLauncher("cc2", source = "persona", agent = "claude"),
            HubLauncher("pi", source = "persona", agent = "omp"),
        )
        val groups = harnessGroups(list)
        assertEquals(listOf("claude", "codex", "omp", "api"), groups.map { it.first })
        assertEquals(listOf("cc1", "cc2"), groups.first().second.map { it.name })
        assertEquals(1, harnessGroups(list.take(1)).size)
        assertEquals("Codex", harnessName("codex"))
        assertNull(harnessName("api"))
    }

    @Test fun aRefusedEditKeepsTheFormAndSaysWhy() = runBlocking {
        val hub = Hub().apply { refuse = persona.core.hub.HubFailure(persona.core.hub.HubError.Server, "Base URL 无效", status = 400) }
        val model = editing(hub = hub)
        model.edit(model.state.value.draft.copy(model = "gw-max"))
        assertFalse(model.saveEdit())
        val s = model.state.value
        assertFalse(s.saved)
        assertTrue(s.dirty)
        assertTrue(s.error?.resolve(res).orEmpty().contains("Base URL 无效"))
    }
}
