package persona.feature.settings

import android.content.Context
import android.content.res.Resources
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.DetectedEngine
import persona.core.contracts.Machine
import persona.core.contracts.api.*
import persona.core.hub.HubError
import persona.core.hub.HubFailure
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.async
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonPrimitive
import org.junit.Assert.*
import org.junit.Test
import org.robolectric.RuntimeEnvironment

@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
class EngineSelectionModelTest {
    private val res = RuntimeEnvironment.getApplication().resources
    private val machine = Machine(id = "m1", online = true, engine = "herdr",
        engines = listOf(DetectedEngine("herdr", "herdr", true), DetectedEngine("orca", "Orca", true)))

    @Test fun saveAppliesTheMachineTheHubReturned() = runBlocking {
        val applied = mutableListOf<Machine>()
        var sent: Pair<String, String>? = null
        val model = EngineSelectionModel(machine, put = { id, engine -> sent = id to engine; machine.copy(engine = engine) }, applied = { applied += it })
        model.choose("orca")
        assertTrue(model.save("m1"))
        assertEquals("m1" to "orca", sent)
        assertEquals("orca", applied.single().engine)
        assertEquals("orca", model.state.value.selected)
        assertNull(model.state.value.error)
        assertFalse(model.state.value.saving)
    }

    @Test fun aFailedSaveKeepsTheChoiceAndSaysWhy() = runBlocking {
        val applied = mutableListOf<Machine>()
        val model = EngineSelectionModel(machine, put = { _, _ -> throw HubFailure(HubError.Server, "unknown engine", 400) }, applied = { applied += it })
        model.choose("orca")
        assertFalse(model.save("m1"))
        assertEquals("保存失败：unknown engine", model.state.value.error?.resolve(res))
        assertEquals("orca", model.state.value.selected)
        assertTrue(applied.isEmpty())
        model.choose("herdr")
        assertNull(model.state.value.error)
        val offline = EngineSelectionModel(machine, put = { _, _ -> throw java.io.IOException("reset") }, applied = {})
        assertFalse(offline.save("m1"))
        assertTrue(offline.state.value.error!!.resolve(res).startsWith("保存失败：连不上 hub"))
    }

    @Test fun offlineMachinesSaveTooAndPushesMoveThePickerUntilEdited() = runBlocking {
        val offline = machine.copy(online = false, engines = emptyList())
        val model = EngineSelectionModel(offline, put = { _, engine -> offline.copy(engine = engine) }, applied = {})
        model.choose("herdr-core")
        assertTrue(model.save("m1"))
        assertEquals("herdr-core", model.state.value.selected)
        val live = EngineSelectionModel(machine, put = { _, _ -> machine }, applied = {})
        live.synchronize(machine.copy(engine = "orca"))
        assertEquals("orca", live.state.value.selected)
        live.choose("herdr")
        live.synchronize(machine.copy(engine = "orca"))
        assertEquals("herdr", live.state.value.selected)
    }

    @Test fun noSecondSaveWhileOneIsRunning() = runBlocking {
        val gate = CompletableDeferred<Unit>()
        var calls = 0
        val model = EngineSelectionModel(machine, put = { _, engine -> calls++; gate.await(); machine.copy(engine = engine) }, applied = {})
        val first = async(start = CoroutineStart.UNDISPATCHED) { model.save("m1") }
        assertTrue(model.state.value.saving)
        assertFalse(model.save("m1"))
        gate.complete(Unit)
        assertTrue(first.await())
        assertEquals(1, calls)
    }
}

// Robolectric: the words come from the app's resources, in Simplified Chinese (robolectric.properties).
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
class ClaudeSettingsModelTest {
    private val res: Resources get() = ApplicationProvider.getApplicationContext<Context>().resources

    private class Choices : ClaudeSettingsChoices {
        override var machine = ""
        override var applyAll = true
    }

