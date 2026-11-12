package persona.core.contracts.api

import persona.core.contracts.ProtocolCompatibility

import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.flowOf
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/** The hub's hello feature for built-in presets, a name it makes up and `assign` (docs/integrations/launchers.md "Built-in presets"). */
const val BuiltinPresetsFeature = "builtin_presets"

/**
 * The hub's hello feature for a Persona-managed provider's device scope (docs/integrations/launchers.md "Device scope and on-demand
 * delivery"): without it a new provider gets no "Computers" page and an older hub's providers show none.
 */
const val LauncherScopeFeature = "launcher_scope"

/** The categories a provider added from the wizard takes over while they are still at their default (docs/assistant/onboarding.md 5.4). */
val AssignPurposes = listOf("assistant", "agent.new")

/**
 * "Add model provider" (docs/integrations/launchers.md "API" "Adding from an extension"): a provider kept by the hub (gateway, key sealed there, models),
 * typed in or taken from an extension's preset. Bound to the active pairing, like model configuration.
 */
interface ProviderSetup {
    /** Pin the wizard and its follow-up requests to the assistant that opened it. */
    fun forManager(manager: String): ProviderSetup = ManagerProviderSetup(this, manager)

    /** `GET /api/v1/launchers/presets`: the providers the hub's extensions offer; none from a hub without them (404). */
    suspend fun presets(): List<ProviderPreset>

    /** `POST /api/v1/launchers`; the hub's refusal (a taken name, a bad address) comes back as its message. */
    suspend fun add(provider: NewProvider)

    /**
     * Whether the selected hub has built-in presets ([BuiltinPresetsFeature]): it names a provider added from one itself
     * and takes `assign`. False from older hubs, which need the name and know no assign.
     */
    val builtinPresets: Flow<Boolean> get() = flowOf(false)

    /** Whether the selected hub takes a provider's device scope ([LauncherScopeFeature]); false from older hubs. */
    val launcherScope: Flow<Boolean> get() = flowOf(false)

    /** `POST /api/v1/launchers` with the hub's answer: the name it gave and, with `assign`, what now runs on it. */
    suspend fun create(provider: NewProvider): ProviderAdded {
        add(provider)
        return ProviderAdded(name = provider.name)
    }

    /** `POST /api/v1/launchers/test`: one 1-token request to the gateway; always an answer, [ProviderTest.error] says why not. */
    suspend fun test(probe: ProviderProbe): ProviderTest = throw UnsupportedOperationException()

    /** `POST /api/v1/launchers/discover`: the models the gateway lists; [ProviderDiscovery.error] when it could not be asked. */
    suspend fun discover(probe: ProviderProbe): ProviderDiscovery = throw UnsupportedOperationException()

    /**
     * `PUT /api/v1/launchers/{name}`: a Persona-managed one changed. The base fields always go (the hub replaces them); the
     * others only when they changed, so what is left out keeps what is stored, the key, headers and environment included.
     */
    suspend fun update(name: String, changes: ProviderUpdate): Unit = throw UnsupportedOperationException()

    /** `GET /api/v1/launchers`: every provider, kept by Persona or found in a machine's ~/.zshrc (hidden ones too). */
    suspend fun launchers(): List<HubLauncher> = throw UnsupportedOperationException()

    /** `GET /api/v1/launchers/{name}`: one, a ~/.zshrc one with its function on each machine (secrets masked). */
    suspend fun launcher(name: String): HubLauncher = throw UnsupportedOperationException()

    /**
     * `DELETE /api/v1/launchers/{name}`: a Persona one is deleted with its key (the hub refuses one a use runs on); a
     * ~/.zshrc one is only hidden in Persona, the file is not touched.
     */
    suspend fun remove(name: String): Unit = throw UnsupportedOperationException()

    /** `POST /api/v1/launchers/{name}/restore`: a hidden ~/.zshrc one is listed again. */
    suspend fun restore(name: String): Unit = throw UnsupportedOperationException()

    /** `POST /api/v1/launchers/{name}/check?machine=`: one real request through it ([machine] null: the assistant's). */
    suspend fun check(name: String, machine: String?): LauncherCheckAnswer = throw UnsupportedOperationException()

