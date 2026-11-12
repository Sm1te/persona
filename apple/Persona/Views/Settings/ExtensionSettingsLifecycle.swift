import Foundation
import Observation

/// Capabilities follow provenance, never the extension's current running state.
/// Raw origins may contain local paths or credentials and must not be shown.
nonisolated struct ExtensionSettingsPolicy {
    let canEnable: Bool
    let canCheck: Bool
    let canAutoUpgrade: Bool
    let canUninstall: Bool
    let updateReason: String
    let uninstallReason: String
    let source: String
    let provenance: String
    let runtime: String
    let market: String?

    static var uninstallPreviewGap: String {
        String(localized: "This assistant does not provide a safe uninstall preview. Remove the extension at its source.")
    }
    static var rollbackPreviewGap: String {
        String(localized: "This assistant does not provide a rollback preview.")
    }
    /// The footer while the manager's master switch is off (#4747, as the web's #1828).
    static var globalAutoOffReason: String {
        String(localized: "Auto-upgrade is paused because automatic extension updates are off for this assistant. Your setting is kept and applies again once they're turned back on.")
    }

    /// Whether the extension's Auto-upgrade switch can be flipped. While the manager's master switch is off
    /// (`globalAuto` false) it can't: it keeps showing the extension's own value (`ExtensionText.autoUpgradeShown`),
    /// which applies again once the master switch is back on (#4747). An unknown master switch (still loading, or a
    /// hub without the route) leaves it editable, so a hub that never answers does not lock it.
    func canFlipAutoUpgrade(globalAuto: Bool?) -> Bool {
        canAutoUpgrade && globalAuto != false
    }

    init(detail: HubExtensionDetail) {
        let plugin = detail.plugin
        // kind is Manifest.Kind(), a contribution category; installed runtime metadata is not exposed.
        runtime = String(localized: "Not provided")
        let builtin = plugin.source == "builtin"
        let configured = detail.readOnly || ["hub.json", "manager.json"].contains(plugin.source ?? "")
        let attached = plugin.attached != nil
        let installed = plugin.source == "installed" && !attached && !configured
        let origin = detail.origin ?? ""
        let parts = origin.hasPrefix("market:") ? String(origin.dropFirst(7)).split(separator: "/", maxSplits: 1, omittingEmptySubsequences: false) : []
        market = installed && parts.count == 2 && !parts[0].isEmpty && !parts[1].isEmpty ? String(parts[0]) : nil
        canEnable = !configured && plugin.source != "dev" && (plugin.enabled != nil || builtin)
        let marketRemoved = plugin.marketWasRemoved
        canCheck = market != nil && !marketRemoved
        canAutoUpgrade = canCheck && plugin.autoUpgrade != nil
        // DELETE is destructive even when dependency checks fail. There is no safe removal preview API.
        canUninstall = false
        if attached {
            source = String(localized: "Attached")
            updateReason = String(localized: "This extension is not managed by an extension market. Update it at its source.")
            uninstallReason = String(localized: "This extension is managed outside the extension market. Remove it at its source.")
        } else if builtin {
            source = String(localized: "Built-in")
            updateReason = String(localized: "Built-in extensions are updated with Persona.")
            uninstallReason = String(localized: "Built-in extensions cannot be uninstalled. Turn the extension off instead.")
        } else if configured {
            source = String(localized: "Configured (manager.json)")
            updateReason = String(localized: "This extension is not managed by an extension market. Update it at its source.")
            uninstallReason = String(localized: "This extension is managed outside the extension market. Remove it at its source.")
        } else {
            switch plugin.source {
            case "dev": source = String(localized: "Development")
            case "installed": source = String(localized: "Installed [Extension Market Views a8b6c3]")
            default: source = String(localized: "Not provided")
            }
            if marketRemoved {
                updateReason = String(localized: "Its market was removed. Add the market again with the same address to check for new versions.")
            } else if canCheck && !canAutoUpgrade {
                updateReason = String(localized: "This assistant does not provide this action.")
            } else {
                updateReason = canCheck ? "" : String(localized: "This extension is not managed by an extension market. Update it at its source.")
            }
            uninstallReason = market != nil ? Self.uninstallPreviewGap : String(localized: "This extension is managed outside the extension market. Remove it at its source.")
        }
        // Manager-owned market identifiers are safe provenance; never expose local paths or URLs.
        provenance = attached ? String(localized: "Attached") :
            (origin.isEmpty ? String(localized: "Not provided") :
                (market != nil ? origin : String(localized: "Local installation")))
    }
}

/// Shared by standard settings views. It never touches the configuration page's draft.
///
/// Review on a hub that runs operations (#3692) previews as the market page does (`MarketExtensionModel`): the hub
/// downloads and checks in the background, the page shows the operation's steps (`operation`), the Update sheet opens
/// when it waits for consent, and agreeing goes on with the same operation. A slow download no longer holds one
/// request until the phone gives up on it. An older hub answers the preview when it is done, as before.
@Observable final class ExtensionSettingsLifecycle {
    struct Deps {
        var api: () -> APIClient?
        /// The app's operations; nil: a store of its own that runs none (tests of the synchronous path).
        var operations: ExtensionOperations?
        static var live: Deps { .init(api: { AppModel.shared.api }, operations: .shared) }
    }

