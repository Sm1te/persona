package persona.feature.settings

import android.content.res.Resources
import persona.core.contracts.api.*
import persona.core.designsystem.UiText
import persona.core.designsystem.failureReasonText
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update

/** The purposes grouped by their hub group (empty: "Other"), in the hub's order, matching [query] by name, group, id or model. */
internal fun modelPurposeGroups(res: Resources, purposes: List<ModelPurpose>, providers: List<ModelProvider>, query: String): List<Pair<String, List<ModelPurpose>>> {
    val q = query.trim()
    return purposes.filter { purpose ->
        q.isEmpty() || (listOf(purpose.name, purpose.group, purpose.id) + summaryWords(res, purpose, providers)).any { it.contains(q, ignoreCase = true) }
    }.groupBy { it.group.ifBlank { res.getString(R.string.settings_src_model_group_other) } }.toList()
}

/** What is in effect, piece by piece (`ModelPurpose.summaryParts` in words of [res]): the provider's name, the model, the effort. */
internal fun summaryWords(res: Resources, purpose: ModelPurpose, providers: List<ModelProvider>): List<String> = listOfNotNull(
    purpose.effective.provider?.takeIf { it.isNotBlank() }?.let { id -> providers.firstOrNull { it.id == id }?.displayName ?: id },
    purpose.effective.model?.takeIf { it.isNotBlank() },
    purpose.effective.effort?.takeIf { it.isNotBlank() }?.let { res.getString(R.string.settings_src_model_effort_summary, it) },
)

/** A use of Agents the hub lets the user set apart (Lark /agent, the AI twin): it has fields of its own and follows
 *  Agents while they are empty. Other uses of a category have no fields and stay read-only. */
internal fun agentsOverride(purpose: ModelPurpose): Boolean =
    purpose.parent == "agent.new" && purpose.fields.orEmpty().any { it == "provider" || it == "model" || it == "effort" }

/** A model-only or effort-only selection is also an explicit override, even when its provider is inherited. */
internal fun agentsOverrideMode(res: Resources, purpose: ModelPurpose): String? =
    if (!agentsOverride(purpose)) null else res.getString(
        if (listOf(purpose.override.provider, purpose.override.model, purpose.override.effort).any { !it.isNullOrBlank() })
            R.string.settings_src_model_agents_separate else R.string.settings_src_model_agents_follow
    )

/** A purpose's row: what is in effect, then the hub's status or where it inherits from. */
internal fun purposeSubtitle(res: Resources, purpose: ModelPurpose, catalog: ModelCatalog): String = listOfNotNull(
    summaryWords(res, purpose, catalog.providers).joinToString(res.getString(R.string.settings_src_model_summary_separator))
        .ifEmpty { res.getString(R.string.settings_src_state_not_configured) },
    agentsOverrideMode(res, purpose),
    purpose.status?.takeIf { it.isNotBlank() } ?: purpose.inheritedFrom?.takeIf { it.isNotBlank() && !agentsOverride(purpose) }?.let { from ->
        res.getString(R.string.settings_src_model_follows, catalog.purposes.firstOrNull { it.id == from }?.displayName ?: from)
    },
).joinToString("\n")

internal data class ProviderOption(val id: String, val label: String)

/** "Use default", then the providers the purpose can use (hidden ones marked), and a reference the hub no longer offers. */
internal fun providerOptions(res: Resources, purpose: ModelPurpose, providers: List<ModelProvider>, chosen: String): List<ProviderOption> {
    val candidates = purpose.providersFrom(providers)
    return listOf(ProviderOption("", res.getString(R.string.settings_src_model_follow_default))) +
        candidates.map { ProviderOption(it.id, if (it.hidden) res.getString(R.string.settings_src_model_provider_hidden, it.displayName) else it.displayName) } +
        listOfNotNull(ProviderOption(chosen, res.getString(R.string.settings_src_model_provider_unavailable, chosen))
            .takeIf { chosen.isNotEmpty() && candidates.none { it.id == chosen } })
}

/** Effort is a field of launchers and of Responses APIs; a Messages API provider has none to set. */
internal fun showsEffort(purpose: ModelPurpose, provider: ModelProvider?): Boolean =
    purpose.supports("effort") && (agentsOverride(purpose) || !(provider?.kind == "api" && provider.api != "responses"))