    /** `GET /api/v1/launchers/import?machine=`: what the machine's claude aliases come to, merged by gateway and key. */
    suspend fun importPreview(machine: String): List<ImportedProvider> = throw UnsupportedOperationException()

    /** `POST /api/v1/launchers/import`: the ticked [providers]; the hub reads the keys on the machine again itself. */
    suspend fun import(machine: String, providers: List<String>): LauncherImportResult = throw UnsupportedOperationException()
}

/** One provider of Settings › Model providers (the hub's `LauncherView`): never its key, headers or environment, only whether it has them. */
@Serializable
data class HubLauncher(
    @SerialName("name") val name: String,
    /** The name the user gave it, shown instead of [name] where set. */
    @SerialName("display_name") val displayName: String? = null,
    /** persona (kept by Persona) | zshrc (a machine's ~/.zshrc function) | path (a wrapper script on its PATH) */
    @SerialName("source") val source: String = "zshrc",
    /** launcher | api */
    @SerialName("kind") val kind: String? = null,
    @SerialName("api") val api: String? = null,
    @SerialName("base_url") val baseUrl: String? = null,
    @SerialName("model") val model: String? = null,
    @SerialName("small_model") val smallModel: String? = null,
    @SerialName("subagent_model") val subagentModel: String? = null,
    @SerialName("custom_model") val customModel: String? = null,
    @SerialName("args") val args: List<String> = emptyList(),
    @SerialName("has_key") val hasKey: Boolean = false,
    @SerialName("has_headers") val hasHeaders: Boolean = false,
    /** Its own environment for the launch (#5953), sealed at the hub like the headers. */
    @SerialName("has_env") val hasEnv: Boolean = false,
    /** api_key (x-api-key) | bearer */
    @SerialName("auth") val auth: String? = null,
    /** ~/.zshrc ones: the machines whose ~/.zshrc defines it. */
    @SerialName("machines") val machines: List<String> = emptyList(),
    @SerialName("hidden") val hidden: Boolean = false,
    @SerialName("models") val models: List<PresetModel> = emptyList(),
    /** "" made by hand, "zshrc:<alias>@<machine>", "preset:<id>". */
    @SerialName("origin") val origin: String? = null,
    /** The detail's: each machine's function body, keys masked. */
    @SerialName("definitions") val definitions: Map<String, String> = emptyMap(),
    /** The harness a launcher runs in: claude (also empty) | codex | agy | omp. */
    @SerialName("agent") val agent: String? = null,
    @SerialName("custom_model_name") val customModelName: String? = null,
    @SerialName("custom_model_description") val customModelDescription: String? = null,
    // A managed one's model and gateway parameters (docs/integrations/launchers.md "Provider model and gateway parameters").
    @SerialName("opus_model") val opusModel: String? = null,
    @SerialName("sonnet_model") val sonnetModel: String? = null,
    @SerialName("haiku_model") val haikuModel: String? = null,
    @SerialName("fable_model") val fableModel: String? = null,
    @SerialName("subagent_force") val subagentForce: Boolean = false,
    @SerialName("proxy") val proxy: String? = null,
    @SerialName("no_proxy") val noProxy: String? = null,
    @SerialName("timeout_ms") val timeoutMs: Int = 0,
    @SerialName("compact_window") val compactWindow: Int = 0,
    @SerialName("max_context") val maxContext: Int = 0,
    @SerialName("effort") val effort: String? = null,
    // A managed one's device scope ("Device scope and on-demand delivery"); all absent from an older hub.
    /** all | selected | none */
    @SerialName("share") val share: String? = null,
    /** With selected: the computers it runs on; with all: the work computers added to it. */
    @SerialName("scope_machines") val scopeMachines: List<String> = emptyList(),
    /** on_demand | push */
    @SerialName("delivery") val delivery: String? = null,
    /** skip | use */
    @SerialName("own_service") val ownService: String? = null,
    /** The computers with a key of their own (the keys never come back). */
    @SerialName("key_machines") val keyMachines: List<String> = emptyList(),
    /** Saved before scopes existed: every computer, work ones too, each with a copy, until a scope field is saved. */
    @SerialName("legacy_scope") val legacyScope: Boolean = false,
    /** A direct API connection's "Send through": the computer whose node sends its requests; none: the hub itself. */
    @SerialName("via_machine") val viaMachine: String? = null,
) {
    val managed: Boolean get() = ProtocolCompatibility.managedProvider(source)
    val pathScript: Boolean get() = source == "path"
    val isApi: Boolean get() = kind == "api"

    /** The harness it runs in, for grouping: a direct API its own ("api"), a launcher its agent ("claude" when none is said). */
    val harness: String get() = if (isApi) "api" else agent?.takeIf { it.isNotBlank() } ?: "claude"

    /** A managed one on a hub that knows device scopes. */
    val hasScope: Boolean get() = managed && !share.isNullOrBlank()

    /** What it is called in lists. */
    val title: String get() = displayName?.takeIf { it.isNotBlank() } ?: name
}