    private class Api : MachineManagement {
        val writes = mutableListOf<Triple<String, String, JsonElement>>()
        val failing = mutableSetOf<String>()
        var reads = 0
        var readFails = false
        override suspend fun claudeSettings(machineId: String): List<ClaudeSetting> {
            reads++
            if (readFails) throw HubFailure(HubError.Server, "hub is unreachable", 503)
            return listOf(ClaudeSetting("language", "回复语言", value = JsonPrimitive("中文")))
        }
        override suspend fun setClaudeSetting(machineId: String, key: String, value: JsonElement): List<ClaudeSetting> {
            writes += Triple(machineId, key, value)
            if (machineId in failing) throw HubFailure(HubError.Server, "machine is offline", 503)
            return listOf(ClaudeSetting(key, "回复语言", value = value))
        }
        val codexWrites = mutableListOf<Triple<String, String, JsonElement>>()
        override suspend fun codexSettings(machineId: String) =
            listOf(ClaudeSetting("model_reasoning_effort", "思考强度", type = "enum", options = listOf("low", "high"), value = JsonPrimitive("high")))
        override suspend fun setCodexSetting(machineId: String, key: String, value: JsonElement): List<ClaudeSetting> {
            codexWrites += Triple(machineId, key, value)
            return listOf(ClaudeSetting(key, "思考强度", type = "enum", options = listOf("low", "high"), value = value))
        }
        override suspend fun launchers(machineId: String) = emptyList<AgentLauncher>()
        override suspend fun launcherQuota(machineId: String, name: String, refresh: Boolean) = persona.core.contracts.api.LauncherQuotaAnswer()
        override suspend fun reconnect(machineId: String) = Unit
        override suspend fun deploy(machineId: String) = Unit
        override suspend fun herdr(machineId: String): persona.core.contracts.api.HerdrState = error("unused")
        override suspend fun startHerdr(machineId: String): persona.core.contracts.api.HerdrState = error("unused")
        override suspend fun setEngine(machineId: String, engine: String) = Machine(machineId)
    }

    private val hub = Machine("hub", name = "服务器", kind = "local", online = true)
    private val mac = Machine("mac", name = "MacBook", kind = "ssh", online = true)

    @Test fun picksTheRememberedOnlineMachineElseTheHubsOwn() {
        assertEquals("hub", claudeSettingsMachine("", listOf(mac, hub)))
        assertEquals("mac", claudeSettingsMachine("mac", listOf(mac, hub)))
        assertEquals("hub", claudeSettingsMachine("gone", listOf(mac, hub)))
        assertEquals("mac", claudeSettingsMachine("", listOf(mac)))
        assertNull(claudeSettingsMachine("mac", emptyList()))
    }

    @Test fun writesEveryOnlineMachineAndClearingSendsNull() = runBlocking {
        val api = Api()
        val choices = Choices()
        val model = ClaudeSettingsModel(api, choices)
        model.follow(listOf(mac, hub))
        assertEquals("hub", model.state.value.machineId)
        assertEquals("hub", choices.machine)
        model.load()
        assertEquals("中文", model.state.value.settings.single().stringValue)
        assertEquals("已应用到 2 台机器", model.set("language", JsonPrimitive("English"), listOf(mac, hub))?.resolve(res))
        assertEquals(listOf("mac", "hub"), api.writes.map { it.first })
        model.applyAll(false)
        assertFalse(choices.applyAll)
        assertEquals("已保存", model.set("language", JsonNull, listOf(mac, hub))?.resolve(res))
        assertEquals(Triple("hub", "language", JsonNull as JsonElement), api.writes.last())
        assertTrue(model.state.value.settings.single().isDefault)
    }

    @Test fun codexKindReadsAndWritesCodexSettingsOnly() = runBlocking {
        val api = Api()
        val model = ClaudeSettingsModel(api, Choices(), AgentSettingsKind.Codex)
        model.follow(listOf(mac, hub))
        model.load()
        assertEquals(0, api.reads)
        assertEquals("high", model.state.value.settings.single().stringValue)
        assertEquals("已应用到 2 台机器", model.set("model_reasoning_effort", JsonPrimitive("low"), listOf(mac, hub))?.resolve(res))
        assertEquals(listOf("mac", "hub"), api.codexWrites.map { it.first })
        assertTrue(api.writes.isEmpty())
        assertEquals("low", model.state.value.settings.single().stringValue)
    }

