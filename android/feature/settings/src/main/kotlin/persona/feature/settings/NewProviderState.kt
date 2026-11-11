package persona.feature.settings

import android.content.res.Resources
import persona.core.contracts.api.AssignPurposes
import persona.core.contracts.Machine
import persona.core.contracts.api.AssistantReadiness
import persona.core.contracts.api.HubLauncher
import persona.core.contracts.api.PresetModel
import persona.core.contracts.api.NewProvider
import persona.core.contracts.api.ProviderAdded
import persona.core.contracts.api.ProviderDiscovery
import persona.core.contracts.api.ProviderPreset
import persona.core.contracts.api.ProviderProbe
import persona.core.contracts.api.ProviderSetup
import persona.core.contracts.api.ProviderTest
import persona.core.contracts.api.ReadinessStep
import persona.core.designsystem.UiText
import persona.core.designsystem.failureReasonText
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update

/**
 * "New model provider" (docs/integrations/launchers.md "Adding from extensions"): typed in, or - when the hub's extensions offer presets - taken from one,
 * where only the name, the key and maybe another of its models are filled in and the hub adds the rest.
 */
internal data class NewProviderDraft(
    val fromPreset: Boolean = false,
    val name: String = "",
    val apiKey: String = "",
    /** launcher | api */
    val kind: String = "launcher",
    /** messages | responses, for an API. */
    val api: String = "messages",
    val baseUrl: String = "",
    /** api_key (x-api-key, the hub's default) | bearer, for a gateway typed in. */
    val auth: String = "api_key",
    val model: String = "",
    val smallModel: String = "",
    val subagentModel: String = "",
    val args: String = "",
    /** The picked preset's id and the model picked from it (its default when untouched). */
    val preset: String = "",
    val presetModel: String = "",
    // "Advanced" (docs/integrations/launchers.md "Provider model and gateway parameters"); an existing provider opens with its own.
    /** The harness a launcher runs in: claude | codex | agy | omp (kept as it is; the form does not change it). */
    val agent: String = "claude",
    /** The models picked, in order; the default is [model] (put first when not among them). */
    val models: List<PresetModel> = emptyList(),
    val opusModel: String = "",
    val sonnetModel: String = "",
    val haikuModel: String = "",
    val fableModel: String = "",
    val subagentForce: Boolean = false,
    /** Default thinking level; "" is the model's. */
    val effort: String = "",
    /** Numbers as typed; empty or 0 is the default. */
    val timeoutMs: String = "",
    val compactWindow: String = "",
    val maxContext: String = "",
    val proxyMode: ProxyMode = ProxyMode.Follow,
    /** "Send through" (an existing direct API connection, #4775): a computer's id, "" the hub itself. */
    val viaMachine: String = "",
    val proxy: String = "",
    val noProxy: String = "",
    /** Typed headers, "Name: value" a line; never the stored ones. Empty keeps them unless [clearHeaders]. */
    val headers: String = "",
    val clearHeaders: Boolean = false,
    /** Typed environment, NAME=value a line; never the stored one. Empty keeps it unless [clearEnv]. */
    val env: String = "",
    val clearEnv: Boolean = false,
    val customModel: String = "",
    val customModelName: String = "",
    val customModelDescription: String = "",
)

/**
 * Where "New model provider" is on a hub with built-in presets (docs/assistant/onboarding.md 4.3 "Three paths" B): [Pick] a provider, paste its
 * [Key] (tested by itself) and see what now runs on it ([Done]); a gateway typed in is the [Form]. An older hub opens on the
 * [Form] with its "Fill in" / "From extension" switch.
 */
internal enum class WizardStep { Pick, Key, Done, Form }

/** A preset key's test: running, or what the hub answered. */
internal data class KeyTest(val running: Boolean, val result: ProviderTest? = null, val failure: UiText? = null)

/** What saving from a preset gave: the hub's answer, whether assign was asked, and whether the computer lacked Claude Code before. */
internal data class WizardAdded(val result: ProviderAdded, val asked: Boolean, val noClaudeBefore: Boolean)

