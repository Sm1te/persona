import Foundation
import Observation

// Settings › Extensions › Extension Market (docs/extensions/extensions.md「Extension marketplace」): the plugins of the markets the hub has, searched and filtered by
// market; one plugin's page with what it may do, installed or upgraded after its preview (the permissions agreed to
// are the preview's digest) and removed when it came from that market; and the markets themselves (added with an
// optional token that is never shown again, refreshed, removed). Only with a hub that says `markets: true`.

/// One of GET /api/v1/plugins/-/markets.
nonisolated struct ExtensionMarket: Codable, Hashable, Sendable, Identifiable {
    var id: String
    /// What was added: https://… or github:owner/repo.
    var spec: String
    var name: String
    var description: String?
    var homepage: String?
    var hasToken: Bool
    /// How many plugins its index lists.
    var plugins: Int?
    var warnings: [String]?
    /// Why its index could not be read the last time.
    var error: String?
    var fetchedAt: Date?
    var addedAt: Date?
    /// The market every hub has (#2351); nil from a hub before it.
    var official: Bool?

    /// The official market reads Official Extensions whatever its index names it (#5017): the index's name is its publisher's.
    var title: String {
        if official == true { return ExtensionMarket.officialTitle }
        return name.isEmpty ? spec : name
    }
    static var officialTitle: String { String(localized: "Official extensions") }
    /// A GitHub market read without a token: GitHub's 60 requests an hour for the whole network, so a token is what
    /// fixes it failing, public repositories included.
    var wantsGitHubToken: Bool { spec.hasPrefix("github:") && !hasToken }

    enum CodingKeys: String, CodingKey {
        case id, spec, name, description, homepage, plugins, warnings, error, official
        case hasToken = "has_token", fetchedAt = "fetched_at", addedAt = "added_at"
    }
}

extension ExtensionMarket {
    private enum NewKeys: String, CodingKey { case extensions }

    /// The count is `extensions` under /api/v1/extensions, `plugins` under the old path (#1844).
    nonisolated init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        spec = try c.decodeIfPresent(String.self, forKey: .spec) ?? ""
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? ""
        description = try c.decodeIfPresent(String.self, forKey: .description)
        homepage = try c.decodeIfPresent(String.self, forKey: .homepage)
        hasToken = try c.decodeIfPresent(Bool.self, forKey: .hasToken) ?? false
        official = try c.decodeIfPresent(Bool.self, forKey: .official)
        plugins = try c.decodeIfPresent(Int.self, forKey: .plugins)
            ?? decoder.container(keyedBy: NewKeys.self).decodeIfPresent(Int.self, forKey: .extensions)
        warnings = try c.decodeIfPresent([String].self, forKey: .warnings)
        error = try c.decodeIfPresent(String.self, forKey: .error)
        fetchedAt = try c.decodeIfPresent(Date.self, forKey: .fetchedAt)
        addedAt = try c.decodeIfPresent(Date.self, forKey: .addedAt)
    }
}

/// One plugin of the catalog (`CatalogItem`): what a market says of it, and how it stands on this hub.
nonisolated struct ExtensionCatalogItem: Codable, Hashable, Sendable, Identifiable {
    var market: String
    var marketName: String?
    var pluginID: String
    var name: String
    var version: String
    var description: String?
    var publisher: String?
    var homepage: String?
    var tags: [String]?
    var contributes: [String: [String]]?
    var permissions: [ExtensionPermission]?
    /// Why this hub cannot install it (no package for its platform, a newer hub needed).
    var problem: String?
    /// The version installed, if it is.
    var installed: String?
    var installedSource: String?
    var fromThisMarket: Bool?
    var upgradable: Bool?
    /// The ID this one takes over in its market (a rename, docs/extensions/extensions.md "Declared identity changes").
    var replaces: String?
    /// The installed extension this one upgrades under a new ID, once the hub checked it came from this market.
    var renameFrom: String?

    var id: String { market + "/" + pluginID }

    enum CodingKeys: String, CodingKey {
        case market, name, version, description, publisher, homepage, tags, contributes, permissions, problem, installed, upgradable, replaces
        case pluginID = "id", marketName = "market_name", installedSource = "installed_source", fromThisMarket = "from_this_market"
        case renameFrom = "rename_from"
    }
}

