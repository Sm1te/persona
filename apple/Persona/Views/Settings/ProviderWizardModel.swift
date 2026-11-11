import Foundation
import Observation

/// A managed launcher's name as the hub takes it.
enum LauncherName {
    /// What is wrong with `name`, the way the hub would put it; nil while empty or fine.
    static func problem(_ name: String) -> String? {
        let n = name.trimmingCharacters(in: .whitespaces)
        guard !n.isEmpty else { return nil }
        if n.range(of: "^[A-Za-z_][A-Za-z0-9_-]*$", options: .regularExpression) == nil {
            return String(localized: "Names can only use letters, digits, _, and -, and must start with a letter or _")
        }
        // The hub's reservedLauncherNames: claude itself and the /api/v1/launchers/<word> routes.
        if ["claude", "presets", "test", "discover", "import"].contains(n) { return String(localized: "Can't be named \(n)") }
        return nil
    }
}

/// Settings › Model Providers › Add / Edit (#2455, after cindy's AddProviderWizard): Choose Provider → Connect (name, address, auth, key;
/// Test Connection, Get Model List) → Choose Models (context window, thinking levels, the default), with the rest under Advanced.
///
/// A hub with `builtin_presets` (docs/assistant/onboarding.md 4.3「三条路」, 5.4, #4197) starts from the providers it offers
/// instead: pick one → paste the key (tested by itself) → what now runs on it (`assign`, `assigned` / `kept`); a
/// compatible gateway typed by hand is the last of the list and goes the three steps above. Beside it, "I already use
/// Claude Code" tries the main conversation once (readiness `?probe=1`) and "Where to get a model" lists where to get a
/// key. An older hub gets the wizard as it was, Anthropic official included.
@Observable final class ProviderWizardModel {
    struct Deps {
        var api: () -> APIClient?
        /// The hub's `features` (hello / health).
        var features: () -> Set<String> = { [] }
        /// The main conversation's readiness as last read (nil: an older hub, or not read yet).
        var readiness: () -> AssistantReadiness? = { nil }
        /// A readiness read here (the one-message try) for the rest of the app.
        var landReadiness: (AssistantReadiness) -> Void = { _ in }
        /// The hub's computers (the scope's checklist and its per-computer keys).
        var machines: () -> [Machine] = { [] }
        @MainActor static var live: Deps {
            .init(api: { AppModel.shared.api }, features: { AppModel.shared.hubFeatures },
                  readiness: { AppModel.shared.readiness.current }, landReadiness: { AppModel.shared.readiness.land($0) },
                  machines: { AppModel.shared.hubMachines })
        }
    }

    /// The main conversation already runs on a Claude Code that answers (docs/assistant/onboarding.md 4.3 B.3): its computer,
    /// Claude Code and model all check out (a launcher that runs Codex or a runtime skips the Claude Code step, so it
    /// is not this). The "use it for the main conversation and new agents" toggle then starts off, so a working default is not swapped out quietly.
    static func claudeCodeWorks(_ r: AssistantReadiness?) -> Bool {
        guard let r, r.claudeWorks else { return false }
        return r.step("claude")?.state == "ok"
    }

    /// The hub's feature for built-in presets, a name it makes up, `display_name` and `assign` (docs/integrations/launchers.md「Built-in presets」).
    static let presetsFeature = "builtin_presets"
    /// The uses a provider added in the wizard takes over while they are still at their default (docs/assistant/onboarding.md 5.4).
    static let assignPurposes = ["assistant", "agent.new"]

    enum Kind: String, CaseIterable, Hashable {
        /// api.anthropic.com with an x-api-key.
        case anthropic
        /// Any Anthropic-compatible gateway (a Coding Plan, a company's proxy).
        case custom
    }

    static let anthropicURL = "https://api.anthropic.com"