internal data class NewProviderState(
    val presets: List<ProviderPreset> = emptyList(),
    val loaded: Boolean = false,
    val draft: NewProviderDraft = NewProviderDraft(),
    val busy: Boolean = false,
    val error: ResText? = null,
    /** The hub names a provider from a preset and takes assign ([persona.core.contracts.api.BuiltinPresetsFeature]). */
    val hubNames: Boolean = false,
    val step: WizardStep = WizardStep.Form,
    /** Why the presets could not be read (the pick list still offers the gateway typed in). */
    val presetsError: ResText? = null,
    val keyTest: KeyTest? = null,
    /** "Use it for the main conversation and new agents"; null: not touched, so it follows the readiness (off while Claude Code already answers). */
    val assignSet: Boolean? = null,
    /** The main conversation's readiness, asked when the page opens and again after saving; null without it. */
    val readiness: AssistantReadiness? = null,
    /** The first readiness answer has not come: the switch and "Save" wait for it. */
    val readinessPending: Boolean = false,
    val added: WizardAdded? = null,
    /** "Test connection" / "Fetch models" of the form. */
    val probing: Boolean = false,
    val probe: ProviderTest? = null,
    /** The hub could not be asked (not the gateway's answer, which is in [probe] / [found]). */
    val probeFailure: UiText? = null,
    val discovering: Boolean = false,
    val found: ProviderDiscovery? = null,
    val foundFailure: UiText? = null,
    /** The provider being edited (Persona-managed); null while adding one. */
    val existing: HubLauncher? = null,
    /** The "Computers" page's form; null on a hub without device scopes ([persona.core.contracts.api.LauncherScopeFeature]). */
    val scope: ScopeForm? = null,
    /** The machines the scope is chosen from (this hub's). */
    val machines: List<Machine> = emptyList(),
    /** The form and the scope as editing opened, to ask before throwing changes away. */
    val opened: Pair<NewProviderDraft, ScopeForm?>? = null,
    /** Saved: the page closes. */
    val saved: Boolean = false,
) {
    val editing: Boolean get() = existing != null

    /** Something was changed since editing opened; adding never asks. */
    val dirty: Boolean get() = opened?.let { (d, sc) -> d != draft || sc != scope } ?: false

    /** The hub's default for own_service: use when the provider is saved with assign, or is a direct API. */
    val defaultUse: Boolean get() = when {
        existing != null -> existing.isApi
        step == WizardStep.Key -> hubNames && assign || selected?.kind == "api"
        usesPreset -> selected?.kind == "api"
        else -> draft.kind == "api"
    }

    /** The scope's fields for the save (none on a hub without scopes). */
    val scopeBody: ScopeFields get() = scope?.let { scopeFields(it, existing, machines, defaultUse) } ?: ScopeFields()

    /** What keeps the edit from being saved; null when it can go. */
    val editProblem: ResText? get() {
        val l = existing ?: return null
        val d = draft
        if ((d.kind == "api" || d.agent == "claude") && gatewayHost(d.baseUrl).isEmpty()) return ResText.of(R.string.settings_pedit_url_needed)
        if (d.model.isBlank()) return ResText.of(R.string.settings_pedit_model_needed)
        if ((d.kind == "api" || d.agent == "claude") && d.apiKey.isBlank() && !l.hasKey) return ResText.of(R.string.settings_pedit_key_needed)
        return d.advancedProblem() ?: scope?.problem
    }
    val canSaveEdit: Boolean get() = editing && !busy && dirty && editProblem == null

    /** PUT's body for the edit. */
    val update: persona.core.contracts.api.ProviderUpdate? get() = existing?.let { providerUpdate(draft, it, scopeBody) }

    /** The main conversation already answers on a computer's Claude Code: a new key does not take it over by default. */
    val works: Boolean get() = claudeWorks(readiness)
    val assign: Boolean get() = assignSet ?: !works

    /** The key step can be saved: a key, a fine name if any, not while its test runs or the readiness is still asked. */
    val canSaveKey: Boolean get() {
        if (busy || selected == null || draft.apiKey.isBlank() || keyTest?.running == true || scope?.problem != null) return false
        if (!hubNames && draft.name.isBlank()) return false
        return nameProblem == null
    }

    /** What "Test connection" / "Fetch models" send for the form: the gateway, the key and the auth typed in. */
    val formProbe: ProviderProbe? get() {
        val d = draft
        val stored = existing?.takeIf { it.hasKey }
        if (d.baseUrl.isBlank() || d.apiKey.isBlank() && stored == null) return null
        // Editing: by name, so the hub uses the stored key and headers for what is not typed over them.
        return ProviderProbe(baseUrl = d.baseUrl.trim(), apiKey = d.apiKey.trim().takeIf { it.isNotEmpty() }, auth = d.auth,
            model = d.model.trim().takeIf { it.isNotEmpty() }, name = existing?.name, headers = d.headersValue().takeIf { !d.chat },
            api = d.api.takeIf { d.kind == "api" }, viaMachine = d.viaMachine.takeIf { d.kind == "api" && existing != null })
    }

    val usesPreset: Boolean get() = draft.fromPreset && selected != null
    val selected: ProviderPreset? get() = presets.firstOrNull { it.id == draft.preset }
    val nameProblem: ResText? get() = providerNameProblem(draft.name)

    /** What is sent; null while something required is missing. */
    val request: NewProvider? get() {
        val d = draft
        val name = d.name.trim()
        val key = d.apiKey.trim()
        if (name.isEmpty() || nameProblem != null || key.isEmpty()) return null
        val fields = scopeBody
        selected?.takeIf { d.fromPreset }?.let { preset ->
            val model = d.presetModel.trim()
            return NewProvider(name, key, preset = preset.id, model = model.takeIf { it.isNotEmpty() && it != preset.defaultModel },
                share = fields.share, scopeMachines = fields.scopeMachines, delivery = fields.delivery, ownService = fields.ownService,
                machineKeys = fields.machineKeys)
        }
        val api = d.kind == "api"
        if (d.baseUrl.isBlank() || d.model.isBlank() || d.advancedProblem() != null || scope?.problem != null) return null
        return NewProvider(
            name, key, kind = d.kind, api = if (api) d.api else "messages", baseUrl = d.baseUrl.trim(), model = d.model.trim(),
            auth = d.auth.takeIf { !api && it == "bearer" },
            smallModel = d.smallModel.trim().takeIf { !api && it.isNotEmpty() },
            subagentModel = d.subagentModel.trim().takeIf { !api && it.isNotEmpty() },
            args = d.args.split(Regex("\\s+")).filter { it.isNotEmpty() }.takeIf { !api && it.isNotEmpty() },
        ).withExtras(d, fields)
    }
    val canSave: Boolean get() = !busy && request != null
}