    @Test fun eachKindHasItsOwnWords() {
        assertEquals("Codex 设置", res.getString(AgentSettingsKind.Codex.title))
        assertEquals("Claude Code 设置", res.getString(AgentSettingsKind.Claude.title))
        assertTrue(res.getString(AgentSettingsKind.Codex.caption).contains("~/.codex/config.toml"))
    }

    @Test fun failuresAreListedPerMachineAndTheTableIsReadAgain() = runBlocking {
        val api = Api().apply { failing += "mac" }
        val model = ClaudeSettingsModel(api, Choices())
        model.follow(listOf(mac, hub))
        model.load()
        val reads = api.reads
        assertNull(model.set("language", JsonPrimitive("English"), listOf(mac, hub)))
        assertEquals("没有全部保存：\nMacBook：machine is offline", model.state.value.error?.resolve(res))
        assertEquals(reads + 1, api.reads)
        // Nothing online: nothing shown.
        model.follow(emptyList())
        assertEquals("", model.state.value.machineId)
    }

    @Test fun aFailedSaveThatCannotBeReadBackGoesBackAndSaysItWasNotSaved() = runBlocking {
        val api = Api()
        val model = ClaudeSettingsModel(api, Choices())
        model.follow(listOf(hub))
        model.load()
        api.failing += "hub"
        api.readFails = true
        assertNull(model.set("language", JsonPrimitive("English"), listOf(hub)))
        // The control is back on what the machine still has, and the page says the change did not go through.
        assertEquals("中文", model.state.value.settings.single().stringValue)
        assertEquals("没有全部保存：\n服务器：machine is offline", model.state.value.error?.resolve(res))
    }
}

// Robolectric: the words come from the app's resources, in Simplified Chinese (robolectric.properties).
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
class ModelConfigurationStateTest {
    private val res: Resources get() = ApplicationProvider.getApplicationContext<Context>().resources

    private val providers = listOf(ModelProvider("launcher.claude", "claude", model = "model-chat"),
        ModelProvider("launcher.old", "旧网关", hidden = true), ModelProvider("api.fast", "轻量 API", kind = "api", api = "messages"))
    private val agent = ModelPurpose("agent.new", "新建 Agent", "对话与 Agent", effective = ModelBinding("launcher.claude", "model-chat"),
        inheritedFrom = "assistant", fields = listOf("provider", "model", "effort"), revision = 3)
    private val chat = ModelPurpose("assistant", "Persona 对话", "对话与 Agent", effective = ModelBinding("launcher.claude", "model-chat", "high"))
    private val triage = ModelPurpose("inbox.triage", "分拣与摘要", "收件箱", kind = "api", effective = ModelBinding("api.fast"), status = "供应商离线")
    private val catalog = ModelCatalog(listOf(agent, chat, triage, ModelPurpose("misc", "其他用途")), providers)

    @Test fun groupsKeepTheHubsOrderAndSearchNamesGroupsIdsAndModels() {
        assertEquals(listOf("对话与 Agent", "收件箱", "其他"), modelPurposeGroups(res, catalog.purposes, providers, "").map { it.first })
        assertEquals(listOf("inbox.triage"), modelPurposeGroups(res, catalog.purposes, providers, "分拣").flatMap { it.second }.map { it.id })
        assertEquals(listOf("agent.new", "assistant"), modelPurposeGroups(res, catalog.purposes, providers, "MODEL-CHAT").flatMap { it.second }.map { it.id })
        assertEquals(listOf("inbox.triage"), modelPurposeGroups(res, catalog.purposes, providers, "轻量").flatMap { it.second }.map { it.id })
        assertTrue(modelPurposeGroups(res, catalog.purposes, providers, "技能").isEmpty())
    }

