import Foundation
import Testing
@testable import Persona

@MainActor
struct ExtensionSettingsLifecycleTests {
    private static let installed = #"{"id":"p","name":"P","kind":"ui","status":"disabled","source":"installed","enabled":false,"version":"1","origin":"market:m1/p","auto_upgrade":true,"auto_upgrade_paused":"disabled","config":{"value":"saved"},"previous_version":"0","permissions":[]}"#
    private static let upgrades = #"{"auto":true,"upgrades":[{"id":"p","market":"m1","from":"1","to":"2","state":"needs_permission"}]}"#
    private static let catalog = #"{"markets":[{"id":"m1","name":"Market","spec":"https://market.example.com","has_token":false}],"plugins":[{"id":"p","market":"m1","name":"P","version":"2","installed":"1","upgradable":true}]}"#
    private static let preview = #"{"preview_id":"pv","manifest":{"id":"p","version":"2"},"permissions":[{"key":"network","title":"Network"}],"permissions_digest":"digest","upgrade":true,"from":"1","market":"m1","more_permissions":true}"#

    private func decode(_ json: String = Self.installed) throws -> HubExtensionDetail {
        try JSON.decoder.decode(HubExtensionDetail.self, from: Data(json.utf8))
    }

    private func configure(_ hub: FakeHub) {
        hub.on("GET", "/api/v1/plugins/p/-/", json: Self.installed)
        hub.on("GET", "/api/v1/plugins/-/upgrades", json: Self.upgrades)
        hub.on("GET", "/api/v1/plugins/-/catalog", json: Self.catalog)
        hub.on("POST", "/api/v1/plugins/-/catalog/preview", json: Self.preview)
        hub.on("POST", "/api/v1/plugins/p/-/upgrade", json: Self.installed)
    }

    @Test func policySeparatesProvenanceFromCapabilities() throws {
        var detail = try decode()
        let market = ExtensionSettingsPolicy(detail: detail)
        #expect(market.market == "m1" && market.canCheck && market.canAutoUpgrade && market.canEnable)
        #expect(!market.canUninstall && market.uninstallReason == ExtensionSettingsPolicy.uninstallPreviewGap)
        #expect(market.runtime == String(localized: "Not provided"))
        #expect(market.runtime != detail.plugin.kind, "a contribution category is not execution-runtime metadata")
        #expect(ExtensionText.autoUpgradeShown(detail.plugin))
        var renamed = detail
        renamed.plugin.id = "renamed-p"
        let renamedPolicy = ExtensionSettingsPolicy(detail: renamed)
        #expect(renamedPolicy.market == "m1" && renamedPolicy.canCheck)
        #expect(renamedPolicy.provenance == detail.origin, "installation provenance may retain an earlier package ID")
        detail.plugin.source = "builtin"
        let builtin = ExtensionSettingsPolicy(detail: detail)
        #expect(builtin.canEnable && !builtin.canCheck && !builtin.canAutoUpgrade && !builtin.canUninstall)
        #expect(builtin.updateReason == String(localized: "Built-in extensions are updated with Persona."))
        detail.plugin.source = "hub.json"
        #expect(!ExtensionSettingsPolicy(detail: detail).canEnable)
        detail.plugin.source = "dev"
        detail.origin = "/private/example/extension"
        let development = ExtensionSettingsPolicy(detail: detail)
        #expect(development.source == String(localized: "Development"))
        #expect(!development.canCheck && !development.canAutoUpgrade && !development.canUninstall)
        detail.plugin.source = "installed"
        for origin in ["/private/example/extension", "https://user:password@example.com/archive", "remote:private-endpoint", "market:", "market:/p"] {
            detail.origin = origin
            let policy = ExtensionSettingsPolicy(detail: detail)
            #expect(policy.market == nil && !policy.canCheck)
            #expect(policy.provenance == String(localized: "Local installation"))
            #expect(!policy.provenance.contains(origin))
        }
        detail.origin = nil
        #expect(ExtensionSettingsPolicy(detail: detail).provenance == String(localized: "Not provided"))
        detail.origin = "market:m1/p"
        detail.plugin.autoUpgrade = nil
        #expect(!ExtensionSettingsPolicy(detail: detail).canAutoUpgrade)
    }