/** What "Test availability" answered: [machine] it ran on (none for a direct API), and the result. */
data class LauncherCheckAnswer(val machine: String?, val check: LauncherCheck)

@Serializable
data class LauncherCheck(
    @SerialName("ok") val ok: Boolean = false,
    @SerialName("agent") val agent: String? = null,
    @SerialName("model") val model: String? = null,
    @SerialName("answer") val answer: String? = null,
    @SerialName("millis") val millis: Int = 0,
    @SerialName("error") val error: String? = null,
)

/** One provider "Import from ~/.zshrc" found: merged from [aliases]; [existing] is the stored one it would add models to. */
@Serializable
data class ImportedProvider(
    @SerialName("name") val name: String,
    @SerialName("base_url") val baseUrl: String = "",
    @SerialName("model") val model: String = "",
    @SerialName("has_key") val hasKey: Boolean = false,
    @SerialName("aliases") val aliases: List<String> = emptyList(),
    @SerialName("existing") val existing: String? = null,
    @SerialName("models") val models: List<PresetModel> = emptyList(),
)

@Serializable
data class LauncherImportResult(
    @SerialName("created") val created: List<String> = emptyList(),
    @SerialName("updated") val updated: List<String> = emptyList(),
)

/** One provider an extension offers: the address, the kind and the models come from it; the user adds a name and a key. */
@Serializable
data class ProviderPreset(
    /** `<extension>/<provider>`. */
    @SerialName("id") val id: String,
    @SerialName("plugin") val extension: String = "",
    @SerialName("name") val name: String = "",
    @SerialName("description") val description: String? = null,
    /** launcher | api */
    @SerialName("kind") val kind: String = "launcher",
    @SerialName("api") val api: String? = null,
    @SerialName("base_url") val baseUrl: String = "",
    @SerialName("model") val model: String? = null,
    @SerialName("small_model") val smallModel: String? = null,
    @SerialName("models") val models: List<PresetModel> = emptyList(),
    /** What the key looks like or where to get one. */
    @SerialName("key_hint") val keyHint: String? = null,
    /** Why the extension's own list of models could not be had (the declared ones are still there). */
    @SerialName("models_error") val modelsError: String? = null,
    /** How the gateway takes the key: api_key (x-api-key) | bearer; built-in presets only. */
    @SerialName("auth") val auth: String? = null,
    /** Where to open an account or get a key (built-in presets); opened in the app. */
    @SerialName("signup_url") val signupUrl: String? = null,
    /** The name in the request's language ("Zhipu GLM"); [name] stays the plain one. */
    @SerialName("display_name") val displayName: String? = null,
) {
    /** What it is called in the list. */
    val title: String get() = displayName?.takeIf { it.isNotBlank() } ?: name.ifBlank { id }

    /** One of the hub's own (`plugin: "builtin"`), not an extension's. */
    val builtin: Boolean get() = extension == BUILTIN

    /** Claude Code needs a Messages endpoint; Responses and Chat Completions serve only the hub's direct-API calls. */
    val runsClaude: Boolean get() = kind != "api" || api.isNullOrEmpty() || api == "messages"

    /** The hub's key test supports Messages, Responses and Chat Completions (an empty protocol means Messages). */
    val testable: Boolean get() = api.isNullOrEmpty() || api == "messages" || api == "responses" || api == "chat"

    /** "Where to get a key": the signup page when it is a web address. */
    val signupLink: String? get() = signupUrl?.trim()?.takeIf { it.startsWith("https://") || it.startsWith("http://") }

    /** The model a launcher gets when none is picked: the declared default, else the first listed. */
    val defaultModel: String get() = model?.takeIf { it.isNotBlank() } ?: models.firstOrNull()?.id.orEmpty()

    /** What can be picked: the listed models, with the default first when it is not among them. */
    val choices: List<PresetModel> get() {
        val default = defaultModel
        return if (default.isEmpty() || models.any { it.id == default }) models else listOf(PresetModel(default)) + models
    }

    companion object {
        /** The hub's built-in presets' plugin. */
        const val BUILTIN = "builtin"
    }
}