    @Test fun rowsSayWhatIsInEffectThenStatusOrSource() {
        assertEquals("claude，model-chat\n跟随 Persona 对话", purposeSubtitle(res, agent, catalog))
        assertEquals("轻量 API\n供应商离线", purposeSubtitle(res, triage, catalog))
        assertEquals("未配置", purposeSubtitle(res, ModelPurpose("misc"), catalog))
    }

    // The uses of Agents the hub lets the user set apart: Lark /agent and the AI twin.
    private val agentsOverrides = listOf("lark.agent", "inbox.agent")

    @Test fun agentsOverrideModeIncludesPartialOverrides() {
        for (id in agentsOverrides) {
            val child = agent.copy(id = id, parent = "agent.new", inheritedFrom = "Agents")
            assertEquals("跟随 Agents", agentsOverrideMode(res, child))
            assertTrue(purposeSubtitle(res, child.copy(status = "offline"), catalog).contains("跟随 Agents"))
            for (override in listOf(ModelBinding("launcher.old"), ModelBinding(model = "custom"), ModelBinding(effort = "high"))) {
                assertEquals("单独设置", agentsOverrideMode(res, child.copy(override = override)))
            }
            assertTrue(showsEffort(child, providers[2]))
            // An older hub leaves it without fields: read-only, following Agents as any other use.
            assertNull(agentsOverrideMode(res, child.copy(fields = emptyList())))
        }
        assertNull(agentsOverrideMode(res, agent))
        assertNull(agentsOverrideMode(res, triage.copy(id = "inbox.judge", parent = "inbox.triage", fields = listOf("provider", "model"))))
    }

    @Test fun agentsOverrideEditorSavesOwnRevisionAndRestoresFollowing() = runBlocking {
        for (purposeId in agentsOverrides) {
            var saved = agent.copy(id = purposeId, parent = "agent.new", revision = 0)
            val ids = mutableListOf<String>()
            val revisions = mutableListOf<Long>()
            val api = object : ModelConfiguration {
                override suspend fun catalog() = catalog
                override suspend fun purpose(id: String): ModelPurposeDetail {
                    ids += id
                    return ModelPurposeDetail(saved, providers)
                }
                override suspend fun providerModels(provider: String, refresh: Boolean, machine: String?): ProviderModels? = null
                override suspend fun save(purpose: ModelPurpose, draft: ModelConfigurationDraft): ModelPurposeSave {
                    assertEquals(purposeId, purpose.id)
                    revisions += purpose.revision
                    saved = purpose.copy(revision = purpose.revision + 1,
                        override = ModelBinding(draft.provider, draft.model, draft.effort))
                    return ModelPurposeSave.Saved(ModelPurposeDetail(saved, providers))
                }
            }
            val model = ModelPurposeModel(api, purposeId)
            model.load()
            model.edit(ModelConfigurationDraft(provider = "launcher.old", model = "custom", effort = "high"))
            assertNotNull(model.save())
            assertEquals("单独设置", agentsOverrideMode(res, requireNotNull(model.state.value.purpose)))
            assertNotNull(model.save(restore = true))
            assertEquals("跟随 Agents", agentsOverrideMode(res, requireNotNull(model.state.value.purpose)))
            assertEquals(listOf(purposeId), ids)
            assertEquals(listOf(0L, 1L), revisions)
        }
    }

    @Test fun providerChoicesMarkHiddenAndUnavailableReferences() {
        val options = providerOptions(res, agent.copy(override = ModelBinding("launcher.old")), providers, "launcher.old")
        assertEquals(listOf("", "launcher.claude", "launcher.old"), options.map { it.id })
        assertEquals("旧网关（已隐藏）", options.last().label)
        val gone = providerOptions(res, agent, providers, "launcher.gone")
        assertEquals("launcher.gone（已经没有了）", gone.last().label)
        assertTrue(showsEffort(agent, providers[0]))
        assertFalse(showsEffort(agent, providers[2]))
        assertTrue(showsEffort(agent, providers[2].copy(api = "responses")))
        assertFalse(showsEffort(triage.copy(fields = listOf("provider", "model")), null))
    }

