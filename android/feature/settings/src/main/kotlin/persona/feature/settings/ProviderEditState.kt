package persona.feature.settings

import persona.core.contracts.Machine
import persona.core.contracts.api.HubLauncher
import persona.core.contracts.api.NewProvider
import persona.core.contracts.api.PresetModel
import persona.core.contracts.api.ProviderUpdate

// Editing a Persona-managed provider with "New model provider"'s form (iOS ProviderWizardModel's existing provider, the web's
// lib/providers.ts formFromView / providerBody): the form an existing provider opens with, its write-only secrets (key,
// headers, environment: never shown, empty keeps, cleared only on purpose), the proxy's three choices, the checks the hub
// makes, and PUT's body with only what changed. Pure: NewProviderModel holds the state.

/** A provider's proxy as iOS Settings' Configure Proxy (#6238): the computer's, none for the gateway, or its own server. */
internal enum class ProxyMode { Follow, Direct, Manual }

/** Claude Code's thinking levels (proto.EffortLevels), in order. */
internal val EffortLevels = listOf("low", "medium", "high", "xhigh", "max")

/** The host of a gateway's address, lower case; "" without one. */
internal fun gatewayHost(baseUrl: String): String =
    runCatching { java.net.URI(baseUrl.trim()).host }.getOrNull()?.lowercase().orEmpty()

/** The choice a stored provider opens on: its own server is Manual, a bypass list of the gateway's host alone No Proxy, anything else Follow. */
internal fun proxyModeOf(proxy: String, noProxy: String, gateway: String): ProxyMode {
    if (proxy.isNotBlank()) return ProxyMode.Manual
    val hosts = noProxy.lowercase().split(',', ' ', '\t', '\n').filter { it.isNotBlank() }
    return if (gateway.isNotEmpty() && hosts == listOf(gateway)) ProxyMode.Direct else ProxyMode.Follow
}

/** What the choice stores as `proxy` and `no_proxy`. */
internal fun NewProviderDraft.proxyValues(): Pair<String, String> = when (proxyMode) {
    ProxyMode.Follow -> "" to noProxy.trim()
    ProxyMode.Direct -> "" to gatewayHost(baseUrl)
    ProxyMode.Manual -> proxy.trim() to noProxy.trim()
}

/** Manual without a server, or one the hub refuses (http, https, socks5 or socks5h, no user or password); null when it will do. */
internal fun NewProviderDraft.proxyProblem(): ResText? {
    if (proxyMode != ProxyMode.Manual) return null
    val server = proxy.trim()
    if (server.isEmpty()) return ResText.of(R.string.settings_pedit_proxy_needed)
    val uri = runCatching { java.net.URI(server) }.getOrNull()
    if (uri == null || uri.scheme?.lowercase() !in setOf("http", "https", "socks5", "socks5h") || (uri.host.isNullOrBlank() && uri.port < 0))
        return ResText.of(R.string.settings_pedit_proxy_scheme)
    if (uri.userInfo != null) return ResText.of(R.string.settings_pedit_proxy_credentials)
    return null
}

/** The hub's checkHeaders: one "Name: value" a line; the problem names the line, never its value. */
internal fun headersProblem(raw: String): ResText? {
    raw.split('\n').forEachIndexed { i, line ->
        val l = line.trim()
        if (l.isEmpty()) return@forEachIndexed
        val name = l.substringBefore(':', "").trim()
        if (':' !in l || name.isEmpty() || name.any { it.isWhitespace() }) return ResText.of(R.string.settings_pedit_header_line, i + 1)
    }
    return null
}

/** The hub's checkEnv: NAME=value a line ("export " first is fine, # a comment), at most 50. */
internal fun envProblem(raw: String): ResText? {
    var count = 0
    raw.split('\n').forEachIndexed { i, line ->
        val l = line.trim { it.isWhitespace() || it == '\uFEFF' }
        if (l.isEmpty() || l.startsWith("#")) return@forEachIndexed
        val rest = l.replaceFirst(Regex("^export\\s+"), "")
        val name = rest.substringBefore('=', "").trim()
        if ('=' !in rest || !Regex("^[A-Za-z_][A-Za-z0-9_]*$").matches(name)) return ResText.of(R.string.settings_pedit_env_line, i + 1)
        if (++count > 50) return ResText.of(R.string.settings_pedit_env_limit)
    }
    return null
}

private fun numberOk(s: String) = s.isBlank() || s.trim().all { it.isDigit() } && s.trim().length <= 9
private fun number(s: String): Int = s.trim().takeIf { it.isNotEmpty() }?.toIntOrNull() ?: 0
private fun numberText(n: Int) = if (n > 0) n.toString() else ""

/** A Chat Completions API: none of the launch settings, headers or proxy apply (docs/integrations/launchers.md "Direct-API providers"). */
internal val NewProviderDraft.chat: Boolean get() = kind == "api" && api == "chat"