/** One model of a gateway (proto.ProviderModel): what a preset lists, discovery finds, or a managed provider keeps. */
@Serializable
data class PresetModel(
    @SerialName("id") val id: String,
    @SerialName("name") val name: String? = null,
    /** Tokens; null or 0: unknown (200K assumed). */
    @SerialName("context_window") val contextWindow: Int? = null,
    @SerialName("efforts") val efforts: List<String>? = null,
    @SerialName("default_effort") val defaultEffort: String? = null,
) {
    val label: String get() = name?.takeIf { it.isNotBlank() } ?: id
}

/**
 * `POST /api/v1/launchers`' body. From a preset only [name], [apiKey], [preset] and maybe [model] are set: the hub fills
 * in the rest. Blank fields are left out.
 */
@Serializable
data class NewProvider(
    @SerialName("name") val name: String,
    @SerialName("api_key") val apiKey: String,
    @SerialName("preset") val preset: String? = null,
    /** launcher | api; null with a preset (the preset's). */
    @SerialName("kind") val kind: String? = null,
    /** messages | responses */
    @SerialName("api") val api: String? = null,
    @SerialName("base_url") val baseUrl: String? = null,
    @SerialName("model") val model: String? = null,
    @SerialName("small_model") val smallModel: String? = null,
    @SerialName("subagent_model") val subagentModel: String? = null,
    @SerialName("args") val args: List<String>? = null,
    /** What the apps call it (any language); blank with a preset: the preset's. */
    @SerialName("display_name") val displayName: String? = null,
    /** x-api-key (`api_key`) or `bearer`, typed in by hand. */
    @SerialName("auth") val auth: String? = null,
    /** The categories to put on it while they are still at their default ([AssignPurposes]); only for hubs with [BuiltinPresetsFeature]. */
    @SerialName("assign") val assign: List<String>? = null,
    // The model and gateway parameters typed in (docs/integrations/launchers.md "Provider model and gateway parameters"): only what was set.
    @SerialName("custom_model") val customModel: String? = null,
    @SerialName("custom_model_name") val customModelName: String? = null,
    @SerialName("custom_model_description") val customModelDescription: String? = null,
    @SerialName("models") val models: List<PresetModel>? = null,
    @SerialName("opus_model") val opusModel: String? = null,
    @SerialName("sonnet_model") val sonnetModel: String? = null,
    @SerialName("haiku_model") val haikuModel: String? = null,
    @SerialName("fable_model") val fableModel: String? = null,
    @SerialName("subagent_force") val subagentForce: Boolean? = null,
    @SerialName("headers") val headers: String? = null,
    @SerialName("env") val env: String? = null,
    @SerialName("proxy") val proxy: String? = null,
    @SerialName("no_proxy") val noProxy: String? = null,
    @SerialName("timeout_ms") val timeoutMs: Int? = null,
    @SerialName("compact_window") val compactWindow: Int? = null,
    @SerialName("max_context") val maxContext: Int? = null,
    @SerialName("effort") val effort: String? = null,
    // The device scope ([LauncherScopeFeature]): only what differs from the hub's defaults (all, on demand, skip; use with assign or a direct API).
    @SerialName("share") val share: String? = null,
    @SerialName("scope_machines") val scopeMachines: List<String>? = null,
    @SerialName("delivery") val delivery: String? = null,
    @SerialName("own_service") val ownService: String? = null,
    /** A key for single computers, by machine id. */
    @SerialName("machine_keys") val machineKeys: Map<String, String>? = null,
)

/**
 * `PUT /api/v1/launchers/{name}`'s body. The base fields have no default, so they always go: the hub replaces them. The
 * rest are null unless changed (left out keeps what is stored): [apiKey] only when a new one was typed, [headers] /
 * [env] "" to clear them, [machineKeys] "" to remove one computer's key.
 */