    /// Configure Proxy's three choices (#6238).
    enum ProxyMode: String, CaseIterable, Identifiable {
        /// No proxy of its own: the computer's (`personad launch` skips one that no longer answers, #5953).
        case follow
        /// The gateway goes direct, everything else the computer's way: the gateway's host as `no_proxy`. Not "*",
        /// which would send the web pages and commands of the agent direct too.
        case direct
        /// Its own server.
        case manual
        var id: String { rawValue }

        var title: String {
            switch self {
            case .follow: String(localized: "Follow This Computer")
            case .direct: String(localized: "No Proxy")
            case .manual: String(localized: "Manual")
            }
        }
    }

    let existing: LauncherView?
    var kind: Kind = .custom
    var name = ""
    var baseURL = ""
    /// api_key (x-api-key) | bearer.
    var auth = "api_key"
    var apiKey = ""
    /// Typed headers, "Name: value" a line. Empty keeps what the hub has unless `clearHeaders`.
    var headers = ""
    /// Clear Headers: the stored headers go when saved.
    var clearHeaders = false
    /// Typed environment, NAME=value a line (#5953); empty keeps what the hub has unless `clearEnv`.
    var env = ""
    var clearEnv = false

    /// The models picked, in order; the default is `defaultModel`.
    private(set) var models: [ProviderModel] = []
    var defaultModel = ""
    /// What the gateway said it serves (Get Model List).
    private(set) var discovered: [ProviderModel] = []

    // Advanced
    var opusModel = ""
    var sonnetModel = ""
    var haikuModel = ""
    var fableModel = ""
    var smallModel = ""
    var subagentModel = ""
    var subagentForce = false
    var effort = ""
    /// How the provider reaches its gateway (#6238), as iOS Settings' Configure Proxy: its own proxy or none. `proxy`
    /// is Manual's server, `noProxy` the hosts that go direct (Follow and Manual); No Proxy is the gateway's host.
    var proxyMode: ProxyMode = .follow
    var proxy = ""
    var noProxy = ""
    /// Numbers as typed; empty or 0 is the default.
    var timeoutMS = ""
    var compactWindow = ""
    var maxContext = ""
    /// Send Through (an existing direct API connection, #4775): the computer whose node sends its requests, "" the hub.
    var viaMachine = ""
    /// Where it runs and when its key travels (#2333, `ProviderComputersPage`): a stored provider's as it opened, else
    /// the hub's defaults. Only fields that differ from what it opened with are sent (`ProviderScopeForm.fields`).
    var scope = ProviderScopeForm()

    private(set) var testing = false
    private(set) var test: LauncherProbeResult?
    private(set) var discovering = false
    /// Get Model List ran (even when it found none or failed): the models step shows what came of it.
    private(set) var discoveredOnce = false
    private(set) var discoverError: String?
    private(set) var saving = false
    private(set) var error: String?
    @ObservationIgnored private let deps: Deps

    // MARK: Presets (a hub with builtin_presets)

    /// The hub's providers (built-in, then the extensions'), in its order.
    private(set) var presets: [LauncherPreset] = []
    private(set) var presetsLoaded = false
    private(set) var presetsError: String?
    /// The picked preset; picking another starts its key, name and model over.
    var presetID: String? {
        didSet {
            guard presetID != oldValue else { return }
            presetKey = ""
            presetName = ""
            presetDisplayName = ""
            presetModel = preset?.defaultModel ?? ""
            keyTest = nil
            keyTask?.cancel()
        }
    }
    var presetKey = ""
    /// Empty: the hub names it after the provider.
    var presetName = ""
    /// Empty: the hub uses the preset's (「智谱 GLM」); any language.
    var presetDisplayName = ""
    /// One of the preset's models (its default when none was picked).
    var presetModel = ""
    private(set) var keyTesting = false
    private(set) var keyTest: LauncherProbeResult?
    @ObservationIgnored private var keyTask: Task<Void, Never>?
    /// nil: not touched, so it follows the readiness.
    var assignChoice: Bool?
    /// What saving a preset came to; set, the wizard shows its last screen.
    private(set) var added: LauncherAdded?
    /// Whether `assign` went with the save.
    private(set) var addedAsked = false