internal data class ModelCatalogState(
    val catalog: ModelCatalog? = null,
    val loading: Boolean = false,
    val error: ResText? = null,
    val unsupported: Boolean = false,
)

/**
 * [purposes] with [own], a use the hub's catalog leaves out, after the last one of its group (the hub's order within the
 * group stands), or at the end when no listed use is in that group. Unchanged when the catalog lists it already.
 */
internal fun modelPurposesWith(purposes: List<ModelPurpose>, own: ModelPurpose): List<ModelPurpose> {
    if (purposes.any { it.id == own.id }) return purposes
    val last = purposes.indexOfLast { it.group == own.group }
    return if (last < 0) purposes + own else purposes.subList(0, last + 1) + own + purposes.subList(last + 1, purposes.size)
}

internal class ModelCatalogModel(private val api: ModelConfiguration) {
    private val mutable = MutableStateFlow(ModelCatalogState())
    val state = mutable.asStateFlow()

    /**
     * The background supervisor's use (#4927, [BackgroundSupervisor.PURPOSE]) is read by its own id: the hub's catalog
     * does not list it (docs/assistant/model-config.md). A hub without it, or one that cannot answer now, lists the
     * catalog as it is.
     */
    private suspend fun withBackgroundSupervisor(catalog: ModelCatalog): ModelCatalog {
        if (catalog.purposes.any { it.id == BackgroundSupervisor.PURPOSE }) return catalog
        val own = try { api.purpose(BackgroundSupervisor.PURPOSE).purpose }
            catch (cancelled: CancellationException) { throw cancelled }
            catch (_: Exception) { return catalog }
        return if (own.id == BackgroundSupervisor.PURPOSE) catalog.copy(purposes = modelPurposesWith(catalog.purposes, own)) else catalog
    }

    suspend fun load() {
        if (state.value.loading) return
        mutable.update { it.copy(loading = true) }
        try {
            val catalog = withBackgroundSupervisor(api.catalog())
            mutable.update { it.copy(catalog = catalog, error = null, unsupported = false) }
        } catch (cancelled: CancellationException) { throw cancelled }
        catch (_: ModelConfigurationUnsupported) { mutable.update { it.copy(unsupported = true, error = null) } }
        catch (error: Exception) { mutable.update { it.copy(error = ResText.of(R.string.settings_src_read_failed, failureReasonText(error))) } }
        finally { mutable.update { it.copy(loading = false) } }
    }
}

/** A launcher provider's /model list for "Model" (#943); [loaded] with no [list]: the hub has none for it. */
internal data class ProviderModelsState(
    val provider: String = "",
    val list: ProviderModels? = null,
    val loading: Boolean = false,
    val loaded: Boolean = false,
    val error: UiText? = null,
)

internal data class ModelPurposeState(
    val detail: ModelPurposeDetail? = null,
    val draft: ModelConfigurationDraft = ModelConfigurationDraft(),
    val busy: Boolean = false,
    val error: ResText? = null,
    /** Another device saved first: saving stays off until the page is reloaded (which drops the edit). */
    val conflict: Boolean = false,
    val models: ProviderModelsState = ProviderModelsState(),
) {
    val purpose: ModelPurpose? get() = detail?.purpose
    /** The provider the form talks about: the chosen one, else the one in effect. */
    val provider: ModelProvider? get() = detail?.providers?.firstOrNull { it.id == (draft.provider.ifEmpty { purpose?.effective?.provider }) }
    val changed: Boolean get() = purpose?.let { draft != ModelConfigurationDraft.from(it) } == true

    /** The launcher provider whose /model list "Model" picks from; "": an extension's, an API's, none. */
    val listProvider: String get() = provider?.takeIf { it.kind == "launcher" && purpose?.kind != "plugin" && purpose?.supports("model") == true }?.id.orEmpty()
    /** The list for [listProvider] once it is in (the state may still hold another provider's). */
    val modelList: ProviderModels? get() = models.list?.takeIf { models.provider == listProvider && listProvider.isNotEmpty() }
    /** "Model" is a pick from the list (or one coming); otherwise the plain field (an older hub, a failure, no list). */
    val picksModel: Boolean get() = listProvider.isNotEmpty() && models.provider == listProvider &&
        (modelList?.models?.isNotEmpty() == true || (models.loading && !models.loaded))

    /** What an empty "Model" runs: the model inherited while the provider is too, else the provider's default. */
    val defaultModelDetail: String? get() {
        val inherited = purpose?.effective?.model?.takeIf { draft.provider.isEmpty() && purpose?.override?.model.isNullOrBlank() && it.isNotBlank() }
        if (inherited != null) return inherited
        val default = modelList?.models?.firstOrNull { it.isDefault } ?: return null
        return default.resolved?.takeIf { it.isNotBlank() } ?: default.label
    }
}