/// GET /api/v1/plugins/-/catalog.
nonisolated struct ExtensionCatalog: Decodable, Sendable {
    var markets: [ExtensionMarket]
    var plugins: [ExtensionCatalogItem]
    var platform: String?

    private enum CodingKeys: String, CodingKey { case markets, plugins, extensions, platform }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        markets = try c.decodeIfPresent([ExtensionMarket].self, forKey: .markets) ?? []
        // `extensions` under /api/v1/extensions, `plugins` under the old path (#1844).
        plugins = try c.decodeIfPresent([ExtensionCatalogItem].self, forKey: .extensions)
            ?? c.decodeIfPresent([ExtensionCatalogItem].self, forKey: .plugins) ?? []
        platform = try c.decodeIfPresent(String.self, forKey: .platform)
        // The official market's plugins carry its name as the market does (#5017).
        let official = Set(markets.filter { $0.official == true }.map(\.id))
        for i in plugins.indices where official.contains(plugins[i].market) {
            plugins[i].marketName = ExtensionMarket.officialTitle
        }
    }
}

/// What installing (or upgrading to) a plugin of a market would be: downloaded and checked, its permissions to agree to.
nonisolated struct ExtensionPreview: Decodable, Hashable, Sendable, Identifiable {
    struct Manifest: Decodable, Hashable, Sendable {
        var id: String
        var name: String?
        var version: String?
        var description: String?
        var extensionPack: [ExtensionPackMember]?
        /// The command-line programs it installs (#4507): the Install command-line programs line lists them.
        var tools: [ExtensionToolSpec]?

        enum CodingKeys: String, CodingKey {
            case id, name, version, description, tools
            case extensionPack = "extension_pack"
        }
    }

    var previewID: String
    var manifest: Manifest
    var permissions: [ExtensionPermission]
    var permissionsDigest: String
    var warnings: [String]?
    var expiresAt: Date?
    var upgrade: Bool?
    /// The version it upgrades from.
    var from: String?
    var morePermissions: Bool?
    /// What the new version may do that the installed one may not.
    var addedPermissions: [ExtensionPermission]?
    var market: String?
    var depends: [ExtensionDependencyStatus]?
    var bundle: [ExtensionPreview]?
    var unresolved: [String]?
    /// The installed extension this upgrades under the manifest's new ID (its `warnings` say so).
    var renameFrom: String?

    var canConfirm: Bool { confirmationProblem(at: .now) == nil }
    /// Whether this previews installed extension `id`'s new version, under the same ID or a new one (#2511).
    func upgrades(_ id: String) -> Bool { manifest.id == id || renameFrom == id }
    var expirationBoundary: Date? { ([self] + (bundle ?? [])).compactMap(\.expiresAt).min() }

    func confirmationProblem(at now: Date) -> String? {
        let all = [self] + (bundle ?? [])
        if all.contains(where: { $0.expiresAt.map { $0 <= now } == true }) {
            return String(localized: "The preview expired. Close it and preview again.")
        }
        if all.contains(where: { $0.unresolved?.isEmpty == false }) {
            return String(localized: "Some dependencies are unresolved. Add the markets that provide them, then preview again.")
        }
        let bundled = Set((bundle ?? []).map { $0.manifest.id })
        if !all.allSatisfy({ preview in
            (preview.depends ?? []).allSatisfy { dependency in
                dependency.state == "ok" || (["missing", "old"].contains(dependency.state) && bundled.contains(dependency.id))
            }
        }) {
            return String(localized: "Dependencies aren't ready. Install, turn on, or update those extensions, then preview again.")
        }
        return nil
    }

    var id: String { previewID }

    enum CodingKeys: String, CodingKey {
        case manifest, permissions, warnings, upgrade, from, market, depends, bundle, unresolved
        case previewID = "preview_id", permissionsDigest = "permissions_digest", expiresAt = "expires_at"
        case morePermissions = "more_permissions", addedPermissions = "added_permissions", renameFrom = "rename_from"
    }
}