    /// "I already use Claude Code": the main conversation's one-word try.
    private(set) var claudeTrying = false
    private(set) var claudeTry: ClaudeTry?

    /// The new flow: the hub offers built-in presets and takes `assign` (an older hub keeps the wizard as it was).
    var presetsFlow: Bool { isNew && deps.features().contains(Self.presetsFeature) }
    var preset: LauncherPreset? { presets.first { $0.id == presetID } }
    /// "Where to get a model": the built-in providers that say where to sign up.
    var signupPresets: [LauncherPreset] { presets.filter { $0.isBuiltin && $0.runsClaude && $0.signup != nil } }
    var presetNameProblem: String? { LauncherName.problem(presetName) }
    private var trimmedKey: String { presetKey.trimmingCharacters(in: .whitespacesAndNewlines) }
    /// A key long enough to be worth testing on its own: a test costs a request, so not on every keystroke.
    var keyTestable: Bool { (preset?.testable ?? false) && trimmedKey.count >= 8 }
    /// The key test says the provider does not serve this region (#4482): no key will work here, only another
    /// provider, so the wizard offers that instead of testing again (#4526).
    var regionBlocked: Bool { keyTest.map { !$0.ok && $0.code == "region_blocked" } ?? false }
    /// The assign toggle: on unless the main conversation already runs on a Claude Code that answers, or the key test
    /// says the region is blocked (#4526); the user can still turn it on.
    var assign: Bool { assignChoice ?? (!Self.claudeCodeWorks(deps.readiness()) && !regionBlocked) }
    var canSavePreset: Bool {
        preset != nil && !trimmedKey.isEmpty && presetNameProblem == nil && scopeProblem == nil && !keyTesting && !saving
    }

    init(existing: LauncherView? = nil, deps: Deps = .live) {
        self.existing = existing
        self.deps = deps
        guard let e = existing else { return }
        name = e.name
        viaMachine = e.viaMachine ?? ""
        let url = e.baseURL ?? ""
        kind = url.trimmingCharacters(in: CharacterSet(charactersIn: "/ ")) == Self.anthropicURL ? .anthropic : .custom
        baseURL = kind == .custom ? url : ""
        auth = e.bearerAuth ? "bearer" : "api_key"
        models = e.models ?? []
        defaultModel = e.model ?? ""
        if !defaultModel.isEmpty, !models.contains(where: { $0.id == defaultModel }) { models.insert(ProviderModel(id: defaultModel), at: 0) }
        opusModel = e.opusModel ?? ""
        sonnetModel = e.sonnetModel ?? ""
        haikuModel = e.haikuModel ?? ""
        fableModel = e.fableModel ?? ""
        smallModel = e.smallModel ?? ""
        subagentModel = e.subagentModel ?? ""
        subagentForce = e.subagentForce ?? false
        effort = e.effort ?? ""
        proxy = e.proxy ?? ""
        noProxy = e.noProxy ?? ""
        proxyMode = Self.proxyMode(proxy: proxy, noProxy: noProxy, gateway: gatewayHost)
        if proxyMode == .direct { noProxy = "" }
        timeoutMS = Self.number(e.timeoutMS)
        compactWindow = Self.number(e.compactWindow)
        maxContext = Self.number(e.maxContext)
        if let s = ProviderScopeForm(view: e) { scope = s }
    }

    /// The Computers row is offered: a stored provider whose hub returned its scope, a new one when the hub takes scopes
    /// (`launcher_scope`). An older hub sees the wizard as it was, and nothing of the scope is sent.
    var scopeShown: Bool {
        if let e = existing { return e.share != nil }
        return deps.features().contains(ProviderScopeForm.feature)
    }

    /// Selected Computers with none ticked: the hub would refuse it, so Save waits.
    var scopeProblem: String? { scopeShown ? scope.problem : nil }