    let id: String
    let manager: String?
    private(set) var busy = false
    private(set) var error: String?
    private(set) var globalAuto: Bool?
    private(set) var candidate: ExtensionUpgrade?
    private(set) var checked = false
    private(set) var preview: ExtensionPreview?
    private(set) var detail: HubExtensionDetail?
    /// The hub is downloading and checking the new version (its operation's steps are on the page meanwhile).
    private(set) var previewing = false
    /// The operation the open preview belongs to: agreeing goes on with it, closing the sheet without agreeing
    /// cancels it.
    @ObservationIgnored private(set) var consentOf: String?
    @ObservationIgnored private let deps: Deps
    @ObservationIgnored private let operations: ExtensionOperations

    var policy: ExtensionSettingsPolicy? { detail.map(ExtensionSettingsPolicy.init(detail:)) }
    var previousVersion: String? { detail?.previousVersion }
    var rollbackReason: String { ExtensionSettingsPolicy.rollbackPreviewGap }

    init(id: String, manager: String? = nil, deps: Deps = .live) {
        self.id = id
        self.manager = manager
        self.deps = deps
        operations = deps.operations ?? ExtensionOperations(deps: .init(api: deps.api, features: { [] }, hub: { nil }))
    }

    /// The upgrade going on, waiting for consent, or the one that failed (one that went through or was canceled is
    /// not shown).
    var operation: ExtensionOperation? {
        latestOperation.flatMap { $0.state == .succeeded || $0.state == .canceled ? nil : $0 }
    }

    /// The state of its latest operation, the ones that ended included (to read the page again when one went through).
    var operationState: ExtensionOperation.State? { latestOperation?.state }

    /// Its latest operation from its market; a rename's is under the new ID once its package was read.
    private var latestOperation: ExtensionOperation? {
        guard let market = policy?.market else { return nil }
        return operations.operation(for: id, market: market, manager: manager)
            ?? candidate?.renameTo.flatMap { operations.operation(for: $0, market: market, manager: manager) }
    }

    /// Review can start: nothing else is going on for it.
    var canReview: Bool { !busy && !previewing && operation?.isActive != true }

    private func api() throws -> APIClient {
        guard let api = deps.api()?.manager(manager) else {
            throw APIError(status: 0, message: String(localized: "Not connected to the hub yet"))
        }
        return api
    }

    func loadMetadata(detail: HubExtensionDetail) async {
        guard !busy, detail.plugin.id == id else { return }
        busy = true
        defer { busy = false }
        self.detail = detail
        preview = nil
        candidate = detail.plugin.upgrade
        checked = false
        globalAuto = nil
        error = nil
        guard ExtensionSettingsPolicy(detail: detail).canCheck else { return }
        do {
            let upgrades = try await api().pluginUpgrades()
            globalAuto = upgrades.auto
            candidate = upgrades.upgrades.first { $0.id == id }
        } catch { record(error) }
    }

    @discardableResult func checkForUpdates() async -> Bool {
        guard !busy, policy?.canCheck == true else { return false }
        busy = true
        checked = false
        defer { busy = false }
        do {
            _ = try await checkedCandidate(refresh: true)
            error = nil
            return true
        } catch { record(error); return false }
    }

    /// Read canonical installed metadata and the market before saying no update exists.
    private func checkedCandidate(refresh: Bool) async throws -> ExtensionUpgrade? {
        let api = try api()
        let detail = try await api.plugin(id)
        self.detail = detail
        let policy = ExtensionSettingsPolicy(detail: detail)
        guard let market = policy.market else {
            throw APIError(status: 0, message: policy.updateReason)
        }
        let catalog = try await api.pluginCatalog(market: market, refresh: refresh)
        guard let entryMarket = catalog.markets.first(where: { $0.id == market }), entryMarket.error?.isEmpty != false else {
            throw APIError(status: 0, message: String(localized: "The extension market is unavailable. Check its source and try again."))
        }
        // A renamed extension is in its market under its new ID (#2511).
        guard let entry = catalog.plugins.first(where: { $0.market == market && $0.pluginID == id })
            ?? catalog.plugins.first(where: { $0.market == market && $0.renameFrom == id }) else {
            throw APIError(status: 0, message: String(localized: "This extension is no longer listed in its market."))
        }
        if let problem = entry.problem, !problem.isEmpty { throw APIError(status: 0, message: problem) }
        let upgrades = try await api.pluginUpgrades()
        globalAuto = upgrades.auto
        let next = upgrades.upgrades.first { $0.id == id && $0.market == market }
        // A version under a new ID is never installed automatically: it waits for the owner's confirmation.
        let renamed = entry.pluginID != id
        candidate = entry.upgradable == true ? (next ?? ExtensionUpgrade(id: id, name: entry.name, market: market,
            from: detail.plugin.version ?? entry.installed ?? "", to: entry.version, state: renamed ? "needs_permission" : "off",
            renameTo: renamed ? entry.pluginID : nil)) : nil
        checked = true
        return candidate
    }