/** One purpose's edit; a conflict never overwrites another device's change, and the draft survives it. */
internal class ModelPurposeModel(private val api: ModelConfiguration, private val id: String) {
    private val mutable = MutableStateFlow(ModelPurposeState())
    val state = mutable.asStateFlow()

    suspend fun load() {
        if (state.value.busy) return
        mutable.update { it.copy(busy = true, error = null) }
        try {
            val detail = api.purpose(id)
            mutable.update { it.copy(detail = detail, draft = ModelConfigurationDraft.from(detail.purpose), conflict = false) }
        } catch (cancelled: CancellationException) { throw cancelled }
        catch (error: Exception) { mutable.update { it.copy(error = ResText.of(R.string.settings_src_read_failed, failureReasonText(error))) } }
        finally { mutable.update { it.copy(busy = false) } }
    }

    fun edit(draft: ModelConfigurationDraft) = mutable.update { if (it.busy) it else it.copy(draft = draft) }

    /**
     * A provider was just added ("New model provider" from the provider sheet): the providers are read again, the edit kept, and
     * the new one picked when this use can take it.
     */
    suspend fun providerAdded(providerId: String) {
        if (state.value.busy) return
        mutable.update { it.copy(busy = true, error = null) }
        try {
            val detail = api.purpose(id)
            mutable.update { s ->
                val usable = detail.purpose.providersFrom(detail.providers).any { it.id == providerId }
                s.copy(detail = detail, draft = if (usable) s.draft.choosingProvider(providerId) else s.draft)
            }
        } catch (cancelled: CancellationException) { throw cancelled }
        catch (error: Exception) { mutable.update { it.copy(error = ResText.of(R.string.settings_src_read_failed, failureReasonText(error))) } }
        finally { mutable.update { it.copy(busy = false) } }
    }

    /** The /model list of [ModelPurposeState.listProvider] (again with [refresh]); a provider without one clears it. */
    suspend fun loadModels(refresh: Boolean = false) {
        val provider = state.value.listProvider
        val current = state.value.models
        if (provider.isEmpty()) { mutable.update { it.copy(models = ProviderModelsState()) }; return }
        if (!refresh && current.provider == provider && (current.loaded || current.loading)) return
        mutable.update { it.copy(models = ProviderModelsState(provider, list = current.list.takeIf { refresh && current.provider == provider }, loading = true, loaded = refresh && current.loaded)) }
        try {
            val list = api.providerModels(provider, refresh, state.value.purpose?.machineId)
            mutable.update { if (it.models.provider != provider) it else it.copy(models = it.models.copy(list = list, loading = false, loaded = true, error = null)) }
        } catch (cancelled: CancellationException) { throw cancelled }
        catch (error: Exception) {
            mutable.update { if (it.models.provider != provider) it else it.copy(models = it.models.copy(loading = false, error = failureReasonText(error))) }
        }
    }

    /** The notice to show when the hub took it ([restore]: every override of this purpose cleared); null otherwise. */
    suspend fun save(restore: Boolean = false): ResText? {
        val current = state.value
        val purpose = current.purpose ?: return null
        if (current.busy || current.conflict) return null
        mutable.update { it.copy(busy = true, error = null) }
        return try {
            when (val result = api.save(purpose, if (restore) ModelConfigurationDraft.Restore else current.draft)) {
                is ModelPurposeSave.Saved -> {
                    mutable.update { it.copy(detail = result.detail, draft = ModelConfigurationDraft.from(result.detail.purpose)) }
                    ResText.of(if (restore) R.string.settings_src_model_restored else R.string.settings_src_model_saved)
                }
                ModelPurposeSave.Conflict -> {
                    mutable.update { it.copy(conflict = true, error = ResText.of(R.string.settings_src_model_conflict)) }
                    null
                }
            }
        } catch (cancelled: CancellationException) { throw cancelled }
        catch (error: Exception) { mutable.update { it.copy(error = ResText.of(R.string.settings_src_not_saved, failureReasonText(error))) }; null }
        finally { mutable.update { it.copy(busy = false) } }
    }
}
