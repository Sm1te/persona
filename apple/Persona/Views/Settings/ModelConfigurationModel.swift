import Foundation
import Observation

/// One source of model choices: the hub's purposes, bindings and effective values.
@Observable final class ModelConfigurationModel {
    struct Deps {
        var api: () -> APIClient?
        var updated: () async -> Void = {}

        static var live: Deps {
            let app = AppModel.shared
            // A saved use moves the pickers' 「默认：…」 too: they read the machines' launchers again.
            return .init(api: { app.api }, updated: { app.launcherDefaultsChanged(); await app.refreshAssistant() })
        }
    }

    private(set) var purposes: [ModelPurpose] = []
    private(set) var providers: [ModelProvider] = []
    private(set) var purpose: ModelPurpose?
    private(set) var loaded = false
    private(set) var loading = false
    private(set) var saving = false
    private(set) var error: String?
    private(set) var conflict = false
    var draft = ModelBinding()
    @ObservationIgnored private let deps: Deps
    @ObservationIgnored private var loadedPurposeID: String?

    init(deps: Deps = .live) { self.deps = deps }

    var dirty: Bool { purpose.map { draft != $0.override } ?? false }

    var categories: [ModelPurpose] { purposes.filter { $0.uses != nil } }
    var independentPurposes: [ModelPurpose] {
        purposes.filter { $0.uses == nil && $0.categoryID != nil && $0.categoryID != $0.id && $0.hasWritableFields }
    }

    var isLarkAgent: Bool { purpose?.id == "lark.agent" }

    /// A use of Agents the hub lets the user set apart (Lark /agent, the Inbox's AI twin): fields of its own, following
    /// Agents while they are empty. An older hub sends no fields for it, and it stays a read-only use of Agents.
    static func followsAgents(_ p: ModelPurpose) -> Bool { p.parent == "agent.new" && p.hasWritableFields }

    var isAgentsOverride: Bool { purpose.map(Self.followsAgents) ?? false }

    /// The background supervisor's use (#5083, docs/assistant/model-config.md): the hub's catalog leaves it out and
    /// answers for it by its own id, so the list asks for it apart. An older hub has none (404) and lists none.
    static let supervise = "assistant.supervise"

    /// The background supervisor's use with fields of its own: it follows Main Chat while they are empty.
    static func followsMainChat(_ p: ModelPurpose) -> Bool { p.id == supervise && p.hasWritableFields }

    var isMainChatOverride: Bool { purpose.map(Self.followsMainChat) ?? false }

    /// A use set apart from its category (Agents or Main Chat), which it follows while its own fields are empty.
    static func followsCategory(_ p: ModelPurpose) -> Bool { followsAgents(p) || followsMainChat(p) }

    /// 「Follow Agents」 or 「Follow Main Chat」: what the shown use follows while its fields are empty; nil for a category.
    var followLabel: String? {
        if isMainChatOverride { return String(localized: "Follow Main Chat") }
        return isAgentsOverride ? String(localized: "Follow Agents") : nil
    }

    func inheritanceStatus(_ p: ModelPurpose) -> String {
        guard p.override.isEmpty else { return String(localized: "Separately configured") }
        return Self.followsMainChat(p) ? String(localized: "Follow Main Chat") : String(localized: "Follow Agents")
    }

    /// Launchers translate effort to their runtime; direct API calls still require Responses.
    var allowsEffort: Bool {
        guard let purpose, purpose.supports("effort") else { return false }
        let id = draft.provider ?? purpose.effective.provider
        guard let provider = providers.first(where: { $0.id == id }) else { return true }
        return purpose.kind == "launcher" || provider.kind != "api" || provider.api == "responses"
    }