extension APIClient {
    func pluginMarkets() async throws -> [ExtensionMarket] {
        struct List: Decodable { var markets: [ExtensionMarket]? }
        let list: List = try await get(extensionPath("/-/markets"))
        return list.markets ?? []
    }

    func addExtensionMarket(spec: String, token: String?) async throws -> ExtensionMarket {
        struct Body: Encodable { var spec: String; var token: String? }
        return try JSON.decoder.decode(ExtensionMarket.self, from: try await request("POST", extensionPath("/-/markets"),
                                                                                   body: Body(spec: spec, token: token), timeout: 60))
    }

    func deleteExtensionMarket(_ id: String) async throws {
        _ = try await request("DELETE", extensionPath("/-/markets/\(id)"))
    }

    func refreshExtensionMarket(_ id: String) async throws -> ExtensionMarket {
        try JSON.decoder.decode(ExtensionMarket.self, from: try await request("POST", extensionPath("/-/markets/\(id)/refresh"), timeout: 60))
    }

    func setExtensionMarket(_ id: String, token: String) async throws -> ExtensionMarket {
        try JSON.decoder.decode(ExtensionMarket.self, from: try await request("PUT", extensionPath("/-/markets/\(id)/token"),
                                                                            body: ["token": token], timeout: 60))
    }

    func pluginCatalog(query: String = "", market: String? = nil, refresh: Bool = false) async throws -> ExtensionCatalog {
        try await get(extensionPath("/-/catalog"), query: ["q": query, "market": market ?? "", "refresh": refresh ? "1" : ""])
    }

    func previewCatalogExtension(market: String, plugin: String) async throws -> ExtensionPreview {
        try JSON.decoder.decode(ExtensionPreview.self, from: try await request("POST", extensionPath("/-/catalog/preview"),
                                                                             body: ["market": market, extensionsAPI ? "extension" : "plugin": plugin], timeout: 120))
    }

    func installExtension(preview: ExtensionPreview) async throws -> HubExtensionDetail {
        try JSON.decoder.decode(HubExtensionDetail.self, from: try await request(
            "POST", extensionPath("/-/install"), body: ExtensionInstallRequest(preview: preview), timeout: 120))
    }

    func upgradeExtension(_ id: String, preview: ExtensionPreview) async throws -> HubExtensionDetail {
        try JSON.decoder.decode(HubExtensionDetail.self, from: try await request(
            "POST", extensionPath("/\(id)/-/upgrade"), body: ExtensionInstallRequest(preview: preview), timeout: 120))
    }

    @discardableResult func uninstallExtension(_ id: String, cascade: Bool = false, members: Bool = false) async throws -> ExtensionRemovalResult {
        try JSON.decoder.decode(ExtensionRemovalResult.self, from: try await request("DELETE", extensionPath("/\(id)/-/"),
                              query: ["cascade": cascade ? "1" : "", "members": members ? "1" : ""], timeout: 60))
    }
}

/// How a catalog plugin stands on this hub, in words and as the one thing to do.
enum ExtensionMarketText {
    enum Step: Equatable { case install, upgrade, upToDate, unavailable }

    static func step(_ p: ExtensionCatalogItem) -> Step {
        if p.problem?.isEmpty == false { return p.installed == nil ? .unavailable : .upToDate }
        if p.installed == nil { return .install }
        return p.upgradable == true ? .upgrade : .upToDate
    }

    /// The row's word on it: Available / Update to 1.1.0 / Installed 1.2.0 / Can't Install.
    static func state(_ p: ExtensionCatalogItem) -> String {
        switch step(p) {
        case .install: String(localized: "Available [Extension Market Model 06c13d]")
        case .upgrade: String(localized: "Update Available · \(p.installed ?? "") → \(p.version)")
        case .upToDate: String(localized: "Installed · \(p.installed ?? p.version)")
        case .unavailable: String(localized: "Can't Install")
        }
    }