/** The form's "Advanced" as the hub would check it; null when it can be saved. */
internal fun NewProviderDraft.advancedProblem(): ResText? {
    if (!chat) {
        if (headers.isNotBlank()) headersProblem(headers)?.let { return it }
        if (env.isNotBlank()) envProblem(env)?.let { return it }
        proxyProblem()?.let { return it }
        if (!(numberOk(timeoutMs) && numberOk(compactWindow) && numberOk(maxContext))) return ResText.of(R.string.settings_pedit_number)
    }
    if (kind != "api" && args.split(Regex("\\s+")).any { it.isNotEmpty() && !it.startsWith("-") }) return ResText.of(R.string.settings_pedit_args_dash)
    return null
}

/** The form an existing provider opens with: its models picked (the default among them even when not listed), secrets empty. */
internal fun providerDraftOf(l: HubLauncher): NewProviderDraft {
    val url = l.baseUrl.orEmpty()
    val models = l.models.toMutableList()
    val model = l.model.orEmpty()
    if (models.isNotEmpty() && model.isNotBlank() && models.none { it.id == model }) models.add(0, PresetModel(model))
    val noProxy = l.noProxy.orEmpty()
    val mode = proxyModeOf(l.proxy.orEmpty(), noProxy, gatewayHost(url))
    return NewProviderDraft(
        name = l.name, kind = if (l.isApi) "api" else "launcher", api = l.api?.takeIf { it.isNotBlank() } ?: "messages",
        baseUrl = url, auth = if (l.auth == "bearer") "bearer" else "api_key", model = model,
        smallModel = l.smallModel.orEmpty(), subagentModel = l.subagentModel.orEmpty(), args = l.args.joinToString(" "),
        agent = l.agent?.takeIf { it.isNotBlank() } ?: "claude", models = models,
        opusModel = l.opusModel.orEmpty(), sonnetModel = l.sonnetModel.orEmpty(), haikuModel = l.haikuModel.orEmpty(), fableModel = l.fableModel.orEmpty(),
        subagentForce = l.subagentForce, effort = l.effort.orEmpty(),
        timeoutMs = numberText(l.timeoutMs), compactWindow = numberText(l.compactWindow), maxContext = numberText(l.maxContext),
        proxyMode = mode, proxy = l.proxy.orEmpty(), noProxy = if (mode == ProxyMode.Direct) "" else noProxy,
        viaMachine = l.viaMachine.orEmpty(),
        customModel = l.customModel.orEmpty(), customModelName = l.customModelName.orEmpty(), customModelDescription = l.customModelDescription.orEmpty(),
    )
}

/** The models that go: the picked ones, with the default first when it is not among them (none picked: none). */
internal fun NewProviderDraft.modelsOut(): List<PresetModel> {
    val picked = models.filter { it.id.isNotBlank() }.map { it.copy(id = it.id.trim(), name = it.name?.trim()?.takeIf(String::isNotEmpty)) }
    val default = model.trim()
    return if (picked.isEmpty() || default.isEmpty() || picked.any { it.id == default }) picked else listOf(PresetModel(default)) + picked
}

/** #2455's fields as the hub takes them (its pointer ones: left out keeps what is stored), for comparing old and new. */
private data class Extras(
    val models: List<PresetModel>, val opus: String, val sonnet: String, val haiku: String, val fable: String, val subagentForce: Boolean,
    val proxy: String, val noProxy: String, val timeout: Int, val compact: Int, val maxContext: Int, val effort: String,
)

private fun NewProviderDraft.extras(): Extras {
    val (proxyValue, noProxyValue) = proxyValues()
    return Extras(modelsOut(), opusModel.trim(), sonnetModel.trim(), haikuModel.trim(), fableModel.trim(), subagentForce,
        proxyValue, noProxyValue, number(timeoutMs), number(compactWindow), number(maxContext), effort.trim())
}

/** Headers as they go: typed ones, "" to clear the stored ones, null to keep them. */
internal fun NewProviderDraft.headersValue(): String? = headers.trim().takeIf { it.isNotEmpty() } ?: "".takeIf { clearHeaders }

/** The environment as it goes; only its end is trimmed, so the hub's line numbers match the box. */
internal fun NewProviderDraft.envValue(): String? = env.takeIf { it.isNotBlank() }?.trimEnd() ?: "".takeIf { clearEnv }

/**
 * PUT /api/v1/launchers/{name}'s body for [d] over the stored [old]: the base fields always (the hub replaces them), the
 * others only when they changed, the key only when typed, headers and environment only when typed or cleared, and the
 * scope's [scope] fields.
 */