    func modelSummary(_ p: ModelPurpose) -> String {
        let model = p.effective.model ?? p.effective.provider.flatMap { id in providers.first { $0.id == id }?.name }
        let effort = p.effective.effort.map { value in SessionModes.effortLevels.first { $0.id == value }?.title ?? value }
        // Main Chat on the Agent SDK says so; the CLI is the default and goes unsaid.
        let runtime = p.effective.runtime == AssistantRuntime.agentSDK ? AssistantRuntime.title(AssistantRuntime.agentSDK) : nil
        let result = [model, effort, runtime].compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: " · ")
        return result.isEmpty ? String(localized: "Not set up") : result
    }

    func summary(_ p: ModelPurpose) -> String {
        let provider = p.effective.provider.flatMap { id in providers.first { $0.id == id }?.name ?? id }
        return [provider, p.effective.model, p.effective.effort].compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: " · ")
    }

    var candidates: [ModelProvider] {
        guard let purpose else { return [] }
        return providers.filter { p in
            if p.hidden == true && p.id != draft.provider { return false }
            if let ids = purpose.providerIDs { return ids.contains(p.id) || p.id == draft.provider }
            return p.kind == purpose.kind || p.id == draft.provider
        }
    }

    /// Main Chat's runtimes (How It Runs); empty for every other use and on an older hub.
    var runtimes: [String] {
        guard let purpose, purpose.supports("runtime") else { return [] }
        return purpose.runtimes ?? []
    }

    /// Another provider clears the model and the effort; the runtime is not the provider's and stays.
    func chooseProvider(_ id: String) {
        let selected = id.isEmpty ? nil : id
        guard selected != draft.provider else { return }
        draft = ModelBinding(provider: selected, runtime: draft.runtime)
    }

    func restoreInheritance() { draft = ModelBinding() }

    /// Returning from a model picker must not reload the server binding over the draft.
    func loadIfNeeded(purposeID: String) async {
        guard loadedPurposeID != purposeID else { return }
        await load(purposeID: purposeID)
    }

    func load(purposeID: String? = nil) async {
        guard !loading && !saving else { return }
        loading = true
        defer { loading = false; loaded = true }
        do {
            guard let api = deps.api() else { throw APIError(status: 0, message: String(localized: "Not connected to the hub yet")) }
            if let purposeID {
                var result = try await api.modelPurpose(purposeID)
                if !result.purpose.hasWritableFields, let category = result.purpose.categoryID, category != result.purpose.id {
                    result = try await api.modelPurpose(category)
                }
                guard result.purpose.kind == "plugin" || (result.purpose.categoryID != nil &&
                    (result.purpose.hasWritableFields || (result.purpose.categoryID == result.purpose.id && result.purpose.uses != nil))) else {
                    purpose = nil
                    throw APIError(status: 0, message: String(localized: "This hub doesn't support model configuration by category yet. Update the server first."))
                }
                purpose = result.purpose
                providers = result.providers
                draft = result.purpose.override
                loadedPurposeID = purposeID
            } else {
                // The background supervisor's use is not in the catalog: asked for by its id, beside the catalog.
                let supervise = Self.supervise
                async let apart = try? api.modelPurpose(supervise)
                let result = try await api.modelConfiguration()
                let extra = await apart
                purposes = Self.listing(result.purposes, apart: extra?.purpose)
                providers = result.providers
                if !purposes.isEmpty && categories.isEmpty {
                    error = String(localized: "This hub doesn't support model configuration by category yet. Update the server first.")
                    return
                }
            }
            error = nil
            conflict = false
        } catch {
            self.error = Self.message(error, singlePurpose: purposeID != nil)
        }
    }

    func save() async -> Bool {
        guard !saving && !loading && !conflict, let purpose,
              purpose.hasWritableFields || (purpose.fields == nil && (purpose.kind == "plugin" || purpose.uses != nil)) else { return false }
        saving = true
        defer { saving = false }
        do {
            guard let api = deps.api() else { throw APIError(status: 0, message: String(localized: "Not connected to the hub yet")) }
            let binding = ModelBinding(provider: Self.trim(draft.provider), model: Self.trim(draft.model), effort: Self.trim(draft.effort),
                                       runtime: Self.trim(draft.runtime))
            let patch = ModelConfigurationPatch(revision: purpose.revision, binding: binding,
                                                fields: purpose.fields ?? ["provider", "model"])
            let result = try await api.saveModelPurpose(purpose.id, patch: patch)
            self.purpose = result.purpose
            providers = result.providers
            draft = result.purpose.override
            error = nil
            conflict = false
            await deps.updated()
            return true
        } catch {
            conflict = (error as? APIError)?.status == 409
            self.error = conflict ? String(localized: "This setting was changed elsewhere. Your edits are still here; reload, then save again.") : Self.message(error, singlePurpose: true)
            return false
        }
    }

    /// The catalog with the use the hub answers for apart (`supervise`) after it: only one that follows a listed
    /// category with fields of its own, and never twice once a hub lists it itself.
    static func listing(_ catalog: [ModelPurpose], apart: ModelPurpose?) -> [ModelPurpose] {
        guard let apart, followsMainChat(apart), let category = apart.categoryID, category != apart.id,
              catalog.contains(where: { $0.id == category }), !catalog.contains(where: { $0.id == apart.id }) else { return catalog }
        return catalog + [apart]
    }

    private static func trim(_ value: String?) -> String? {
        guard let value = value?.trimmingCharacters(in: .whitespacesAndNewlines), !value.isEmpty else { return nil }
        return value
    }

    private static func message(_ error: Error, singlePurpose: Bool = false) -> String {
        if (error as? APIError)?.status == 404 {
            return singlePurpose ? String(localized: "This use no longer exists, or the hub doesn't support unified model configuration yet. Refresh the list of uses or update the server.")
                : String(localized: "This hub doesn't support unified model configuration yet. Update the server first.")
        }
        return error.localizedDescription
    }
}