    func prepareUpgrade() async {
        guard canReview, policy?.canCheck == true else { return }
        preview = nil
        let candidate: ExtensionUpgrade?
        busy = true
        do {
            candidate = try await checkedCandidate(refresh: false)
        } catch {
            busy = false
            record(error)
            return
        }
        guard let candidate else { busy = false; error = nil; return }
        if operations.stepped {
            // The request answers at once with the operation: the page shows its steps, not a spinner, for as long
            // as the download takes, and the sheet opens when it waits for consent.
            previewing = true
            defer { previewing = false }
            do {
                let started = try await operations.startPreview(market: candidate.market, extension: id, manager: manager)
                busy = false
                error = nil
                let op = await operations.settled(started)
                if op.state == .needsConsent { try await review(op) }
            } catch {
                busy = false
                record(error)
            }
            return
        }
        defer { busy = false }
        do {
            let next = try await api().previewCatalogExtension(market: candidate.market, plugin: id)
            guard previewMatches(next, market: candidate.market) else {
                throw APIError(status: 0, message: String(localized: "This assistant does not provide this action."))
            }
            preview = next
            error = nil
        } catch { record(error) }
    }

    /// Opens the preview an operation waits on (Review Permissions on its card, the page opened again, a retry).
    func review(_ op: ExtensionOperation) async throws {
        guard op.state == .needsConsent, let market = policy?.market,
              let pv = try await operations.consentPreview(op.id, manager: op.manager) else { return }
        guard previewMatches(pv, market: market) else {
            await operations.dismiss(op.id, manager: op.manager)
            throw APIError(status: 0, message: String(localized: "This assistant does not provide this action."))
        }
        consentOf = op.id
        preview = pv
    }

    /// A preview this page may confirm: an upgrade of this extension, from its market, from the version installed.
    private func previewMatches(_ pv: ExtensionPreview, market: String) -> Bool {
        pv.upgrades(id) && pv.upgrade == true && pv.market == market && pv.from == detail?.plugin.version
    }

    /// The operation the open preview belongs to, taken: the sheet closing after this does not cancel it.
    func takeConsent() -> String? {
        defer { consentOf = nil }
        return consentOf
    }

    /// Called only by the user's final confirmation; preparing never agrees to added permissions. `consent`: the
    /// operation it goes on with (`takeConsent()`, taken before the sheet closed): the sheet closes at once and the
    /// steps show on the page; nil: the synchronous upgrade of an older hub. true: upgraded.
    @discardableResult func confirmUpgrade(_ agreed: ExtensionPreview? = nil, consent: String? = nil) async -> Bool {
        guard !busy, let preview = agreed ?? preview, policy?.canCheck == true else { return false }
        if let problem = preview.confirmationProblem(at: .now) { error = problem; return false }
        guard preview.upgrades(id), preview.market == policy?.market, preview.from == detail?.plugin.version else {
            error = String(localized: "This assistant does not provide this action.")
            return false
        }
        busy = true
        defer { busy = false }
        if consent != nil {
            self.preview = nil
            do {
                // The card shows where a failure stopped, with Retry.
                guard try await operations.agree(id, preview: preview, manager: manager).state == .succeeded else { return false }
                candidate = nil
                error = nil
                return true
            } catch { record(error); return false }
        }
        do {
            detail = try await api().upgradeExtension(id, preview: preview)
            self.preview = nil
            candidate = nil
            error = nil
            return true
        } catch { record(error); return false }
    }

    /// The sheet closed without Update: the operation waiting on it is canceled (the hub drops the preview).
    func dismissPreview() {
        guard !busy else { return }
        preview = nil
        guard let op = consentOf else { return }
        consentOf = nil
        Task { await operations.dismiss(op, manager: manager) }
    }

    /// Retry on the card of a failed operation: the hub starts it again (its package is in its cache, or still
    /// downloading); the sheet opens when it waits for consent again. true: upgraded.
    func retry() async -> Bool {
        guard let op = operation, op.state == .failed, !previewing else { return false }
        guard op.retryable, !op.isLocal else {
            await operations.dismiss(op.id, manager: op.manager)
            await prepareUpgrade()
            return false
        }
        previewing = true
        defer { previewing = false }
        do {
            let next = try await operations.retry(op)
            error = nil
            switch next.state {
            case .needsConsent: try await review(next)
            case .succeeded: candidate = nil; return true
            default: break
            }
        } catch { record(error) }
        return false
    }

    /// Close or Cancel on the card: the hub forgets one that ended, cancels one that waits or still downloads.
    func dismissOperation() async {
        guard let op = operation else { return }
        if op.id == consentOf { consentOf = nil }
        await operations.dismiss(op.id, manager: op.manager)
    }

    /// What the hub has going on, when the page opens (one started earlier shows its steps).
    func loadOperations() async { await operations.reload() }

    private func record(_ error: Error) {
        if let apiError = error as? APIError, [404, 405, 501].contains(apiError.status) {
            self.error = String(localized: "This assistant does not provide this action.")
        } else { self.error = error.localizedDescription }
    }
}