    @Test func aRemovedMarketKeepsThePreferenceButPausesUpdates() throws {
        var detail = try decode()
        detail.plugin.enabled = true
        detail.plugin.autoUpgradePaused = "market_removed"
        let removed = ExtensionSettingsPolicy(detail: detail)
        #expect(ExtensionText.autoUpgradeShown(detail.plugin), "pausing does not overwrite the user's saved preference")
        #expect(removed.market == "m1", "the installation provenance survives its market's removal")
        #expect(!removed.canAutoUpgrade, "a paused preference stays visible but cannot be edited")
        #expect(!removed.canCheck, "there is no registered market to check")
        #expect(removed.updateReason == String(localized: "Its market was removed. Add the market again with the same address to check for new versions."))
        #expect(removed.uninstallReason == ExtensionSettingsPolicy.uninstallPreviewGap, "pausing update checks must not rewrite the installation provenance")

        detail.plugin.autoUpgrade = false
        #expect(!ExtensionText.autoUpgradeShown(detail.plugin), "an explicitly disabled preference stays off too")
    }

    /// #4747 (the web's #1828): while the manager's master switch is off, the extension's Auto-upgrade switch keeps
    /// showing its own saved value but can't be flipped; with the master switch on it can again. A hub that does not
    /// say (no route: an older hub) leaves it editable.
    @Test(arguments: [true, false]) func theMasterSwitchOffGreysTheSwitchOutKeepingItsValue(own: Bool) async throws {
        var detail = try decode()
        detail.plugin.autoUpgrade = own
        let policy = ExtensionSettingsPolicy(detail: detail)
        for (auto, flips) in [(false, false), (true, true)] {
            let hub = FakeHub()
            hub.on("GET", "/api/v1/plugins/-/upgrades", json: #"{"auto":\#(auto),"upgrades":[]}"#)
            let lifecycle = ExtensionSettingsLifecycle(id: "p", manager: "assistant", deps: .init(api: { hub.client }))
            await lifecycle.loadMetadata(detail: detail)
            #expect(lifecycle.globalAuto == auto)
            #expect(policy.canFlipAutoUpgrade(globalAuto: lifecycle.globalAuto) == flips)
            #expect(ExtensionText.autoUpgradeShown(detail.plugin) == own, "the saved value is shown either way")
        }
        let old = FakeHub()
        let lifecycle = ExtensionSettingsLifecycle(id: "p", manager: "assistant", deps: .init(api: { old.client }))
        await lifecycle.loadMetadata(detail: detail)
        #expect(lifecycle.globalAuto == nil)
        #expect(policy.canFlipAutoUpgrade(globalAuto: nil), "an unknown master switch does not lock the extension's")
        // One that can't auto-upgrade at all stays locked whatever the master switch says.
        detail.plugin.autoUpgrade = nil
        #expect(!ExtensionSettingsPolicy(detail: detail).canFlipAutoUpgrade(globalAuto: true))
    }

    @Test func attachedAndSandboxDecodeTheirExistingContract() throws {
        let extra = #", "attached":{"online":false,"since":"2026-11-01T00:00:00Z","error":"offline","version":"2"},"sandbox":{"enabled":true,"default":true,"chosen":false,"active":false,"why":"unsupported","detail":"Unavailable","trusted":true,"egress":{"allowed":3}}"#
        let detail = try decode(String(Self.installed.dropLast()) + extra + "}")
        #expect(detail.plugin.attached?.online == false && detail.plugin.attached?.version == "2")
        #expect(detail.plugin.attached?.since != nil)
        #expect(detail.plugin.sandbox?.enabled == true && detail.plugin.sandbox?.active == false)
        #expect(detail.plugin.sandbox?.egress["allowed"] == .number(3))
        let attached = ExtensionSettingsPolicy(detail: detail)
        #expect(attached.source == String(localized: "Attached"))
        #expect(!attached.canCheck && !attached.canAutoUpgrade && !attached.canUninstall)
        #expect(attached.provenance == String(localized: "Attached"))
    }

    @Test func previewRequiresFinalConfirmationAndBindsEveryRequestToManager() async throws {
        let hub = FakeHub()
        configure(hub)
        let lifecycle = ExtensionSettingsLifecycle(id: "p", manager: "assistant", deps: .init(api: { hub.client }))
        await lifecycle.loadMetadata(detail: try decode())
        await lifecycle.checkForUpdates()
        #expect(lifecycle.globalAuto == true && lifecycle.candidate?.to == "2")
        await lifecycle.prepareUpgrade()
        #expect(lifecycle.preview?.morePermissions == true)
        #expect(hub.sent("POST", "/api/v1/plugins/p/-/upgrade").isEmpty)
        lifecycle.dismissPreview()
        #expect(!((await lifecycle.confirmUpgrade())))
        #expect(hub.sent("POST", "/api/v1/plugins/p/-/upgrade").isEmpty)
        await lifecycle.prepareUpgrade()
        #expect(await lifecycle.confirmUpgrade())
        let request = try #require(hub.sent("POST", "/api/v1/plugins/p/-/upgrade").first)
        let body = try JSON.decoder.decode(JSONValue.self, from: request.body ?? Data())
        #expect(body["preview_id"] == .string("pv") && body["permissions_digest"] == .string("digest"))
        #expect(hub.requests.allSatisfy { $0.headers[APIClient.managerHeader] == "assistant" })
        #expect(hub.requests.allSatisfy { $0.method != "DELETE" && !$0.path.hasSuffix("rollback") })
        #expect(lifecycle.previousVersion == "0" && lifecycle.rollbackReason == ExtensionSettingsPolicy.rollbackPreviewGap)
    }

    @Test func unavailableAndMissingMarketsNeverLookUpToDateOrClearPageDraft() async throws {
        let hub = FakeHub()
        configure(hub)
        let page = ExtensionModel(id: "p", deps: .init(api: { hub.client }))
        await page.load()
        page.draft["value"] = .string("editing")
        page.numberText["count"] = "not-a-number"
        let lifecycle = ExtensionSettingsLifecycle(id: "p", deps: .init(api: { hub.client }))
        await lifecycle.loadMetadata(detail: try #require(page.detail))
        hub.on("GET", "/api/v1/plugins/-/catalog", json: #"{"markets":[{"id":"m1","name":"Market","has_token":false,"error":"unreachable"}],"plugins":[]}"#)
        await lifecycle.checkForUpdates()
        #expect(lifecycle.error == String(localized: "The extension market is unavailable. Check its source and try again."))
        #expect(lifecycle.candidate != nil)
        hub.on("GET", "/api/v1/plugins/-/catalog", json: #"{"markets":[{"id":"m1","name":"Market","has_token":false}],"plugins":[]}"#)
        await lifecycle.checkForUpdates()
        #expect(lifecycle.error == String(localized: "This extension is no longer listed in its market."))
        #expect(lifecycle.candidate != nil)
        #expect(page.draft["value"] == .string("editing") && page.numberText["count"] == "not-a-number")
    }

    /// A rename (#2511): the market lists the new version under its new ID. Check for updates finds it, the row says the
    /// ID changes (never "auto-update off"), and the preview of the new ID upgrades the installed extension.
    @Test func aRenamedExtensionIsCheckedPreviewedAndUpgraded() async throws {
        let hub = FakeHub()
        configure(hub)
        hub.on("GET", "/api/v1/plugins/-/upgrades", json: #"{"auto":true,"upgrades":[]}"#)
        hub.on("GET", "/api/v1/plugins/-/catalog", json: #"{"markets":[{"id":"m1","name":"Market","spec":"https://market.example.com","has_token":false}],"plugins":[{"id":"p-next","market":"m1","name":"P","version":"2","installed":"1","upgradable":true,"replaces":"p","rename_from":"p"}]}"#)
        hub.on("POST", "/api/v1/plugins/-/catalog/preview", json: #"{"preview_id":"pv","manifest":{"id":"p-next","version":"2"},"permissions":[],"permissions_digest":"digest","upgrade":true,"from":"1","market":"m1","rename_from":"p","warnings":["ID p becomes p-next"]}"#)
        let lifecycle = ExtensionSettingsLifecycle(id: "p", deps: .init(api: { hub.client }))
        await lifecycle.loadMetadata(detail: try decode())
        #expect(await lifecycle.checkForUpdates())
        let candidate = try #require(lifecycle.candidate)
        #expect(lifecycle.error == nil && candidate.renameTo == "p-next" && candidate.state == "needs_permission")
        #expect(ExtensionText.upgradeAction(candidate) != nil)
        #expect(ExtensionText.upgradeDetail(candidate).contains("p-next"), "the row says the ID changes, not that permissions changed")
        await lifecycle.prepareUpgrade()
        #expect(lifecycle.error == nil && lifecycle.preview?.renameFrom == "p")
        #expect(await lifecycle.confirmUpgrade())
        #expect(hub.sent("POST", "/api/v1/plugins/p/-/upgrade").count == 1, "the installed extension is upgraded under its own ID")
    }

    // MARK: - a hub that runs operations (#3692)

    private static func op(_ state: String, id: String = "op1", step: String = "download", updated: String = "2026-11-10T08:00:05Z",
                           previewID: String? = nil, retryable: Bool = false) -> String {
        let failed = state == "failed"
        return #"""
        {"id":"\#(id)","kind":"upgrade","extension":"p","name":"P","version":"2","from":"1","market":"m1","state":"\#(state)",
         "step":"\#(step)","retryable":\#(retryable),"preview_id":\#(previewID.map { "\"\($0)\"" } ?? "null"),
         "error":\#(failed ? #""包下载停滞，请重试。""# : "null"),"error_code":\#(failed ? #""download_stalled""# : "null"),
         "steps":[{"id":"download","state":"\#(step == "download" ? (failed ? "failed" : "running") : "done")","bytes":4537677,"total":13008756},
                  {"id":"consent","state":"\#(state == "needs_consent" ? "running" : step == "download" ? "pending" : "done")"},
                  {"id":"install","state":"\#(state == "succeeded" ? "done" : "pending")"}],
         "started_at":"2026-11-10T08:00:00Z","updated_at":"\#(updated)"}
        """#
    }

    private static func waiting(_ id: String = "op1", updated: String = "2026-11-10T08:00:06Z") -> String {
        String(op("needs_consent", id: id, step: "consent", updated: updated, previewID: "pv").dropLast()) + #","preview":"# + preview + "}"
    }

    private func stepped(_ hub: FakeHub) -> ExtensionOperations {
        ExtensionOperations(deps: .init(api: { hub.client }, features: { ["extension_operations"] }, hub: { "fake" }, poll: .milliseconds(10)))
    }

    /// Review previews in the background: the page stays usable and shows the operation while the package downloads
    /// (a phone's request no longer waits on a slow download until it times out), the sheet opens once it waits for
    /// consent, and Update goes on with the same operation, every request to the extension's manager.
    @Test func reviewPreviewsAsAnOperationAndGoesOnWithIt() async throws {
        let hub = FakeHub()
        configure(hub)
        var previewBody: JSONValue?
        var whileDownloading: (busy: Bool, previewing: Bool, canReview: Bool, state: ExtensionOperation.State?)?
        let lifecycle = ExtensionSettingsLifecycle(id: "p", manager: "assistant", deps: .init(api: { hub.client }, operations: stepped(hub)))
        hub.on("POST", "/api/v1/plugins/-/catalog/preview") { req in
            previewBody = try JSON.decoder.decode(JSONValue.self, from: req.body ?? Data())
            return Data((#"{"operation":"# + Self.op("running") + "}").utf8)
        }
        hub.on("GET", "/api/v1/plugins/-/operations/op1") { _ in
            whileDownloading = whileDownloading ?? (lifecycle.busy, lifecycle.previewing, lifecycle.canReview, lifecycle.operation?.state)
            return Data(Self.waiting().utf8)
        }
        var upgradeBody: JSONValue?
        hub.on("POST", "/api/v1/plugins/p/-/upgrade") { req in
            upgradeBody = try JSON.decoder.decode(JSONValue.self, from: req.body ?? Data())
            hub.on("GET", "/api/v1/plugins/-/operations/op1", json: Self.op("succeeded", step: "install", updated: "2026-11-10T08:00:09Z"))
            return Data((#"{"operation":"# + Self.op("running", step: "install", updated: "2026-11-10T08:00:07Z") + "}").utf8)
        }
        await lifecycle.loadMetadata(detail: try decode())
        await lifecycle.prepareUpgrade()
        #expect(previewBody?["async"] == .bool(true) && previewBody?["market"] == .string("m1"))
        let during = try #require(whileDownloading)
        #expect(!during.busy && during.previewing && !during.canReview && during.state == .running,
                "the form stays usable, Review waits, the card shows the download")
        let shown = try #require(lifecycle.preview, "the sheet opens once the operation waits for consent")
        #expect(shown.morePermissions == true && lifecycle.operation?.state == .needsConsent && !lifecycle.previewing)
        let consent = lifecycle.takeConsent()
        #expect(consent == "op1")
        lifecycle.dismissPreview() // the sheet closing after Update does not cancel it
        #expect(await lifecycle.confirmUpgrade(shown, consent: consent))
        #expect(upgradeBody?["preview_id"] == .string("pv") && upgradeBody?["async"] == .bool(true))
        #expect(hub.sent("DELETE", "/api/v1/plugins/-/operations/op1").isEmpty)
        #expect(lifecycle.operation == nil && lifecycle.operationState == .succeeded && lifecycle.candidate == nil)
        #expect(hub.requests.allSatisfy { $0.headers[APIClient.managerHeader] == "assistant" })
    }

    /// A download that fails says why on the card (not under the button), Review can start again, Retry goes back to
    /// the hub, and closing the sheet it opens cancels that operation.
    @Test func aFailedReviewShowsWhyAndRetries() async throws {
        let hub = FakeHub()
        configure(hub)
        hub.on("POST", "/api/v1/plugins/-/catalog/preview", json: #"{"operation":"# + Self.op("running") + "}")
        hub.on("GET", "/api/v1/plugins/-/operations/op1", json: Self.op("failed", updated: "2026-11-10T08:00:30Z", retryable: true))
        hub.on("POST", "/api/v1/plugins/-/operations/op1/retry", json: #"{"operation":"# + Self.op("running", id: "op2", updated: "2026-11-10T08:00:31Z") + "}")
        hub.on("GET", "/api/v1/plugins/-/operations/op2", json: Self.waiting("op2", updated: "2026-11-10T08:00:40Z"))
        hub.on("DELETE", "/api/v1/plugins/-/operations/op2") { _ in Data() }
        let lifecycle = ExtensionSettingsLifecycle(id: "p", deps: .init(api: { hub.client }, operations: stepped(hub)))
        await lifecycle.loadMetadata(detail: try decode())
        await lifecycle.prepareUpgrade()
        let failed = try #require(lifecycle.operation)
        #expect(failed.state == .failed && failed.failedStep?.id == "download" && failed.error == "包下载停滞，请重试。")
        #expect(lifecycle.error == nil && lifecycle.preview == nil && lifecycle.canReview, "the card says why; nothing spins")
        #expect(await lifecycle.retry() == false)
        #expect(hub.sent("POST", "/api/v1/plugins/-/operations/op1/retry").count == 1)
        #expect(lifecycle.preview?.previewID == "pv" && lifecycle.operation?.id == "op2")
        lifecycle.dismissPreview()
        for _ in 0..<50 where hub.sent("DELETE", "/api/v1/plugins/-/operations/op2").isEmpty { try await Task.sleep(for: .milliseconds(10)) }
        #expect(hub.sent("DELETE", "/api/v1/plugins/-/operations/op2").count == 1, "closed without Update: canceled")
    }

    @Test func busyPreventsReentrantRequests() async throws {
        let hub = FakeHub()
        configure(hub)
        let lifecycle = ExtensionSettingsLifecycle(id: "p", deps: .init(api: { hub.client }))
        await lifecycle.loadMetadata(detail: try decode())
        await lifecycle.prepareUpgrade()
        let prepared = lifecycle.preview
        hub.on("POST", "/api/v1/plugins/p/-/upgrade") { _ in
            #expect(lifecycle.busy)
            lifecycle.dismissPreview()
            #expect(lifecycle.preview == prepared, "a busy confirmation cannot discard its preview")
            return Data(Self.installed.utf8)
        }
        #expect(await lifecycle.confirmUpgrade())
        #expect(!lifecycle.busy)
        #expect(!((await lifecycle.confirmUpgrade())))
        #expect(hub.sent("POST", "/api/v1/plugins/p/-/upgrade").count == 1)
    }

    @Test func autoUpgradeHasItsOwnGuardAndKeepsTheDraft() async throws {
        let hub = FakeHub()
        configure(hub)
        let page = ExtensionModel(id: "p", manager: "assistant", deps: .init(api: { hub.client }))
        await page.load()
        page.draft["value"] = .string("editing")
        hub.on("PUT", "/api/v1/plugins/p/-/auto-upgrade") { req in
            #expect(page.changingAutoUpgrade)
            #expect(req.headers[APIClient.managerHeader] == "assistant")
            return Data(Self.installed.utf8)
        }
        await page.setAutoUpgrade(false)
        #expect(!page.changingAutoUpgrade && page.draft["value"] == .string("editing"))
        #expect(hub.sent("PUT", "/api/v1/plugins/p/-/auto-upgrade").count == 1)
    }
}