/** What is wrong with a provider's name, the way the hub would put it; null while empty or fine. */
internal fun providerNameProblem(name: String): ResText? {
    val n = name.trim()
    return when {
        n.isEmpty() -> null
        !Regex("^[A-Za-z_][A-Za-z0-9_-]*$").matches(n) -> ResText.of(R.string.settings_src_provider_name_invalid)
        n == "claude" || n == "presets" -> ResText.of(R.string.settings_src_provider_name_reserved, n)
        else -> null
    }
}

/** "Direct API · api.example.com", "Claude Code launcher · gateway.example.com". */
internal fun presetSubtitle(res: Resources, preset: ProviderPreset): String {
    val kind = res.getString(if (preset.kind == "api") R.string.settings_src_provider_kind_api else R.string.settings_src_provider_kind_launcher)
    val host = runCatching { java.net.URI(preset.baseUrl).host }.getOrNull()
    return if (host.isNullOrBlank()) kind else res.getString(R.string.settings_src_provider_kind_host, kind, host)
}

/** "Model configuration"'s id of a provider added under [name]. */
internal fun providerId(name: String) = "launcher.${name.trim()}"

/** The main conversation answers on a computer's Claude Code: its computer, Claude Code and model steps are all ok. */
internal fun claudeWorks(r: AssistantReadiness?): Boolean {
    if (r == null) return false
    fun ok(id: String) = r.steps.firstOrNull { it.id == id }?.state == ReadinessStep.OK
    return ok(ReadinessStep.COMPUTER) && ok(ReadinessStep.CLAUDE) && ok(ReadinessStep.MODEL)
}