    var isNew: Bool { existing == nil }
    var hasStoredKey: Bool { existing?.hasKey == true }
    var hasStoredHeaders: Bool { existing?.hasHeaders == true && !clearHeaders }
    var hasStoredEnv: Bool { existing?.hasEnv == true && !clearEnv }

    var address: String { kind == .anthropic ? Self.anthropicURL : baseURL.trimmingCharacters(in: .whitespaces) }
    var authMode: String { kind == .anthropic ? "api_key" : auth }
    private var key: String { apiKey.trimmingCharacters(in: .whitespacesAndNewlines) }
    private var trimmedName: String { name.trimmingCharacters(in: .whitespaces) }
    var nameProblem: String? { LauncherName.problem(name) }

    /// nil keeps the stored headers, "" clears them.
    var headersValue: String? {
        let h = headers.trimmingCharacters(in: .whitespacesAndNewlines)
        if !h.isEmpty { return h }
        return clearHeaders ? "" : nil
    }

    /// nil keeps the stored environment, "" clears it. Only its end is trimmed: the hub's line numbers then match the box.
    var envValue: String? {
        if !env.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            return env.replacingOccurrences(of: #"\s+$"#, with: "", options: .regularExpression)
        }
        return clearEnv ? "" : nil
    }

    /// Connect is filled in: the next step can come.
    var connectionReady: Bool {
        !trimmedName.isEmpty && nameProblem == nil && URL(string: address)?.host() != nil && (!key.isEmpty || hasStoredKey)
    }

    /// The model Test Connection asks for: the default, else the first picked or listed.
    var testModel: String { [defaultModel, models.first?.id ?? "", discovered.first?.id ?? ""].first { !$0.isEmpty } ?? "" }

    var canTest: Bool { connectionReady && !testModel.isEmpty && !testing }

    var canSave: Bool { connectionReady && models.contains { $0.id == defaultModel } && proxyProblem == nil && scopeProblem == nil && !saving }

    /// What the models step lists: the picked ones, then the rest of what the gateway serves.
    var choices: [ProviderModel] {
        models + discovered.filter { d in !models.contains { $0.id == d.id } }
    }

    func isPicked(_ id: String) -> Bool { models.contains { $0.id == id } }

    /// Ticks or unticks a model; the first one ticked becomes the default, and unticking the default moves it on.
    func toggle(_ m: ProviderModel) {
        if let i = models.firstIndex(where: { $0.id == m.id }) {
            models.remove(at: i)
            if defaultModel == m.id { defaultModel = models.first?.id ?? "" }
        } else {
            models.append(m)
            if defaultModel.isEmpty { defaultModel = m.id }
        }
    }

    /// Add: a model the list does not have, by its id; false when it is empty or already there.
    @discardableResult func addModel(_ id: String) -> Bool {
        let id = id.trimmingCharacters(in: .whitespaces)
        guard !id.isEmpty else { return false }
        if isPicked(id) { return false }
        toggle(discovered.first { $0.id == id } ?? ProviderModel(id: id))
        return true
    }

    func update(_ id: String, _ change: (inout ProviderModel) -> Void) {
        guard let i = models.firstIndex(where: { $0.id == id }) else { return }
        change(&models[i])
    }

    func model(_ id: String) -> ProviderModel? { models.first { $0.id == id } ?? discovered.first { $0.id == id } }

    /// A direct API connection: what the hub calls itself, so it can send through a computer.
    var isAPI: Bool { existing?.kind == "api" }
    /// Send Through's choices: the hub's computers, and the stored one even when the hub no longer lists it.
    var viaChoices: [(id: String, name: String)] {
        let list = deps.machines().map { (id: $0.id, name: $0.name) }
        return viaMachine.isEmpty || list.contains(where: { $0.id == viaMachine }) ? list : list + [(id: viaMachine, name: viaMachine)]
    }

    private var probe: LauncherProbe {
        LauncherProbe(name: existing?.name, baseURL: address, auth: authMode, apiKey: key.isEmpty ? nil : key,
                      headers: headersValue, model: testModel.isEmpty ? nil : testModel, viaMachine: isAPI ? viaMachine : nil)
    }