    /// Under a GitHub market's error when it has no token: why a token helps a public repository too.
    static let gitHubTokenHint = String(localized: "Without a token, GitHub allows only 60 reads an hour, shared by every device on your network. A GitHub token raises that to 5,000, public repos included (a fine-grained token with Public repositories and no permissions is enough).")

    /// GitHub's page for a new fine-grained token, filled in for a market (docs/extensions/extensions.md「GitHub rate limits」): a name, what
    /// it is for, a year. No permission: such a token always reads public repositories, which is what lifts the limit.
    static let gitHubTokenURL: URL = {
        var c = URLComponents(string: "https://github.com/settings/personal-access-tokens/new")!
        c.queryItems = [URLQueryItem(name: "name", value: "Persona hub extension markets"),
                        URLQueryItem(name: "description", value: "Lets a Persona hub read its GitHub extension markets (read-only)."),
                        URLQueryItem(name: "expires_in", value: "365")]
        return c.url!
    }()

    /// Under a market's delete confirmation: what removing it does to the extensions installed from it.
    static func removalReason(_ count: Int?) -> String {
        guard let count else {
            return String(localized: "The hub stops reading this market. Extensions installed from it stay, but stop upgrading until the market is added again with the same address.")
        }
        if count == 0 { return String(localized: "The hub stops reading this market. No installed extension came from it.") }
        return String(localized: "The hub stops reading this market. \(count) extensions installed from it stay, but stop upgrading until the market is added again with the same address.")
    }

    /// A market's line: how many plugins, when it was read.
    static func marketSummary(_ m: ExtensionMarket) -> String {
        var parts = [String(localized: "\(m.plugins ?? 0) extensions")]
        if m.hasToken { parts.append(String(localized: "Has token")) }
        if let t = m.fetchedAt { let when = Fmt.relative(t); parts.append(String(localized: "Read \(when)", comment: "When a market's index was read; the time is relative, e.g. 5 minutes ago")) }
        return parts.joined(separator: " · ")
    }
}

/// Extension Market: the catalog (searched, filtered by market) and the markets (added, refreshed, given a token, removed).
@Observable final class ExtensionMarketModel {
    struct Deps {
        var api: () -> APIClient?
        /// The app's own (its installs go through `ExtensionOperations.shared`); a test's has a store of its own.
        var live = false
        /// The current roster resolves the default manager; without it, explicit ownership stamps are not guessed.
        var roster: () -> ManagerRoster? = { nil }
        static var live: Deps {
            .init(api: { AppModel.shared.api }, live: true, roster: { AppModel.shared.managerRoster })
        }
    }

    private(set) var items: [ExtensionCatalogItem]?
    private(set) var markets: [ExtensionMarket] = []
    private(set) var platform: String?
    private(set) var loading = false
    private(set) var error: String?
    /// The words searched for (the hub matches id, name, description, publisher and tags).
    var query = ""
    /// The market the catalog is filtered to (nil: all).
    var market: String?
    /// A market being refreshed or removed (its spinner), by id.
    private(set) var busyMarket: String?
    /// The removal confirmation's market and affected count (nil count: still reading, unavailable, or an older hub).
    private(set) var removalMarket: String?
    private(set) var removalCount: Int?
    @ObservationIgnored private var removalRequest = 0
    /// Whether plugins from the markets upgrade themselves (nil: a hub without auto-upgrade, or not read yet).
    private(set) var autoUpgrade: Bool?
    @ObservationIgnored private let deps: Deps

    init(deps: Deps = .live) { self.deps = deps }

    /// Reads the catalog for the query and the market chosen (`refresh`: every index read again first).
    func load(refresh: Bool = false) async {
        loading = true
        defer { loading = false }
        do {
            let c = try await api().pluginCatalog(query: query.trimmingCharacters(in: .whitespaces), market: market, refresh: refresh)
            items = c.plugins
            markets = c.markets
            platform = c.platform
            if let m = market, !c.markets.contains(where: { $0.id == m }) { market = nil }
            error = nil
        } catch {
            self.error = error.localizedDescription
        }
    }