internal fun providerUpdate(d: NewProviderDraft, old: HubLauncher, scope: ScopeFields = ScopeFields()): ProviderUpdate {
    val api = d.kind == "api"
    val openAi = api && (d.api == "responses" || d.api == "chat")
    val now = d.extras()
    val before = providerDraftOf(old).extras()
    fun <T> changed(pick: (Extras) -> T): T? = pick(now).takeIf { !d.chat && it != pick(before) }
    return ProviderUpdate(
        name = old.name, kind = d.kind, api = if (api) d.api else "messages", baseUrl = d.baseUrl.trim(), model = d.model.trim(),
        smallModel = if (api) "" else d.smallModel.trim(), subagentModel = if (api) "" else d.subagentModel.trim(),
        customModel = if (api) "" else d.customModel.trim(), customModelName = if (api) "" else d.customModelName.trim(),
        customModelDescription = if (api) "" else d.customModelDescription.trim(),
        args = if (api) emptyList() else d.args.split(Regex("\\s+")).filter { it.isNotEmpty() },
        auth = if (openAi) "api_key" else d.auth,
        agent = d.agent.takeIf { !api && it.isNotBlank() && it != "claude" },
        apiKey = d.apiKey.trim().takeIf { it.isNotEmpty() },
        // The models are compared as a whole: a model's own window or thinking level changing is a change of the list.
        models = now.models.takeIf { it != before.models },
        opusModel = changed { it.opus }, sonnetModel = changed { it.sonnet }, haikuModel = changed { it.haiku }, fableModel = changed { it.fable },
        subagentForce = changed { it.subagentForce },
        headers = d.headersValue().takeIf { !d.chat }, env = d.envValue().takeIf { !d.chat },
        proxy = changed { it.proxy }, noProxy = changed { it.noProxy },
        timeoutMs = changed { it.timeout }, compactWindow = changed { it.compact }, maxContext = changed { it.maxContext },
        effort = changed { it.effort },
        share = scope.share, scopeMachines = scope.scopeMachines, delivery = scope.delivery, ownService = scope.ownService, machineKeys = scope.machineKeys,
        viaMachine = d.viaMachine.takeIf { api && it != old.viaMachine.orEmpty() },
    )
}

/** POST's body with what the form's "Advanced" and the scope add: only what was set, so a plain gateway sends what it always did. */
internal fun NewProvider.withExtras(d: NewProviderDraft, scope: ScopeFields): NewProvider {
    val api = d.kind == "api"
    val e = d.extras()
    fun String.set() = takeIf { it.isNotEmpty() }
    fun Int.set() = takeIf { it > 0 }
    val launch = !d.chat
    return copy(
        customModel = d.customModel.trim().set().takeIf { !api },
        customModelName = d.customModelName.trim().set().takeIf { !api },
        customModelDescription = d.customModelDescription.trim().set().takeIf { !api },
        models = e.models.takeIf { it.isNotEmpty() },
        opusModel = e.opus.set().takeIf { launch }, sonnetModel = e.sonnet.set().takeIf { launch },
        haikuModel = e.haiku.set().takeIf { launch }, fableModel = e.fable.set().takeIf { launch },
        subagentForce = true.takeIf { launch && e.subagentForce },
        headers = d.headers.trim().set().takeIf { launch }, env = d.env.takeIf { launch && it.isNotBlank() }?.trimEnd(),
        proxy = e.proxy.set().takeIf { launch }, noProxy = e.noProxy.set().takeIf { launch },
        timeoutMs = e.timeout.set().takeIf { launch }, compactWindow = e.compact.set().takeIf { launch }, maxContext = e.maxContext.set().takeIf { launch },
        effort = e.effort.set().takeIf { launch },
        share = scope.share, scopeMachines = scope.scopeMachines, delivery = scope.delivery, ownService = scope.ownService, machineKeys = scope.machineKeys,
    )
}

/** Picks [m] or lets it go; the first one picked fills an empty default model. */
internal fun NewProviderDraft.toggleModel(m: PresetModel): NewProviderDraft =
    if (models.any { it.id == m.id }) copy(models = models.filterNot { it.id == m.id })
    else copy(models = models + m, model = model.ifBlank { m.id })

/** "Add": a model the list lacks, by its id; unchanged when it is empty or already there. */
internal fun NewProviderDraft.addModel(id: String, found: List<PresetModel> = emptyList()): NewProviderDraft {
    val trimmed = id.trim()
    if (trimmed.isEmpty() || models.any { it.id == trimmed }) return this
    return toggleModel(found.firstOrNull { it.id == trimmed } ?: PresetModel(trimmed))
}

/** What the gateway says of a model already picked fills in what the picked one leaves out (iOS discover). */
internal fun NewProviderDraft.withFound(found: List<PresetModel>): NewProviderDraft = copy(models = models.map { m ->
    val d = found.firstOrNull { it.id == m.id } ?: return@map m
    m.copy(name = m.name ?: d.name, contextWindow = m.contextWindow?.takeIf { it > 0 } ?: d.contextWindow,
        efforts = m.efforts?.takeIf { it.isNotEmpty() } ?: d.efforts, defaultEffort = m.defaultEffort ?: d.defaultEffort)
})

/** The machine's name for a hub id (the merged list may carry a group's suffix). */
internal fun machineName(machines: List<Machine>): (String) -> String = { id -> machineLabel(machines, id) }