    func runTest() async {
        guard canTest, let api = deps.api() else { return }
        testing = true
        defer { testing = false }
        do {
            test = try await api.testLauncher(probe)
        } catch {
            test = LauncherProbeResult(ok: false, error: error.shown ?? error.localizedDescription)
        }
    }

    func discover() async {
        guard connectionReady, !discovering, let api = deps.api() else { return }
        discovering = true
        defer { discovering = false; discoveredOnce = true }
        do {
            let r = try await api.discoverLauncherModels(probe)
            discovered = r.models ?? []
            discoverError = r.error.flatMap { $0.isEmpty ? nil : $0 }
            // What the gateway says of a model already picked fills in what the picked one leaves out.
            for d in discovered {
                update(d.id) { m in
                    if m.name == nil { m.name = d.name }
                    if (m.contextWindow ?? 0) == 0 { m.contextWindow = d.contextWindow }
                    if (m.efforts ?? []).isEmpty { m.efforts = d.efforts }
                    if m.defaultEffort == nil { m.defaultEffort = d.defaultEffort }
                }
            }
        } catch {
            discoverError = error.shown ?? error.localizedDescription
        }
    }

    /// The choice a stored provider opens on: its own server is Manual, a bypass list of the gateway's host alone No
    /// Proxy (what Follow with that list does too), anything else Follow, "*" included.
    static func proxyMode(proxy: String, noProxy: String, gateway: String) -> ProxyMode {
        if !proxy.trimmingCharacters(in: .whitespaces).isEmpty { return .manual }
        let hosts = noProxy.lowercased().split { $0 == "," || $0.isWhitespace }
        return !gateway.isEmpty && hosts == [Substring(gateway)] ? .direct : .follow
    }

    /// The host of the gateway's address, lower case; "" without one.
    var gatewayHost: String { URL(string: address)?.host()?.lowercased() ?? "" }

    /// What the choice stores as `proxy` and `no_proxy`.
    var proxyValues: (proxy: String, noProxy: String) {
        let t = { (s: String) in s.trimmingCharacters(in: .whitespaces) }
        switch proxyMode {
        case .follow: return ("", t(noProxy))
        case .direct: return ("", gatewayHost)
        case .manual: return (t(proxy), t(noProxy))
        }
    }

    /// Manual without a server, or one the hub refuses (`checkLauncher`: http, https, socks5 or socks5h, and no user or
    /// password, as the proxy goes back to every client as is): nil when it will do.
    var proxyProblem: String? {
        guard proxyMode == .manual else { return nil }
        let server = proxy.trimmingCharacters(in: .whitespaces)
        if server.isEmpty { return String(localized: "Enter the proxy server, or choose Follow This Computer.") }
        guard let u = URL(string: server), ["http", "https", "socks5", "socks5h"].contains(u.scheme?.lowercased() ?? ""),
              !(u.host() ?? "").isEmpty || u.port != nil else { // the hub takes http://:8080 (this computer) too
            return String(localized: "The proxy starts with http://, https:// or socks5://, like http://127.0.0.1:7890.")
        }
        if u.user() != nil || u.password() != nil { return String(localized: "The proxy can't include a user name or password.") }
        return nil
    }

    /// The Proxy row's value: the choice, Manual's server itself.
    var proxySummary: String {
        switch proxyMode {
        case .follow: String(localized: "Follow This Computer")
        case .direct: String(localized: "No Proxy")
        case .manual: proxy.trimmingCharacters(in: .whitespaces).isEmpty ? String(localized: "Manual") : proxy.trimmingCharacters(in: .whitespaces)
        }
    }