    /// Just the markets (their page).
    func loadMarkets() async {
        loading = true
        defer { loading = false }
        do {
            markets = try await api().pluginMarkets()
            error = nil
        } catch {
            self.error = error.localizedDescription
        }
    }

    /// The auto-upgrade switch; a hub from before it has none (the switch stays hidden).
    func loadAutoUpgrade() async {
        autoUpgrade = try? await api().pluginUpgrades().auto
    }

    func setAutoUpgrade(_ on: Bool) async {
        let before = autoUpgrade
        autoUpgrade = on
        do {
            autoUpgrade = try await api().setExtensionUpgrades(auto: on).auto
            error = nil
        } catch {
            autoUpgrade = before
            self.error = error.localizedDescription
        }
    }

    /// Adds a market: the hub reads its index first and refuses one it cannot read (the reason comes back). nil: added.
    func addMarket(spec: String, token: String) async -> String? {
        let spec = spec.trimmingCharacters(in: .whitespacesAndNewlines)
        let token = token.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !spec.isEmpty else { return String(localized: "Enter the market's address") }
        do {
            let m = try await api().addExtensionMarket(spec: spec, token: token.isEmpty ? nil : token)
            markets.removeAll { $0.id == m.id }
            markets.append(m)
            return nil
        } catch {
            return error.localizedDescription
        }
    }

    /// Reads the default manager's installed extensions for the confirmation. A failed or ambiguous read stays unknown.
    /// False when a newer market's preparation superseded this one, so its stale confirmation does not open.
    @discardableResult
    func prepareRemoval(of id: String) async -> Bool {
        removalRequest += 1
        let request = removalRequest
        removalMarket = id
        removalCount = nil
        let count: Int?
        do {
            let listed = try await api().plugins()
            let own: [HubExtension]?
            if let roster = deps.roster() {
                own = listed.filter { roster.owner($0.manager) == roster.defaultID }
            } else {
                // An explicit manager stamp needs the roster: the built-in assistant is not necessarily the default.
                let ownershipUnknown = listed.contains {
                    $0.manager != nil && ($0.market == id || ($0.autoUpgrade != nil && $0.market == nil))
                }
                own = ownershipUnknown ? nil : listed.filter { $0.manager == nil }
            }
            if let own {
                // A hub from before `market` can report auto_upgrade without saying which market it came from.
                count = own.contains(where: { $0.autoUpgrade != nil && $0.market == nil }) ? nil : own.filter { $0.market == id }.count
            } else {
                count = nil
            }
        } catch {
            count = nil
        }
        guard request == removalRequest else { return false }
        removalCount = count
        return true
    }

    func removeMarket(_ id: String) async {
        await busy(id) {
            try await $0.deleteExtensionMarket(id)
            self.removalRequest += 1
            self.removalMarket = nil
            self.removalCount = nil
            self.markets.removeAll { $0.id == id }
            if self.market == id { self.market = nil }
        }
    }

    func refreshMarket(_ id: String) async {
        await busy(id) { self.replace(try await $0.refreshExtensionMarket(id)) }
    }

    /// A new token (empty: none); the hub reads the index again with it. nil: done.
    func setToken(_ id: String, token: String) async -> String? {
        do {
            replace(try await api().setExtensionMarket(id, token: token.trimmingCharacters(in: .whitespacesAndNewlines)))
            return nil
        } catch {
            return error.localizedDescription
        }
    }

    private func replace(_ m: ExtensionMarket) {
        if let i = markets.firstIndex(where: { $0.id == m.id }) { markets[i] = m } else { markets.append(m) }
    }

    private func busy(_ id: String, _ run: (APIClient) async throws -> Void) async {
        busyMarket = id
        defer { busyMarket = nil }
        do {
            try await run(try api())
            error = nil
        } catch {
            self.error = error.localizedDescription
        }
    }