@Serializable
data class ProviderUpdate(
    @SerialName("name") val name: String,
    /** launcher | api */
    @SerialName("kind") val kind: String,
    /** messages | responses | chat */
    @SerialName("api") val api: String,
    @SerialName("base_url") val baseUrl: String,
    @SerialName("model") val model: String,
    @SerialName("small_model") val smallModel: String,
    @SerialName("subagent_model") val subagentModel: String,
    @SerialName("custom_model") val customModel: String,
    @SerialName("custom_model_name") val customModelName: String,
    @SerialName("custom_model_description") val customModelDescription: String,
    @SerialName("args") val args: List<String>,
    /** api_key | bearer; left out keeps what is stored. */
    @SerialName("auth") val auth: String? = null,
    /** codex | agy | omp; left out keeps what is stored (Claude Code is the default). */
    @SerialName("agent") val agent: String? = null,
    @SerialName("api_key") val apiKey: String? = null,
    @SerialName("models") val models: List<PresetModel>? = null,
    @SerialName("opus_model") val opusModel: String? = null,
    @SerialName("sonnet_model") val sonnetModel: String? = null,
    @SerialName("haiku_model") val haikuModel: String? = null,
    @SerialName("fable_model") val fableModel: String? = null,
    @SerialName("subagent_force") val subagentForce: Boolean? = null,
    @SerialName("headers") val headers: String? = null,
    @SerialName("env") val env: String? = null,
    @SerialName("proxy") val proxy: String? = null,
    @SerialName("no_proxy") val noProxy: String? = null,
    @SerialName("timeout_ms") val timeoutMs: Int? = null,
    @SerialName("compact_window") val compactWindow: Int? = null,
    @SerialName("max_context") val maxContext: Int? = null,
    @SerialName("effort") val effort: String? = null,
    @SerialName("share") val share: String? = null,
    @SerialName("scope_machines") val scopeMachines: List<String>? = null,
    @SerialName("delivery") val delivery: String? = null,
    @SerialName("own_service") val ownService: String? = null,
    @SerialName("machine_keys") val machineKeys: Map<String, String>? = null,
    /** "Send through" (kind api): a computer's id, "" the hub itself; left out keeps what is stored. */
    @SerialName("via_machine") val viaMachine: String? = null,
)

/** `POST /api/v1/launchers`' answer: [assigned] / [kept] only when `assign` was sent (an older hub leaves them out). */
data class ProviderAdded(
    val name: String,
    val displayName: String? = null,
    /** Null: the hub did not answer about assign (none was asked, or an older hub). */
    val assigned: List<String>? = null,
    val kept: List<KeptPurpose> = emptyList(),
) {
    val title: String get() = displayName?.takeIf { it.isNotBlank() } ?: name
}

/** A category left as it was: customized (someone picked another) | incompatible | failed. */
@Serializable
data class KeptPurpose(@SerialName("purpose") val purpose: String, @SerialName("reason") val reason: String = "")

/**
 * What "Test connection" / "Fetch model list" send: a [preset] with the key (the hub fills in its address and default model), or a
 * gateway typed in. Blank fields are left out.
 */
@Serializable
data class ProviderProbe(
    @SerialName("preset") val preset: String? = null,
    @SerialName("api_key") val apiKey: String? = null,
    @SerialName("base_url") val baseUrl: String? = null,
    /** api_key | bearer */
    @SerialName("auth") val auth: String? = null,
    @SerialName("model") val model: String? = null,
    /** A stored provider's name: the hub uses its key, headers and proxy for what is not typed in. */
    @SerialName("name") val name: String? = null,
    /** Headers typed in over the stored ones ("" sends none for this request). */
    @SerialName("headers") val headers: String? = null,
    /** messages | responses | chat */
    @SerialName("api") val api: String? = null,
    /** "Send through" as chosen: tested from where it will be sent; left out, the stored one. */
    @SerialName("via_machine") val viaMachine: String? = null,
)

@Serializable
data class ProviderTest(
    @SerialName("ok") val ok: Boolean = false,
    @SerialName("ms") val ms: Int = 0,
    @SerialName("model") val model: String? = null,
    @SerialName("error") val error: String? = null,
)

@Serializable
data class ProviderDiscovery(
    @SerialName("models") val models: List<PresetModel> = emptyList(),
    @SerialName("error") val error: String? = null,
)