    var input: LauncherInput {
        let e = existing
        let t = { (s: String) in s.trimmingCharacters(in: .whitespaces) }
        let (proxyValue, noProxyValue) = proxyValues
        var input = LauncherInput(name: trimmedName, baseURL: address, model: defaultModel, smallModel: t(smallModel), subagentModel: t(subagentModel),
                             customModel: e?.customModel ?? "", customModelName: e?.customModelName ?? "",
                             customModelDescription: e?.customModelDescription ?? "", args: e?.args ?? [],
                             apiKey: key.isEmpty ? nil : key, kind: e?.kind ?? "launcher", api: "messages", auth: authMode,
                             models: models, opusModel: t(opusModel), sonnetModel: t(sonnetModel), haikuModel: t(haikuModel),
                             fableModel: t(fableModel), subagentForce: subagentForce, headers: headersValue, env: envValue, proxy: proxyValue,
                             noProxy: noProxyValue, timeoutMS: Int(t(timeoutMS)) ?? 0, compactWindow: Int(t(compactWindow)) ?? 0,
                             maxContext: Int(t(maxContext)) ?? 0, effort: t(effort))
        if scopeShown { scope.apply(to: &input, old: e, machines: deps.machines(), kind: input.kind ?? "launcher") }
        if isAPI, viaMachine != (e?.viaMachine ?? "") { input.viaMachine = viaMachine }
        return input
    }

    /// Saves the provider; false (with `error`) when the hub refused it.
    func save() async -> Bool {
        guard canSave, let api = deps.api() else { return false }
        saving = true
        defer { saving = false }
        do {
            try await api.saveLauncher(input, isNew: isNew)
            error = nil
            return true
        } catch {
            self.error = error.shown ?? error.localizedDescription
            return false
        }
    }

    private static func number(_ n: Int?) -> String { n.map { $0 > 0 ? String($0) : "" } ?? "" }

    // MARK: Presets

    func loadPresets() async {
        guard presetsFlow, let api = deps.api() else { presetsLoaded = true; return }
        presetsLoaded = false
        do {
            presets = try await api.launcherPresets()
            presetsError = nil
        } catch {
            presetsError = error.shown ?? error.localizedDescription
        }
        presetsLoaded = true
    }

    /// What the key test sends for a preset: the key, how the gateway takes it and the preset; the model only when another than
    /// the default was picked (the hub fills in the address and its default model).
    var keyProbe: LauncherProbe? {
        guard let p = preset else { return nil }
        let m = presetModel.trimmingCharacters(in: .whitespaces)
        return LauncherProbe(name: nil, baseURL: nil, auth: p.auth ?? "api_key", apiKey: trimmedKey, headers: nil,
                             model: m.isEmpty || m == p.defaultModel ? nil : m, preset: p.id)
    }

    /// The key or the model changed: the key is tested once it stays the same a moment.
    func keyChanged(after delay: Duration = .milliseconds(700)) {
        keyTask?.cancel()
        keyTest = nil
        guard keyTestable else { return }
        keyTask = Task { [weak self] in
            try? await Task.sleep(for: delay)
            guard !Task.isCancelled else { return }
            await self?.testKey()
        }
    }

    /// Tests the preset's key: one tiny request through the gateway. The hub's words when it fails.
    func testKey() async {
        guard keyTestable, let probe = keyProbe, let api = deps.api() else { return }
        keyTesting = true
        defer { keyTesting = false }
        let result: LauncherProbeResult
        do {
            result = try await api.testLauncher(probe)
        } catch {
            result = LauncherProbeResult(ok: false, error: error.shown ?? error.localizedDescription)
        }
        // A key typed over while it ran is tested again; this answer is not about it.
        if probe == keyProbe { keyTest = result }
    }