    private func api() throws -> APIClient {
        guard let api = deps.api() else { throw APIError(status: 0, message: String(localized: "Not connected to the hub yet")) }
        return api
    }
}

/// Extension Market › one plugin: what it is and may do; installing or upgrading goes preview → agree → install, removing is
/// only for one installed from this market. With a hub that runs operations (#3307) the preview downloads and checks
/// in the background, the page showing its steps; the sheet opens when it waits for the user's yes and the same
/// operation installs. The operations are the app's (`ExtensionOperations`), so they stay when the page is left.
@Observable final class MarketExtensionModel {
    let market: String
    let pluginID: String
    private(set) var item: ExtensionCatalogItem?
    private(set) var loading = false
    private(set) var error: String?
    /// The preview waiting for the user's yes (the sheet with the permissions).
    var preview: ExtensionPreview?
    /// Asking for the preview (an older hub: downloading and checking it before answering).
    private(set) var previewing = false
    /// The install / upgrade / removal request is out and the hub has not said yet what it is doing.
    private(set) var starting = false
    private(set) var isPack = false
    /// An installed pack's members (its detail's `extension_pack`), for the uninstall confirmation to name.
    private(set) var members: [ExtensionPackMember] = []
    /// The installed extensions' names by id, read only for a pack (#4072, #4122): its members are named by them, a member
    /// not installed keeps its id. Names read earlier are kept, so members a partial uninstall removed keep theirs.
    private(set) var memberNames: [String: String] = [:]
    /// The last uninstall with members stopped part way (#4072): what went, what didn't and why, what wasn't tried.
    /// Its failed operation's card says so; Retry uninstalls again with the same choice.
    private(set) var partialRemoval: ExtensionPartialRemoval?
    private(set) var removingMembers = false
    private(set) var removalMessage = String(localized: "Uninstalled")
    /// The last uninstall went through with cleanup warnings: shown until its OK (acknowledgeRemoval), not as a toast.
    var removalNotice: ExtensionRemovalNotice?
    var uninstallConfirmation: ExtensionDisableConfirmation?
    /// The operation the open preview belongs to (a hub with operations): agreeing goes on with it, closing the
    /// sheet without agreeing cancels it.
    @ObservationIgnored private(set) var consentOf: String?
    /// The last removal's choices, for Retry.
    @ObservationIgnored private var lastRemoval = (cascade: false, members: false)
    @ObservationIgnored private let deps: ExtensionMarketModel.Deps
    @ObservationIgnored private let operations: ExtensionOperations

    init(market: String, id: String, deps: ExtensionMarketModel.Deps = .live, operations: ExtensionOperations? = nil) {
        self.market = market
        self.pluginID = id
        self.deps = deps
        self.operations = operations ?? (deps.live ? .shared : ExtensionOperations(deps: .init(api: deps.api, features: { [] }, hub: { nil })))
    }

    var step: ExtensionMarketText.Step? { item.map(ExtensionMarketText.step) }
    var canUninstall: Bool { item?.fromThisMarket == true && item?.installed != nil }
    /// The install, upgrade or removal going on, waiting for consent, or the one that failed (one that went
    /// through or was canceled is not shown).
    var operation: ExtensionOperation? {
        operations.operation(for: pluginID, market: market).flatMap { $0.state == .succeeded || $0.state == .canceled ? nil : $0 }
    }
    /// Previewing, installing, upgrading or removing.
    var working: Bool { previewing || starting || operation?.isActive == true }

    func load() async {
        loading = true
        defer { loading = false }
        do {
            let c = try await api().pluginCatalog(market: market)
            // A link from before a rename names the old ID; the market lists the extension under its new one (#2511).
            guard let it = c.plugins.first(where: { $0.pluginID == pluginID }) ?? c.plugins.first(where: { $0.replaces == pluginID }) else {
                throw APIError(status: 404, message: String(localized: "This market no longer has this extension"))
            }
            item = it
            if it.installed != nil, let detail = try? await api().plugin(pluginID) {
                isPack = detail.plugin.kind == "extension_pack"
                members = detail.extensionPack ?? []
            } else {
                isPack = false
                members = []
            }
            if !members.isEmpty { await nameMembers() }
            error = nil
        } catch {
            self.error = error.localizedDescription
        }
    }