    @Test fun aLauncherProvidersModelIsPickedFromItsList() = runBlocking {
        var answer: ProviderModels? = ProviderModels("claude", listOf(
            AgentModel("default", "Default", resolved = "claude-opus-5-5", isDefault = true), AgentModel("opus", "Opus 5.5", resolved = "claude-opus-5-5"),
        ), machine = "desk")
        var failure: Exception? = null
        val asked = mutableListOf<Pair<String, Boolean>>()
        var askedOn: String? = null
        val api = object : ModelConfiguration {
            override suspend fun catalog() = catalog
            override suspend fun purpose(id: String) = ModelPurposeDetail(agent.copy(machineId = "lab"), providers)
            override suspend fun save(purpose: ModelPurpose, draft: ModelConfigurationDraft): ModelPurposeSave = throw IllegalStateException()
            override suspend fun providerModels(provider: String, refresh: Boolean, machine: String?): ProviderModels? {
                asked += provider to refresh
                askedOn = machine
                failure?.let { throw it }
                return answer
            }
        }
        val model = ModelPurposeModel(api, "agent.new")
        model.load()
        assertEquals("launcher.claude", model.state.value.listProvider)
        model.loadModels()
        model.loadModels() // kept: not asked again
        assertEquals(listOf("launcher.claude" to false), asked)
        assertEquals("the purpose's own machine, where it runs", "lab", askedOn)
        val state = model.state.value
        assertTrue(state.picksModel)
        // Nothing set here: the inherited model is what runs, not the launcher's default.
        assertEquals("model-chat", state.defaultModelDetail)
        model.edit(state.draft.copy(provider = "launcher.claude"))
        assertEquals("claude-opus-5-5", model.state.value.defaultModelDetail)

        // An API provider has no list; an older hub's 404 (null) or a failure leaves the plain field.
        model.edit(model.state.value.draft.copy(provider = "api.fast"))
        assertEquals("", model.state.value.listProvider)
        assertFalse(model.state.value.picksModel)
        model.edit(model.state.value.draft.copy(provider = "launcher.claude"))
        answer = null
        model.loadModels(refresh = true)
        assertFalse(model.state.value.picksModel)
        failure = HubFailure(HubError.Server, "offline", 503)
        model.loadModels(refresh = true)
        assertFalse(model.state.value.picksModel)
        assertEquals("offline", model.state.value.models.error?.resolve(res))
    }

    @Test fun aConflictKeepsTheDraftAndBlocksSavingUntilReloaded() = runBlocking {
        var result: ModelPurposeSave = ModelPurposeSave.Conflict
        var sent: ModelConfigurationDraft? = null
        val api = object : ModelConfiguration {
            override suspend fun catalog() = catalog
            override suspend fun purpose(id: String) = ModelPurposeDetail(agent, providers)
            override suspend fun save(purpose: ModelPurpose, draft: ModelConfigurationDraft): ModelPurposeSave { sent = draft; return result }
            override suspend fun providerModels(provider: String, refresh: Boolean, machine: String?): ProviderModels? = null
        }
        val model = ModelPurposeModel(api, "agent.new")
        model.load()
        model.edit(model.state.value.draft.copy(model = "next"))
        assertTrue(model.state.value.changed)
        assertNull(model.save())
        assertTrue(model.state.value.conflict)
        assertEquals("next", model.state.value.draft.model)
        assertNull(model.save())
        assertEquals("next", sent?.model)
        model.load()
        assertFalse(model.state.value.conflict)
        assertEquals("", model.state.value.draft.model)
        result = ModelPurposeSave.Saved(ModelPurposeDetail(agent.copy(revision = 4), providers))
        assertEquals("已恢复继承", model.save(restore = true)?.resolve(res))
        assertEquals(ModelConfigurationDraft.Restore, sent)
        assertEquals(4L, model.state.value.purpose?.revision)
    }