/** The main conversation's computer has no Claude Code (4.3 B.4): a key alone does not make it answer. */
internal fun claudeMissing(r: AssistantReadiness?): Boolean = r?.steps.orEmpty().any {
    it.id == ReadinessStep.CLAUDE && it.code == "claude_missing" && (it.state == ReadinessStep.PROBLEM || it.state == ReadinessStep.TODO)
}

/**
 * Still no Claude Code after saving: now, or before saving and not found since (after it the step may read `unknown`:
 * the hub does not say a provider of its own misses claude).
 */
internal fun stillNoClaude(added: WizardAdded, now: AssistantReadiness?): Boolean =
    claudeMissing(now) || (added.noClaudeBefore && now?.steps?.firstOrNull { it.id == ReadinessStep.CLAUDE }?.state != ReadinessStep.OK)

/** A pasted key long enough to test by itself: a test costs a request, so not on every keystroke of a typed one. */
internal fun testableKey(key: String) = key.trim().length >= 8

internal class NewProviderModel(
    private val api: ProviderSetup,
    /** The main conversation's readiness (null: the hub has none, or demo mode). */
    private val readiness: (suspend () -> AssistantReadiness?)? = null,
) {
    private val mutable = MutableStateFlow(NewProviderState())
    val state = mutable.asStateFlow()
    private var readinessAnswer: CompletableDeferred<Unit>? = null
    private var testSeq = 0

    /**
     * The presets; the first is picked, and [preferPreset] opens on "From extension" when there are any. [hubNames]: the hub has
     * built-in presets, so the page opens on the list to pick from (the readiness is asked meanwhile).
     */
    suspend fun load(preferPreset: Boolean = false, hubNames: Boolean = false) {
        if (hubNames) mutable.update { it.copy(hubNames = true, step = WizardStep.Pick) }
        try {
            val presets = api.presets()
            mutable.update { s ->
                val first = presets.firstOrNull()
                val keep = presets.any { it.id == s.draft.preset }
                s.copy(presets = presets, loaded = true, draft = if (keep || first == null || hubNames) s.draft
                    else s.draft.copy(preset = first.id, presetModel = first.defaultModel, fromPreset = preferPreset || s.draft.fromPreset))
            }
        } catch (cancelled: CancellationException) { throw cancelled }
        // "Fill in" still works without them.
        catch (error: Exception) {
            mutable.update { it.copy(loaded = true, presetsError = if (hubNames) ResText.of(R.string.settings_onb_presets_failed, failureReasonText(error)) else null) }
        }
    }

    /**
     * Editing [existing] (Persona-managed): the form opens on it, prefilled (its secrets never are), at the form's step;
     * [scopeShown] adds the "Computers" page's form (its own scope, or a new provider's defaults) with [machines] to pick from.
     */
    fun prepare(existing: HubLauncher?, scopeShown: Boolean, machines: List<Machine>) = mutable.update { s ->
        if (existing != null) {
            val draft = providerDraftOf(existing)
            val scope = scopeFrom(existing)
            s.copy(existing = existing, draft = draft, scope = scope, machines = machines, opened = draft to scope, step = WizardStep.Form,
                loaded = true, saved = false, error = null)
        } else s.copy(scope = if (scopeShown) s.scope ?: ScopeForm() else null, machines = machines)
    }

    /** The machines changed (one came online, another was confirmed): the lists follow, what was chosen stays. */
    fun setMachines(machines: List<Machine>) = mutable.update { it.copy(machines = machines) }

    fun editScope(scope: ScopeForm) = mutable.update { if (it.busy || it.scope == null) it else it.copy(scope = scope, error = null) }

    /** Saves an edit with `PUT`: only what changed goes. False (with the reason in [NewProviderState.error]) when the hub refused it. */
    suspend fun saveEdit(): Boolean {
        val current = state.value
        val existing = current.existing ?: return false
        val update = current.update ?: return false
        if (!current.canSaveEdit) return false
        mutable.update { it.copy(busy = true, error = null) }
        return try {
            api.update(existing.name, update)
            mutable.update { it.copy(saved = true, opened = it.draft to it.scope) }
            true
        } catch (cancelled: CancellationException) { throw cancelled }
        catch (error: Exception) { mutable.update { it.copy(error = ResText.of(R.string.settings_src_not_saved, failureReasonText(error))) }; false }
        finally { mutable.update { it.copy(busy = false) } }
    }

    /** Asks the readiness once (the wizard's switch follows it); saving waits for this answer. */
    suspend fun loadReadiness() {
        val source = readiness ?: return
        val answer = CompletableDeferred<Unit>().also { readinessAnswer = it }
        mutable.update { it.copy(readinessPending = true) }
        try { mutable.update { it.copy(readiness = source()) } }
        catch (cancelled: CancellationException) { throw cancelled }
        // A hub without it, or a moment offline: decide as if nothing answers yet (a new key takes over).
        catch (_: Exception) {}
        finally { mutable.update { it.copy(readinessPending = false) }; answer.complete(Unit) }
    }

    /** ①: a preset goes on to its key; null is the gateway typed in. */
    fun choose(id: String?) = mutable.update { s ->
        if (s.busy) return@update s
        val preset = id?.let { p -> s.presets.firstOrNull { it.id == p } }
        if (preset == null) s.copy(step = WizardStep.Form, error = null, draft = s.draft.copy(fromPreset = false))
        else s.copy(step = WizardStep.Key, error = null, keyTest = null,
            draft = if (preset.id == s.draft.preset) s.draft.copy(fromPreset = true)
                else s.draft.copy(fromPreset = true, preset = preset.id, presetModel = preset.defaultModel, name = ""))
    }

    /** Back from ② or the form to ①. */
    fun backToPick() = mutable.update { if (it.busy || !it.hubNames) it else it.copy(step = WizardStep.Pick, error = null, keyTest = null, probe = null, found = null) }

    fun setAssign(on: Boolean) = mutable.update { it.copy(assignSet = on) }

    /**
     * ②'s test of the key as it is now: the hub fills in the preset's address and its default model. A test overtaken by
     * another edit lands nowhere. Nothing for a key too short, or a protocol the hub cannot test.
     */
    suspend fun testKey() {
        val s = state.value
        val preset = s.selected ?: return
        if (s.step != WizardStep.Key || !preset.testable || !testableKey(s.draft.apiKey) || !s.hubNames) return
        val seq = ++testSeq
        val key = s.draft.apiKey
        val model = s.draft.presetModel.trim().takeIf { it.isNotEmpty() && it != preset.defaultModel }
        mutable.update { it.copy(keyTest = KeyTest(true)) }
        val answer = try { KeyTest(false, api.test(ProviderProbe(preset = preset.id, apiKey = key.trim(), auth = preset.auth ?: "api_key", model = model))) }
        catch (cancelled: CancellationException) { if (seq == testSeq) mutable.update { it.copy(keyTest = null) }; throw cancelled }
        catch (error: Exception) { KeyTest(false, failure = failureReasonText(error)) }
        if (seq == testSeq && state.value.draft.apiKey == key) mutable.update { it.copy(keyTest = answer) }
    }

    /** ②: a new key throws away the last test (the page tests it again once it stops changing). */
    fun editKey(draft: NewProviderDraft) = mutable.update {
        if (it.busy) it else it.copy(draft = draft, error = null, keyTest = if (draft.apiKey != it.draft.apiKey || draft.presetModel != it.draft.presetModel) null else it.keyTest)
    }

    /**
     * ② saved: a Claude-compatible preset takes `assign` when asked (after the readiness answer) and refreshes readiness
     * for ③. A direct API cannot run the main conversation or agents, so neither readiness nor assign concerns it.
     */
    suspend fun saveKey(): Boolean {
        val current = state.value
        val preset = current.selected ?: return false
        if (!current.canSaveKey) return false
        mutable.update { it.copy(busy = true, error = null) }
        return try {
            if (preset.runsClaude) readinessAnswer?.await()
            val before = state.value.readiness
            val give = preset.runsClaude && current.hubNames && (state.value.assignSet ?: !claudeWorks(before))
            val d = state.value.draft
            val model = d.presetModel.trim().takeIf { it.isNotEmpty() && it != preset.defaultModel }
            val now = state.value
            val fields = now.scope?.let { scopeFields(it, null, now.machines, give || preset.kind == "api") } ?: ScopeFields()
            val result = api.create(NewProvider(d.name.trim(), d.apiKey.trim(), preset = preset.id, model = model,
                assign = AssignPurposes.takeIf { give }, share = fields.share, scopeMachines = fields.scopeMachines,
                delivery = fields.delivery, ownService = fields.ownService, machineKeys = fields.machineKeys))
            mutable.update { it.copy(step = WizardStep.Done, added = WizardAdded(result, give, preset.runsClaude && claudeMissing(before))) }
            if (preset.runsClaude) readiness?.let { source ->
                try { mutable.update { it.copy(readiness = source()) } }
                catch (cancelled: CancellationException) { throw cancelled }
                catch (_: Exception) {}
            }
            true
        } catch (cancelled: CancellationException) { throw cancelled }
        catch (error: Exception) { mutable.update { it.copy(error = ResText.of(R.string.settings_src_not_saved, failureReasonText(error))) }; false }
        finally { mutable.update { it.copy(busy = false) } }
    }

    /** The form's "Test connection": one request to the gateway typed in. */
    suspend fun probeForm() {
        val probe = state.value.formProbe ?: return
        if (state.value.probing) return
        mutable.update { it.copy(probing = true, probe = null, probeFailure = null) }
        try {
            val result = api.test(probe.copy(model = probe.model ?: state.value.found?.models?.firstOrNull()?.id))
            mutable.update { it.copy(probing = false, probe = result) }
        } catch (cancelled: CancellationException) { mutable.update { it.copy(probing = false) }; throw cancelled }
        catch (error: Exception) { mutable.update { it.copy(probing = false, probeFailure = failureReasonText(error)) } }
    }

    /** The form's "Fetch models": the gateway's models; the first fills an empty default model. */
    suspend fun discoverForm() {
        val probe = state.value.formProbe ?: return
        if (state.value.discovering) return
        mutable.update { it.copy(discovering = true, found = null, foundFailure = null) }
        val result = try { api.discover(probe) }
        catch (cancelled: CancellationException) { mutable.update { it.copy(discovering = false) }; throw cancelled }
        catch (error: Exception) { mutable.update { it.copy(discovering = false, foundFailure = failureReasonText(error)) }; return }
        mutable.update { s ->
            val first = result.models.firstOrNull()?.id
            val draft = (if (s.draft.model.isBlank() && first != null) s.draft.copy(model = first) else s.draft).withFound(result.models)
            s.copy(discovering = false, found = result, draft = draft)
        }
    }

    fun edit(draft: NewProviderDraft) = mutable.update { if (it.busy) it else it.copy(draft = draft, error = null) }

    /** Picking another preset resets the model to its default. */
    fun pick(id: String) = mutable.update { s ->
        val preset = s.presets.firstOrNull { it.id == id } ?: return@update s
        if (s.busy) s else s.copy(draft = s.draft.copy(preset = id, presetModel = preset.defaultModel))
    }

    /** The added provider's "Model configuration" id; null (with the reason in [NewProviderState.error]) when the hub refused it. */
    suspend fun save(): String? {
        val current = state.value
        val request = current.request ?: return null
        if (current.busy) return null
        mutable.update { it.copy(busy = true, error = null) }
        return try {
            api.add(request)
            providerId(request.name)
        } catch (cancelled: CancellationException) { throw cancelled }
        catch (error: Exception) { mutable.update { it.copy(error = ResText.of(R.string.settings_src_not_saved, failureReasonText(error))) }; null }
        finally { mutable.update { it.copy(busy = false) } }
    }
}