    /// What the hub has going on (this page opening: an install started earlier shows its steps).
    func loadOperations() async { await operations.reload() }

    /// Reads the installed extensions' names for a pack's members; names read earlier stay (a member uninstalled since
    /// is still named). A list that can't be read leaves the ids.
    private func nameMembers() async {
        guard let list = try? await api().plugins() else { return }
        memberNames.merge(ExtensionPackMemberLine.names(list)) { _, new in new }
    }

    /// A pack preview's members named before its sheet opens, so the rows don't change under the user.
    private func nameMembers(of preview: ExtensionPreview) async {
        if preview.manifest.extensionPack?.isEmpty == false { await nameMembers() }
    }

    /// The uninstall confirmation's message: for a pack, its members by name and what each choice does with them.
    var uninstallMessage: String {
        guard isPack else { return String(localized: "Its settings and data are deleted too. You can install it again from the market later.") }
        guard !members.isEmpty else {
            return String(localized: "Uninstalling only the pack keeps its members. Uninstalling with members deletes their settings and data.")
        }
        let names = ExtensionText.list(members.map { memberNames[$0.id] ?? $0.id })
        return String(localized: "Members: \(names). Uninstalling only the pack keeps them. Uninstalling with members deletes their settings and data.")
    }

    /// The partial uninstall's lines for its failed operation's card (nil: none, or another operation).
    func partialLines(for op: ExtensionOperation) -> [String]? {
        guard op.kind == .uninstall, op.state == .failed, let partialRemoval else { return nil }
        return partialRemoval.lines(pack: item?.name ?? pluginID, names: memberNames)
    }

    /// Asks for the preview, for the permissions to agree to (the sheet opens with `preview`). A hub with operations
    /// downloads and checks in the background, the steps on this page, and the sheet opens when it waits for consent.
    func prepare() async {
        previewing = true
        defer { previewing = false }
        do {
            if operations.stepped {
                // The request answers at once with the operation; the button stops spinning, the steps take over.
                let op = try await operations.preview(market: market, extension: pluginID)
                if op.state == .needsConsent { try await review(op) }
            } else {
                let pv = try await api().previewCatalogExtension(market: market, plugin: pluginID)
                await nameMembers(of: pv)
                preview = pv
            }
            error = nil
        } catch {
            self.error = error.localizedDescription
        }
    }

    /// Opens the preview an operation waits on (Review Permissions on its card, or the page opened again).
    func review(_ op: ExtensionOperation) async throws {
        guard op.state == .needsConsent, let pv = try await operations.consentPreview(op.id) else { return }
        await nameMembers(of: pv)
        consentOf = op.id
        preview = pv
    }

    /// Installs or upgrades with the preview agreed to; the sheet closes at once and the steps show on the page.
    /// `agreed`: the preview the sheet showed (the sheet closing clears `preview` before this runs).
    /// true: done (the catalog read again); false: it failed (the page's card says where) or could not start.
    /// `consent`: the operation it goes on with (`takeConsent()`, taken before the sheet closed); nil: the page's.
    func confirm(_ agreed: ExtensionPreview? = nil, consent: String? = nil) async -> Bool {
        guard let pv = agreed ?? preview, pv.canConfirm else { return false }
        let of = consent ?? takeConsent()
        preview = nil
        starting = true
        defer { starting = false }
        let op: ExtensionOperation
        if of != nil {
            do {
                op = try await operations.agree(pluginID, preview: pv)
            } catch {
                self.error = error.localizedDescription
                return false
            }
        } else {
            op = await operations.installWithoutSteps(pluginID, name: item?.name ?? pv.manifest.name, preview: pv)
        }
        guard op.state == .succeeded else { return false }
        error = nil
        await load()
        return true
    }

    /// The operation the open preview belongs to, taken: the sheet closing after this does not cancel it.
    func takeConsent() -> String? {
        defer { consentOf = nil }
        return consentOf
    }