    @Test fun anOldHubIsToldApartFromOtherFailures() = runBlocking {
        val unsupported = ModelCatalogModel(object : ModelConfiguration {
            override suspend fun catalog(): ModelCatalog = throw ModelConfigurationUnsupported()
            override suspend fun purpose(id: String): ModelPurposeDetail = throw ModelConfigurationUnsupported()
            override suspend fun save(purpose: ModelPurpose, draft: ModelConfigurationDraft): ModelPurposeSave = throw ModelConfigurationUnsupported()
            override suspend fun providerModels(provider: String, refresh: Boolean, machine: String?): ProviderModels? = null
        })
        unsupported.load()
        assertTrue(unsupported.state.value.unsupported)
        assertNull(unsupported.state.value.error?.resolve(res))
        val broken = ModelCatalogModel(object : ModelConfiguration {
            override suspend fun catalog(): ModelCatalog = throw HubFailure(HubError.Server, "boom", 500)
            override suspend fun purpose(id: String): ModelPurposeDetail = throw IllegalStateException()
            override suspend fun save(purpose: ModelPurpose, draft: ModelConfigurationDraft): ModelPurposeSave = throw IllegalStateException()
            override suspend fun providerModels(provider: String, refresh: Boolean, machine: String?): ProviderModels? = null
        })
        broken.load()
        assertEquals("读取失败：boom", broken.state.value.error?.resolve(res))
    }

    /** #5084: the hub's catalog leaves the background supervisor's use out, so the page asks for it by its id. */
    @Test fun theBackgroundSupervisorsUseIsListedWhenTheHubHasIt() = runBlocking {
        val supervise = ModelPurpose(BackgroundSupervisor.PURPOSE, "后台督办", "对话与 Agent", parent = "assistant",
            effective = ModelBinding("launcher.claude", "model-chat"), inheritedFrom = "assistant")
        var answer: (String) -> ModelPurposeDetail = { ModelPurposeDetail(supervise, providers) }
        var listed = catalog
        val asked = mutableListOf<String>()
        val model = ModelCatalogModel(object : ModelConfiguration {
            override suspend fun catalog() = listed
            override suspend fun purpose(id: String): ModelPurposeDetail { asked += id; return answer(id) }
            override suspend fun save(purpose: ModelPurpose, draft: ModelConfigurationDraft): ModelPurposeSave = throw IllegalStateException()
            override suspend fun providerModels(provider: String, refresh: Boolean, machine: String?): ProviderModels? = null
        })
        fun ids() = model.state.value.catalog?.purposes?.map { it.id }
        model.load()
        // after the last use of its group, the hub's order otherwise
        assertEquals(listOf("agent.new", "assistant", "assistant.supervise", "inbox.triage", "misc"), ids())
        assertEquals(listOf("assistant.supervise"), asked)
        assertEquals("claude，model-chat\n跟随 Persona 对话", purposeSubtitle(res, supervise, requireNotNull(model.state.value.catalog)))
        // a hub without it (404), one that cannot answer, and an answer about another use: the catalog as it is
        answer = { throw ModelPurposeNotFound("no such purpose") }
        model.load()
        assertEquals(catalog.purposes.map { it.id }, ids())
        answer = { throw HubFailure(HubError.Server, "boom", 500) }
        model.load()
        assertEquals(catalog.purposes.map { it.id }, ids())
        assertNull(model.state.value.error)
        answer = { ModelPurposeDetail(agent, providers) }
        model.load()
        assertEquals(catalog.purposes.map { it.id }, ids())
        // a hub that lists it itself is not asked, and it is not listed twice
        asked.clear()
        listed = catalog.copy(purposes = catalog.purposes + supervise)
        model.load()
        assertEquals(listOf("agent.new", "assistant", "inbox.triage", "misc", "assistant.supervise"), ids())
        assertTrue(asked.isEmpty())
        // a group no listed use is in: at the end
        assertEquals(listOf("agent.new", "assistant", "inbox.triage", "misc", "assistant.supervise"),
            modelPurposesWith(catalog.purposes, supervise.copy(group = "主对话")).map { it.id })
    }
}