    /// What Save sends for a preset: the key and the preset, the name and the display name as typed (empty: the hub
    /// makes them up), the model only when another than the default was picked, `assign` when on.
    var presetInput: LauncherInput? {
        guard let p = preset else { return nil }
        let m = presetModel.trimmingCharacters(in: .whitespaces)
        let assigning = p.runsClaude && assign
        var input = LauncherInput(name: presetName.trimmingCharacters(in: .whitespaces), baseURL: "", model: m == p.defaultModel ? "" : m,
                                  smallModel: "", subagentModel: "", customModel: "", customModelName: "", customModelDescription: "",
                                  args: [], apiKey: trimmedKey, preset: p.id,
                                  displayName: presetDisplayName.trimmingCharacters(in: .whitespaces),
                                  assign: assigning ? Self.assignPurposes : nil)
        if scopeShown { scope.apply(to: &input, old: nil, machines: deps.machines(), assign: assigning, kind: p.kind) }
        return input
    }

    /// Adds the preset's provider; false (with `error`) when the hub refused it.
    func savePreset() async -> Bool {
        guard canSavePreset, let input = presetInput, let api = deps.api() else { return false }
        saving = true
        defer { saving = false }
        do {
            added = try await api.addLauncher(input)
            addedAsked = input.assign != nil
            error = nil
            return true
        } catch {
            self.error = error.shown ?? error.localizedDescription
            return false
        }
    }

    /// The provider's name on the last screen: what the hub called it, else what was typed or the preset's.
    var addedTitle: String {
        if let l = added?.launcher { return l.title }
        let typed = presetDisplayName.trimmingCharacters(in: .whitespaces)
        if !typed.isEmpty { return typed }
        let n = presetName.trimmingCharacters(in: .whitespaces)
        return n.isEmpty ? preset?.title ?? "" : n
    }

    var addedSummary: AssignSummary { AssignSummary(added ?? LauncherAdded(), asked: addedAsked) }

    // MARK: I already use Claude Code

    /// Tries the main conversation once (GET /api/v1/assistant/readiness?probe=1: it costs a request); the answer is
    /// the app's readiness from now on too.
    func tryClaude() async {
        guard !claudeTrying, let api = deps.api() else { return }
        claudeTrying = true
        defer { claudeTrying = false }
        do {
            let r = try await api.assistantReadiness(probe: true)
            deps.landReadiness(r)
            claudeTry = .answered(r)
        } catch let e as APIError where e.status == 404 {
            claudeTry = .unsupported
        } catch {
            claudeTry = .failed(error.shown ?? error.localizedDescription)
        }
    }
}

/// "I already use Claude Code"'s answer: the readiness with its probe, its computer and the step in the way.
enum ClaudeTry: Equatable {
    case answered(AssistantReadiness)
    /// An older hub without the readiness (404).
    case unsupported
    /// The request itself failed (the hub's words).
    case failed(String)

    var ok: Bool { if case .answered(let r) = self { r.probe?.ok == true } else { false } }
}

/// The last screen's words: which uses now run on the new provider, and which were kept and why.
struct AssignSummary: Equatable {
    var assigned = ""
    var kept: [String] = []

    init(_ r: LauncherAdded, asked: Bool) {
        // An older hub (or no assign) says nothing of it: the screen sends the user to Model Settings instead.
        guard asked, let a = r.assigned else { return }
        if Set(a) == Set(ProviderWizardModel.assignPurposes) {
            assigned = String(localized: "The main conversation and new agents now use it.")
        } else if !a.isEmpty {
            assigned = String(localized: "\(ListFormatter.localizedString(byJoining: a.map(Self.purpose))) now uses it.")
        }
        kept = (r.kept ?? []).map { Self.kept($0) }
    }

    static func purpose(_ id: String) -> String {
        switch id {
        case "assistant": String(localized: "Main Chat")
        case "agent.new": String(localized: "New agents")
        default: id
        }
    }

    static func kept(_ k: LauncherAdded.Kept) -> String {
        let p = purpose(k.purpose)
        return switch k.reason {
        case "customized": String(localized: "\(p) unchanged: you had changed it, so your choice stays.")
        case "incompatible": String(localized: "\(p) unchanged: it can't use this provider.")
        case "failed": String(localized: "\(p) wasn't switched: you can pick it yourself in Model Settings.")
        default: String(localized: "\(p) unchanged.")
        }
    }
}