    /// The sheet closed without Install: the operation waiting on it is canceled (the hub drops the preview).
    func declined() {
        guard let id = consentOf else { return }
        consentOf = nil
        Task { await operations.dismiss(id) }
    }

    /// The user read the cleanup warnings.
    func acknowledgeRemoval() { removalNotice = nil }

    #if DEBUG
    /// `-showRemovalWarnings YES`: an uninstall's cleanup warnings up, for screenshots.
    func showSampleRemovalNotice() {
        removalNotice = ExtensionRemovalNotice(title: String(localized: "已卸载"), warnings: [
            "请手动删除 /Users/example/.persona/extensions/example/data/" + String(repeating: "cache/", count: 12) + "index.db", // i18n-ignore
            "请手动删除 /Users/example/.persona/extensions/example/package", // i18n-ignore
            "清理 /tmp/example-extension 失败：permission denied", // i18n-ignore
        ])
    }
    #endif

    func uninstall(cascade: Bool = false, members: Bool = false) async -> Bool {
        guard canUninstall, !working else { return false }
        removingMembers = members
        lastRemoval = (cascade, members)
        partialRemoval = nil
        starting = true
        defer { starting = false }
        do {
            let result = try await operations.uninstall(pluginID, name: item?.name, cascade: cascade, members: members)
            removalMessage = members
                ? result.removedMembers.map { String(localized: "Pack uninstalled, along with \($0.count) members") } ?? String(localized: "Pack uninstalled; no result came back for its members")
                : String(localized: "Uninstalled")
            // Removed, with files left for the user (#4466): it counts as uninstalled, the warnings stay up.
            let warnings = result.cleanupWarnings
            removalNotice = warnings.isEmpty ? nil : ExtensionRemovalNotice(title: removalMessage, warnings: warnings)
            uninstallConfirmation = nil
            error = nil
            await load()
            return true
        } catch let e as APIError where !cascade && e.code == "has_dependents" {
            uninstallConfirmation = ExtensionDisableConfirmation(error: e)
            error = nil
            return false
        } catch {
            // The operation's card says where it stopped when there is one (its event can come after the answer:
            // read the hub's list first); else the error under the title.
            if operations.stepped, operation?.state != .failed { await operations.reload() }
            partialRemoval = ExtensionPartialRemoval(error: error)
            self.error = operation?.state == .failed ? nil : error.localizedDescription
            return false
        }
    }

    /// Retry on a failed operation: the hub starts it again when it can (the package is in its cache; the permissions
    /// already agreed to are not asked again); otherwise the same thing again from this page. true: done.
    func retry() async -> Bool {
        guard let op = operation, op.state == .failed else { return false }
        // A partial uninstall is tried again from here: only the synchronous answer lists the members again (#4072).
        let fromHere = op.kind == .uninstall && partialRemoval != nil
        if op.retryable, !op.isLocal, operations.stepped, !fromHere {
            starting = true
            defer { starting = false }
            do {
                let next = try await operations.retry(op)
                switch next.state {
                case .needsConsent:
                    try await review(next)
                    return false
                case .succeeded:
                    error = nil
                    await load()
                    return true
                default:
                    return false
                }
            } catch {
                self.error = error.localizedDescription
                return false
            }
        }
        await operations.dismiss(op.id)
        switch op.kind {
        case .uninstall:
            return await uninstall(cascade: lastRemoval.cascade, members: lastRemoval.members)
        case .install, .upgrade:
            await prepare()
            return false
        }
    }

    /// The card's Cancel (checking, waiting for consent) or Close (failed): the hub cancels or forgets it.
    func dismissOperation() async {
        guard let op = operation else { return }
        if op.id == consentOf { consentOf = nil }
        if op.kind == .uninstall { partialRemoval = nil }
        await operations.dismiss(op.id)
    }

    private func api() throws -> APIClient {
        guard let api = deps.api() else { throw APIError(status: 0, message: String(localized: "Not connected to the hub yet")) }
        return api
    }
}
