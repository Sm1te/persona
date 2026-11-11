import Foundation
import Observation

// Settings › Extensions (docs/extensions/extensions.md「The hub's API」): the hub's plugins, each one's switch, its configuration as a form drawn
// from its schema (`form.fields`), its secrets, write-only, and the pages it draws (`ui`, ExtensionUIModel). Installing,
// upgrading and removing are done on the hub's machine (`personad plugin`), or from a plugin market the hub has
// (ExtensionMarketModel: only what a market lists).

/// One of GET /api/v1/plugins.
nonisolated struct HubExtension: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var name: String
    var kind: String
    /// disabled | needs_setup | incompatible | stopped | starting | running | error (an older hub: starting | running |
    /// error | disabled).
    var status: String
    var error: String?
    var version: String?
    /// builtin | hub.json | installed (nil: a hub from before the plugin base).
    var source: String?
    var enabled: Bool?
    var description: String?
    /// The extension points it contributes to, with the ids.
    var contributes: [String: [String]]?
    /// What it still needs before it runs, in words.
    var needsSetup: [String]?
    var warnings: [String]?
    /// A plugin from a market (a hub with plugin_auto_upgrade): its own auto-upgrade switch, its newer version and what
    /// happened last with an upgrade (docs/extensions/extensions.md「Auto-upgrade」).
    var autoUpgrade: Bool?
    /// Why its own switch does not apply now: "market_removed" once its market is gone, or "disabled" while it is off.
    var autoUpgradePaused: String?
    var marketWasRemoved: Bool { autoUpgradePaused == "market_removed" }
    /// The market it upgrades from, retained after that market is removed (nil: a hub from before this field).
    var market: String?
    var upgrade: ExtensionUpgrade?
    var upgradeNote: ExtensionUpgradeNote?
    /// An extension with a program on computers: how that program is on each machine it is switched on for.
    var nodes: [ExtensionNodeState]?
    /// Its sign-in (a hub with extension_auth, #4507): the hub's last word on it.
    var auth: ExtensionAuth?
    /// The command-line programs this version runs with.
    var tools: [ExtensionToolUse]?
    /// Whose it is (docs/protocol/protocol.md "Per-manager routing"): the hub stamps every item of its merged list, and
    /// every manager lists its own built-ins, so one id can be there once per manager. nil: a hub from before managers.
    var manager: String?
    var attached: ExtensionAttached?
    var sandbox: ExtensionSandbox?

    enum CodingKeys: String, CodingKey {
        case id, name, kind, status, error, version, source, enabled, description, contributes, warnings, market, upgrade, nodes, auth, tools, manager, attached, sandbox
        case needsSetup = "needs_setup", autoUpgrade = "auto_upgrade", autoUpgradePaused = "auto_upgrade_paused", upgradeNote = "upgrade_note"
    }

    /// Its identity in a merged list: the id and the manager (the same built-in is listed under each).
    var listKey: String { manager.map { "\($0)/\(id)" } ?? id }
}

/// An independently running extension connected to the manager's sockets.
nonisolated struct ExtensionAttached: Codable, Hashable, Sendable {
    var online: Bool
    var since: Date?
    var error: String?
    var version: String?
}

/// The manager's sandbox state; egress preserves its counters without assuming their schema.
nonisolated struct ExtensionSandbox: Codable, Hashable, Sendable {
    var enabled: Bool
    var `default`: Bool
    var chosen: Bool
    var active: Bool
    var why: String?
    var detail: String?
    var trusted: Bool?
    var egress: [String: JSONValue]
}

/// An extension's program on one machine (docs/extensions/extensions.md「Programs running on computers」).
nonisolated struct ExtensionNodeState: Codable, Hashable, Sendable {
    var machineID: String
    var version: String?
    /// running | starting | stopped | error | missing | lease_expired | offline | outdated
    var state: String
    var error: String?

    enum CodingKeys: String, CodingKey {
        case version, state, error
        case machineID = "machine_id"
    }

    /// How it is, for the machine's row: what the user can make of it (nil: switched on, nothing heard yet).
    static func status(_ s: ExtensionNodeState?) -> String {
        guard let s else { return String(localized: "Waiting for the hub to send the program") }
        let error = s.error.map(exitText)
        switch s.state {
        case "running": return s.version.map { String(localized: "Running · \($0)") } ?? String(localized: "Running [Extensions Model 1f1cd2]")
        case "starting": return String(localized: "Starting [Extensions Model 0abbfb]")
        case "missing": return String(localized: "Sending the program")
        case "offline": return String(localized: "Offline: starts once connected")
        case "lease_expired": return String(localized: "Disconnected from the hub too long; stopped, and starts again once connected")
        case "outdated": return String(localized: "personad on this computer is too old; update it to run")
        case "error": return error.map { String(localized: "Program error: \($0)") } ?? String(localized: "Program error")
        default: return error.map { String(localized: "\(s.state): \($0)", comment: "A program state the app does not know, and its error") } ?? s.state
        }
    }

    /// Go's words for how a program ended (exec.ExitError), in the apps' words: exit status 3 → Exit code 3.
    static func exitText(_ error: String) -> String {
        error.replacing(#/\bexit status (-?\d+)/#) { m in let code = String(m.1); return String(localized: "Exit code \(code)") }
            .replacing(#/\bsignal: ([a-z ]+)/#) { m in let signal = String(m.1); return String(localized: "ended by signal (\(signal))") }
    }
}

/// A market plugin's newer version and where it stands (ExtensionView.upgrade, GET /api/v1/plugins/-/upgrades).
nonisolated struct ExtensionUpgrade: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var name: String?
    var market: String
    var from: String
    var to: String
    /// available | waiting_idle | needs_permission | off | skipped | failed | incompatible
    var state: String
    var addedPermissions: [ExtensionPermission]?
    var detail: String?
    /// "user" when a skipped version is one the user rolled back from (nil: it didn't start).
    var skippedBy: String?
    /// The new version's ID when it changes the extension's ID (`id` stays the installed one).
    var renameTo: String?

    enum CodingKeys: String, CodingKey {
        case id, name, market, from, to, state, detail
        case addedPermissions = "added_permissions"
        case skippedBy = "skipped_by"
        case renameTo = "rename_to"
    }
}

/// What happened last with a plugin's upgrade.
nonisolated struct ExtensionUpgradeNote: Codable, Hashable, Sendable {
    /// upgraded | rolled_back | needs_permission | failed
    var kind: String
    var from: String
    var to: String
    var detail: String?
    var at: Date
}

/// GET /api/v1/plugins/-/upgrades: the global switch and the newer versions.
nonisolated struct ExtensionUpgrades: Decodable, Hashable, Sendable {
    var auto: Bool
    var upgrades: [ExtensionUpgrade]
}

/// One field of a plugin's configuration form (`form.fields`): the hub's reading of the manifest's JSON Schema subset.
nonisolated struct ExtensionField: Codable, Hashable, Sendable, Identifiable {
    var name: String
    /// string | integer | number | boolean | array | object
    var type: String
    var title: String?
    var description: String?
    var `default`: JSONValue?
    var required: Bool?
    var `enum`: [ExtensionFormChoice]?
    var format: String?
    var pattern: String?
    var minLength: Int?
    var maxLength: Int?
    var minimum: Double?
    var maximum: Double?
    /// An array's items (strings, maybe from a list of choices).
    var items: ExtensionItems?
    var minItems: Int?
    var maxItems: Int?
    var uniqueItems: Bool?
    /// An object's fields (a group of the form).
    var fields: [ExtensionField]?
    /// What to ask before a switch of it is turned on (x-confirm; the plugin cua's 「扩展开着时不自动锁屏」).
    var confirm: String?
    /// format machine: only machines of these systems (x-os); nil, any.
    var os: [String]?

    var id: String { name }
    var label: String { title?.isEmpty == false ? title! : name }
    /// One of the hub's machines by id (format machine on a string): a picker of them (#2385).
    var picksMachine: Bool { type == "string" && format == "machine" && (self.enum ?? []).isEmpty }
    /// One of the hub's launchers by name (format launcher): a picker of them (#2385).
    var picksLauncher: Bool { type == "string" && format == "launcher" && (self.enum ?? []).isEmpty }

    enum CodingKeys: String, CodingKey {
        case name, type, title, description, `default`, required, `enum`, format, pattern, minimum, maximum, items, fields, confirm, os
        case minLength = "min_length", maxLength = "max_length", minItems = "min_items", maxItems = "max_items"
        case uniqueItems = "unique_items"
    }
}

/// One choice of a machine or launcher picker (#2385). `missing`: the value names none of them; it is kept, first, so it
/// shows and can be changed.
nonisolated struct ExtensionPickerChoice: Hashable, Sendable, Identifiable {
    var value: String
    var label: String
    var missing = false
    var id: String { value }

    /// The machines of the systems the field names (x-os), by name.
    static func machines(_ field: ExtensionField, current: String?, machines: [(id: String, name: String, os: String?)]) -> [Self] {
        let os = field.os ?? []
        let out = machines.filter { os.isEmpty || os.contains($0.os ?? "") }
            .map { Self(value: $0.id, label: $0.name.isEmpty ? $0.id : $0.name) }
        return keeping(current, in: out, missing: { String(localized: "\($0) (no such machine)") })
    }

    /// The launchers that are shown (not hidden), by their name, with the one they are started by beside a display
    /// name that differs.
    static func launchers(current: String?, launchers: [LauncherView]) -> [Self] {
        let out = launchers.filter { $0.hidden != true }
            .map { Self(value: $0.name, label: $0.title == $0.name ? $0.name : "\($0.title) (\($0.name))") }
        return keeping(current, in: out, missing: { String(localized: "\($0) (no such launcher)") })
    }

    private static func keeping(_ current: String?, in list: [Self], missing: (String) -> String) -> [Self] {
        guard let current, !current.isEmpty, !list.contains(where: { $0.value == current }) else { return list }
        return [Self(value: current, label: missing(current), missing: true)] + list
    }
}

/// An array field's items.
nonisolated struct ExtensionItems: Codable, Hashable, Sendable {
    var type: String
    var `enum`: [ExtensionFormChoice]?
    /// machine: the hub's machines by id, drawn as a switch each (`ExtensionMachineSwitches`).
    var format: String?
    /// For machine: only machines of these systems (darwin, linux, windows); nil, any.
    var os: [String]?
}

/// One value of a field's choices, with its label.
nonisolated struct ExtensionFormChoice: Codable, Hashable, Sendable, Identifiable {
    var value: JSONValue
    var title: String?
    var id: String { value.string ?? "\(value)" }
    var label: String { title?.isEmpty == false ? title! : (value.string ?? "") }
}

/// A declared secret and whether it has a value (never the value).
nonisolated struct ExtensionSecret: Codable, Hashable, Sendable, Identifiable {
    var name: String
    var title: String
    var description: String?
    var required: Bool?
    var set: Bool
    /// The value's last four characters, only when it is set and the extension asked for them (#5906): to tell which
    /// key it is without showing it.
    var hint: String?
    var id: String { name }
}

/// A line of what a plugin may do (`permissions`).
nonisolated struct ExtensionPermission: Codable, Hashable, Sendable, Identifiable {
    var key: String
    var title: String
    var detail: String?
    var id: String { key }
}

/// GET /api/v1/plugins/{id}/-/: the plugin with its form, settings, secrets and permissions.
nonisolated struct HubExtensionDetail: Decodable, Hashable, Sendable {
    var plugin: HubExtension
    var publisher: String?
    var homepage: String?
    var origin: String?
    var previousVersion: String?
    /// hub.json's: switched and set up there only.
    var readOnly: Bool
    var form: [ExtensionField]
    var config: [String: JSONValue]
    var secrets: [ExtensionSecret]
    var permissions: [ExtensionPermission]
    /// The pages it draws (placement plugin_page), a row each.
    var ui: [ExtensionUIPlace]
    /// The field (format machine) that picks the computers its program runs on; their rows say how it is there.
    var nodeMachines: String?
    var contributions: [ExtensionContributionSwitch]?
    var depends: [ExtensionDependencyStatus]?
    var extensionPack: [ExtensionPackMember]?

    private struct Form: Decodable { var fields: [ExtensionField]? }
    private enum CodingKeys: String, CodingKey {
        case publisher, homepage, origin, form, config, secrets, permissions, ui, contributions, depends
        case extensionPack = "extension_pack"
        case previousVersion = "previous_version", readOnly = "read_only", nodeMachines = "node_machines"
    }

    init(from decoder: Decoder) throws {
        plugin = try HubExtension(from: decoder)
        let c = try decoder.container(keyedBy: CodingKeys.self)
        publisher = try c.decodeIfPresent(String.self, forKey: .publisher)
        homepage = try c.decodeIfPresent(String.self, forKey: .homepage)
        origin = try c.decodeIfPresent(String.self, forKey: .origin)
        previousVersion = try c.decodeIfPresent(String.self, forKey: .previousVersion)
        readOnly = try c.decodeIfPresent(Bool.self, forKey: .readOnly) ?? false
        form = try c.decodeIfPresent(Form.self, forKey: .form)?.fields ?? []
        config = try c.decodeIfPresent([String: JSONValue].self, forKey: .config) ?? [:]
        secrets = try c.decodeIfPresent([ExtensionSecret].self, forKey: .secrets) ?? []
        permissions = try c.decodeIfPresent([ExtensionPermission].self, forKey: .permissions) ?? []
        ui = (try c.decodeIfPresent([ExtensionUIPlace].self, forKey: .ui) ?? []).filter(\.isExtensionPage)
        nodeMachines = try c.decodeIfPresent(String.self, forKey: .nodeMachines)
        contributions = try c.decodeIfPresent([ExtensionContributionSwitch].self, forKey: .contributions)
        depends = try c.decodeIfPresent([ExtensionDependencyStatus].self, forKey: .depends)
        extensionPack = try c.decodeIfPresent([ExtensionPackMember].self, forKey: .extensionPack)
    }
}

/// GET /api/v1/plugins: the plugins, and whether this hub has plugin markets (`markets`: Settings › Extensions › Extension Market).
nonisolated struct HubExtensionList: Decodable, Sendable {
    var plugins: [HubExtension]
    var canInstall: Bool
    var markets: Bool

    private enum CodingKeys: String, CodingKey {
        case plugins, extensions, markets
        case canInstall = "can_install"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        // `extensions` under /api/v1/extensions, `plugins` under the old path (#1844).
        plugins = try c.decodeIfPresent([HubExtension].self, forKey: .extensions)
            ?? c.decodeIfPresent([HubExtension].self, forKey: .plugins) ?? []
        canInstall = try c.decodeIfPresent(Bool.self, forKey: .canInstall) ?? false
        markets = try c.decodeIfPresent(Bool.self, forKey: .markets) ?? false
    }
}

extension APIClient {
    func plugins() async throws -> [HubExtension] { try await pluginList().plugins }

    func pluginList() async throws -> HubExtensionList { try await get(extensionPath("")) }

    func plugin(_ id: String) async throws -> HubExtensionDetail { try await get(extensionPath("/\(id)/-/")) }

    func setExtension(_ id: String, autoUpgrade: Bool) async throws -> HubExtensionDetail {
        try JSON.decoder.decode(HubExtensionDetail.self, from: try await request("PUT", extensionPath("/\(id)/-/auto-upgrade"), body: ["auto": autoUpgrade]))
    }

    func pluginUpgrades() async throws -> ExtensionUpgrades { try await get(extensionPath("/-/upgrades")) }

    func setExtensionUpgrades(auto: Bool) async throws -> ExtensionUpgrades {
        try JSON.decoder.decode(ExtensionUpgrades.self, from: try await request("PUT", extensionPath("/-/upgrades"), body: ["auto": auto]))
    }

    func setExtension(_ id: String, enabled: Bool, cascade: Bool = false) async throws -> HubExtensionDetail {
        var body = ["enabled": enabled]
        if cascade { body["cascade"] = true }
        return try JSON.decoder.decode(HubExtensionDetail.self, from: try await request("PUT", extensionPath("/\(id)/-/enabled"), body: body))
    }

    func setExtension(_ id: String, config: [String: JSONValue]) async throws -> HubExtensionDetail {
        struct Body: Encodable { var config: [String: JSONValue] }
        return try JSON.decoder.decode(HubExtensionDetail.self, from: try await request("PUT", extensionPath("/\(id)/-/config"), body: Body(config: config)))
    }

    func setExtension(_ id: String, secret name: String, value: String) async throws -> HubExtensionDetail {
        try JSON.decoder.decode(HubExtensionDetail.self, from: try await request("PUT", extensionPath("/\(id)/-/secrets/\(name)"), body: ["value": value]))
    }

    func clearExtension(_ id: String, secret name: String) async throws -> HubExtensionDetail {
        try JSON.decoder.decode(HubExtensionDetail.self, from: try await request("DELETE", extensionPath("/\(id)/-/secrets/\(name)")))
    }
}

/// What a plugin is called and says in the settings.
enum ExtensionText {
    /// Names in a row, with the language's list separator (「、」 in Chinese): a, b.
    static func list(_ items: [String]) -> String {
        items.joined(separator: String(localized: "、", comment: "Separator between list items"))
    }

    /// To the user a plugin is on or off (#1684, as the web and Android since #1670): an idle one starts by itself when
    /// it is used, so running and idle are both On; whether it runs right now is the detail page's small print
    /// (`runNote`). hub's `status` values are unchanged.
    static func state(_ p: HubExtension) -> String {
        switch p.status {
        case "running", "stopped": String(localized: "On [Herdr Notice 56410f]", comment: "Extension state: on")
        case "starting": String(localized: "Starting [Session Modes 087739]")
        case "needs_setup": String(localized: "Needs Setup")
        case "disabled": String(localized: "Off [Extensions Model 4e6fd0]", comment: "Extension state: off")
        case "incompatible": String(localized: "Not available on this hub")
        case "error": String(localized: "Something Went Wrong")
        default: String(localized: "Unknown [Connectors Views 93e474]")
        }
    }

    /// A secret's row: set (with its last four characters when the hub gives them, #5906), or not set (required).
    static func secretState(_ s: ExtensionSecret) -> String {
        guard s.set else { return s.required == true ? String(localized: "Not set (required)") : String(localized: "Not set") }
        guard let hint = s.hint, !hint.isEmpty else { return String(localized: "Set") }
        return String(localized: "Set · ends in \(hint)", comment: "A secret that is set, with its last four characters")
    }

    /// Whether an enabled plugin's program runs right now, under its description; nil for any other state.
    static func runNote(_ p: HubExtension) -> String? {
        switch p.status {
        case "running": String(localized: "Running [Extensions Model 9273b8]")
        case "stopped": String(localized: "Not running; starts when needed")
        default: nil
        }
    }

    /// Broken: the state's dot is red (`ExtensionStateDot`: yellow waiting on the user, green on, gray otherwise).
    static func isProblem(_ p: HubExtension) -> Bool { p.status == "error" || p.status == "incompatible" }

    static func source(_ s: String?) -> String? {
        switch s {
        case "builtin": String(localized: "Built-in")
        case "hub.json": "manager.json"
        case "installed": String(localized: "Installed [Extension Market Views a8b6c3]")
        default: nil
        }
    }

    static func point(_ name: String) -> String {
        switch name {
        case "connectors": String(localized: "Connectors")
        case "voice": String(localized: "Voice Chat")
        default: name
        }
    }

    /// The row's value: its state (the version is on its page).
    static func summary(_ p: HubExtension, auth: ExtensionAuth? = nil) -> String {
        let base = p.upgrade?.state == "needs_permission" ? String(localized: "\(state(p)) · Update needs review") : state(p)
        // Not signed in (#4507): said after the state, as the hub's warning says it to older apps.
        guard p.enabled ?? true, let suffix = ExtensionAuthText.listSuffix(auth ?? p.auth) else { return base }
        return base + " · " + suffix // i18n-ignore
    }

    /// A permission's title; the hub's lines for command-line programs (#4507) in this app's words.
    static func permissionTitle(_ p: ExtensionPermission) -> String {
        guard p.key == "tools" else { return p.title }
        let more = p.title.contains("更多") // i18n-ignore: the hub's title for an upgrade's added programs
        return more ? String(localized: "Install more command-line programs") : String(localized: "Install command-line programs")
    }

    /// Under the form: which switches take effect at once (lists of machines, `ExtensionModel.saveNow`) and that the rest
    /// waits for Save. Nil when there is only such switches: the fields say so themselves, nothing waits for Save.
    static func saveFooter(_ fields: [ExtensionField]) -> String? {
        let all = fields.flatMap { $0.type == "object" ? ($0.fields ?? []) : [$0] }
        let now = all.filter { $0.items?.format == "machine" }.map { String(localized: "「\($0.label)」", comment: "A field's name in quotes") }
        if now.isEmpty { return String(localized: "When you're done, tap Save at the end of Extension configuration; the extension restarts after saving. Empty fields use the extension's defaults.") }
        if now.count == all.count { return nil }
        let names = list(now)
        return String(localized: "Switches in \(names) take effect right away. For the rest, tap Save at the end of Extension configuration when you're done; the extension restarts after saving. Empty fields use the extension's defaults.")
    }

    /// The Update section's line for a newer version: what happens to it (docs/extensions/extensions.md「自动升级」).
    static func upgradeDetail(_ u: ExtensionUpgrade) -> String {
        let why = u.detail.flatMap { $0.isEmpty ? nil : $0 }
        switch u.state {
        case "needs_permission":
            // A rename is confirmed for the ID change, not for permissions: the hub's words, or ours for a version
            // Check for Updates found (#2511).
            if let renameTo = u.renameTo {
                return why ?? String(localized: "Changes the extension ID from \(u.id) to \(renameTo). Needs your confirmation; its settings and data stay.")
            }
            return morePermissions(u.addedPermissions ?? [])
        case "waiting_idle": return String(localized: "Updates automatically when the extension is idle")
        case "available": return String(localized: "Updates automatically later")
        case "off": return String(localized: "Auto-update is off")
        case "skipped" where u.skippedBy == "user":
            return String(localized: "You rolled back from this version to \(u.from), so it won't auto-update to it")
        case "skipped": return String(localized: "It didn't start after the last update, so it went back to \(u.from) and won't auto-update to this version")
        case "failed": return why.map { String(localized: "Didn't install: \($0)") } ?? String(localized: "Didn't install")
        case "incompatible": return why.map { String(localized: "Can't install on this hub: \($0)") } ?? String(localized: "Can't install on this hub")
        default: return ""
        }
    }

    /// The saved preference remains visible while upgrades are paused.
    static func autoUpgradeShown(_ p: HubExtension) -> Bool { p.autoUpgrade ?? true }

    /// Whether the line asks something of the user (its row opens the upgrade in the market).
    static func upgradeAction(_ u: ExtensionUpgrade) -> String? {
        switch u.state {
        case "needs_permission": return String(localized: "Review [Extensions Model 9fdfd7]")
        case "off", "skipped", "failed": return String(localized: "Update [Extensions Model 326f67]")
        default: return nil
        }
    }

    /// What a newer version asks for more, as the hub says it in its notice.
    static func morePermissions(_ added: [ExtensionPermission]) -> String {
        guard !added.isEmpty else { return String(localized: "Its permissions changed") }
        let lines = added.map { p in
            p.detail.flatMap { $0.isEmpty ? nil : $0 }.map { String(localized: "\(p.title) (\($0))", comment: "A permission and its detail") } ?? p.title
        }
        let all = list(lines)
        // One sentence per small count: the hub's notice says them with Chinese numerals (「要多一项权限」).
        return switch added.count {
        case 1: String(localized: "Needs 1 more permission: \(all)")
        case 2: String(localized: "Needs 2 more permissions: \(all)")
        case 3: String(localized: "Needs 3 more permissions: \(all)")
        case 4: String(localized: "Needs 4 more permissions: \(all)")
        case 5: String(localized: "Needs 5 more permissions: \(all)")
        default: String(localized: "Needs \(added.count) more permissions: \(all)")
        }
    }

    /// The last upgrade, when it is news of its own (asking and failing are the newer version's line).
    static func upgradeNote(_ n: ExtensionUpgradeNote) -> String? {
        switch n.kind {
        case "upgraded": return String(localized: "Auto-updated to \(n.to) (was \(n.from))")
        case "rolled_back": return String(localized: "\(n.to) didn't start; went back to \(n.from)")
        default: return nil
        }
    }

    /// Settings' row: how many, and how many need the user.
    static func listSummary(_ list: [HubExtension]?) -> String {
        guard let list else { return "" }
        if list.isEmpty { return String(localized: "None [V N C Pi P Diagnostics f12780]", comment: "No extensions") }
        let attention = list.filter { $0.status == "needs_setup" || isProblem($0) }.count
        return attention > 0 ? String(localized: "\(list.count) · \(attention) need you") : String(localized: "\(list.count) [Chats View cb60bd]")
    }
}

/// Settings › Extensions: the list, under each manager's name when the hub has several (docs/assistant/managers.md "Grouping").
@Observable final class ExtensionsModel {
    struct Deps {
        var api: () -> APIClient?
        var auth: ExtensionAuthStore = .shared
        static var live: Deps { .init(api: { AppModel.shared.api }) }
    }

    private(set) var plugins: [HubExtension]?
    /// The hub's managers, read with the list; nil from a hub without a roster.
    private(set) var roster: ManagerRoster?
    /// The hub has plugin markets: the list shows Extension Market.
    private(set) var markets = false
    private(set) var loading = false
    private(set) var error: String?
    @ObservationIgnored private let deps: Deps

    init(deps: Deps = .live) { self.deps = deps }

    /// The hub has no extensions to list (404: an older hub, or Demo Mode): the Mac says so instead of the error.
    private(set) var missing = false

    /// The list under each manager's name; nil with one manager (or none known): one list, as before.
    var groups: [ManagerGroup<HubExtension>]? { plugins.flatMap { list in roster?.groups(list) { $0.manager } } }

    /// The manager an extension's page and requests name: nil for the default's.
    func manager(of p: HubExtension) -> String? { roster?.named(p.manager) }

    func load() async {
        guard !loading else { return }
        loading = true
        defer { loading = false }
        do {
            guard let api = deps.api() else { throw APIError(status: 0, message: String(localized: "Not connected to the hub yet")) }
            let last = roster
            async let managers = ManagerRoster.read(api, keeping: last)
            let list = try await api.pluginList()
            roster = await managers
            plugins = list.plugins
            for p in list.plugins where p.auth != nil { deps.auth.received(p.id, p.auth, manager: manager(of: p)) }
            markets = list.markets
            error = nil
            missing = false
        } catch {
            self.error = error.localizedDescription
            missing = (error as? APIError)?.status == 404
        }
    }
}

/// What saving a secret came to: saved, stopped with the hub's words for the sheet to show (`ExtensionModel.refusal`),
/// or failed otherwise (the page's error says why).
nonisolated enum ExtensionSecretSave: Equatable, Sendable {
    case saved
    case refused(String)
    case failed
}

/// Settings › Extensions › one: its switch, its form (a draft saved as a whole), its secrets.
@Observable final class ExtensionModel {
    struct Deps {
        var api: () -> APIClient?
        var drafts: ExtensionDrafts = ExtensionDrafts()
        static var live: Deps { .init(api: { AppModel.shared.api }, drafts: .shared) }
    }

    let id: String
    /// Whose copy this page is (another manager's: every request names it); nil: the default manager's.
    let manager: String?
    /// That manager's name, for the page to say whose copy it is (nil on the default's page).
    private(set) var managerName: String?
    private(set) var detail: HubExtensionDetail?
    private(set) var loading = false
    private(set) var saving = false
    private(set) var error: String?
    private(set) var changingContribution: String?
    private(set) var changingEnabled = false
    private(set) var changingAutoUpgrade = false
    var disableConfirmation: ExtensionDisableConfirmation?
    /// A pack's members by the names they're installed under (#4122), read only for a pack; a member not installed keeps its id.
    private(set) var memberNames: [String: String] = [:]
    /// The hub's word on a field it refused, by its dotted path ("proxy.host").
    private(set) var fieldErrors: [String: String] = [:]
    /// The extension's own word on the settings it refused when they were saved (#5906), under the form; nothing was saved.
    private(set) var formError: String?
    /// The configuration being edited; saved as a whole (PUT replaces it).
    var draft: [String: JSONValue] = [:] { didSet { keepDraft() } }
    /// What number fields hold as typed, by path, until they read as a number.
    var numberText: [String: String] = [:] { didSet { keepDraft() } }
    /// The hub's launchers for the form's launcher pickers (format launcher), read once the form has one: nil before,
    /// or when they could not be read (the field is typed then).
    private(set) var launchers: [LauncherView]?
    private(set) var loadingLaunchers = false
    @ObservationIgnored private let deps: Deps
    @ObservationIgnored private var draftKey: ExtensionDrafts.Key?

    init(id: String, manager: String? = nil, deps: Deps = .live) {
        self.id = id
        self.manager = manager
        self.deps = deps
    }

    /// The client for this page's requests: its manager named on each.
    private var api: APIClient? { deps.api()?.manager(manager) }

    var dirty: Bool { (detail.map { draft != $0.config } ?? false) || !numberProblems.isEmpty }

    /// Number fields typed into that do not read as one, by path.
    var numberProblems: [String: String] {
        numberText.compactMapValues { text in
            text.trimmingCharacters(in: .whitespaces).isEmpty || Double(text.trimmingCharacters(in: .whitespaces)) != nil ? nil : String(localized: "Must be a number")
        }
    }

    var canSave: Bool { dirty && numberProblems.isEmpty && !saving && !changingEnabled && !changingAutoUpgrade && changingContribution == nil }

    func load() async {
        guard !loading else { return }
        loading = true
        defer { loading = false }
        if let api {
            let source = api.group.isEmpty ? "url:" + api.config.url.absoluteString : "group:" + api.group
            draftKey = .init(source: source, id: id, manager: manager)
        }
        let (whose, base) = (managerName == nil ? manager : nil, deps.api())
        async let name = ManagerRoster.name(of: whose, base)
        await run(preservingDraft: true) { api in try await api.plugin(self.id) }
        if detail?.extensionPack?.isEmpty == false, let list = try? await api?.plugins() {
            memberNames = ExtensionPackMemberLine.names(list)
        }
        if let name = await name { managerName = name }
    }

    /// Reads the hub's launchers when the form has a launcher field and they aren't in yet. GET /api/v1/launchers asks
    /// every machine (up to half a minute), so it runs apart from load. They are the hub's, not a manager's: asked of
    /// the default client.
    func loadLaunchersIfNeeded() async {
        guard launchers == nil, !loadingLaunchers, let form = detail?.form,
              form.contains(where: { $0.picksLauncher || ($0.fields ?? []).contains(where: \.picksLauncher) }),
              let api = deps.api() else { return }
        loadingLaunchers = true
        defer { loadingLaunchers = false }
        launchers = try? await api.allLaunchers()
    }

    func setEnabled(_ on: Bool, cascade: Bool = false) async {
        guard !changingEnabled, changingContribution == nil, !saving, detail?.readOnly == false else { return }
        changingEnabled = true
        defer { changingEnabled = false }
        do {
            guard let api else { throw APIError(status: 0, message: String(localized: "Not connected to the hub yet")) }
            detail = try await api.setExtension(id, enabled: on, cascade: cascade)
            error = nil
            disableConfirmation = nil
        } catch let e as APIError where !on && !cascade && e.code == "has_dependents" {
            disableConfirmation = ExtensionDisableConfirmation(error: e)
            error = nil
        } catch {
            self.error = error.localizedDescription
        }
    }

    func confirmDisable() async {
        disableConfirmation = nil
        await setEnabled(false, cascade: true)
    }

    func setContribution(_ contribution: ExtensionContributionSwitch, enabled: Bool) async {
        guard changingContribution == nil, !changingEnabled, !saving, contribution.canDisable, detail?.readOnly == false else { return }
        changingContribution = contribution.id
        defer { changingContribution = nil }
        do {
            guard let api else { throw APIError(status: 0, message: String(localized: "Not connected to the hub yet")) }
            detail = try await api.setExtensionContribution(id, point: contribution.point, id: contribution.contributionID, enabled: enabled)
            error = nil
        } catch {
            self.error = error.localizedDescription
        }
    }

    func setAutoUpgrade(_ on: Bool) async {
        guard !changingAutoUpgrade, !loading, !saving, !changingEnabled, changingContribution == nil,
              let detail, ExtensionSettingsPolicy(detail: detail).canAutoUpgrade else { return }
        changingAutoUpgrade = true
        defer { changingAutoUpgrade = false }
        await run(preservingDraft: true) { api in try await api.setExtension(self.id, autoUpgrade: on) }
    }

    func save() async -> Bool {
        guard canSave else { return false }
        saving = true
        defer { saving = false }
        fieldErrors = [:]
        formError = nil
        let ok = await run(refused: { self.formError = $0 }) { api in try await api.setExtension(self.id, config: Self.compact(self.draft)) }
        if ok { numberText = [:] }
        return ok
    }

    /// Saves one field at once (a machine's switch) on top of what the hub has, keeping the rest of what is being
    /// edited for Save.
    func saveNow(_ value: JSONValue?, at path: [String]) async {
        guard let saved = detail?.config else { return }
        let editing = draft
        set(value, at: path) // shows at once
        saving = true
        defer { saving = false }
        formError = nil
        let key = path.joined(separator: ".")
        let ok = await run(refused: { self.formError = $0 }) { api in
            try await api.setExtension(self.id, config: Self.compact(Self.setting(saved, path[...], value)))
        }
        // A retry that succeeds clears this field's refusal; errors on fields still being edited stay.
        if ok { fieldErrors.removeValue(forKey: key) }
        // run took the hub's answer as the draft: the other edits go back on top of it.
        draft = Self.setting(editing, path[...], ok ? self.value(path) : Self.value(in: saved, path))
    }

    /// Saves a secret. The value is only sent: never kept here, put in an error or logged.
    func setSecret(_ name: String, value: String) async -> ExtensionSecretSave {
        let value = value.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !value.isEmpty else { return .failed }
        var refusal: String?
        let ok = await run(preservingDraft: true, refused: { refusal = $0 }) { api in try await api.setExtension(self.id, secret: name, value: value) }
        return ok ? .saved : refusal.map(ExtensionSecretSave.refused) ?? .failed
    }

    func clearSecret(_ name: String) async {
        await run(preservingDraft: true) { api in try await api.clearExtension(self.id, secret: name) }
    }

    private func keepDraft() {
        guard let key = draftKey, detail != nil else { return }
        deps.drafts.values[key] = dirty ? .init(config: draft, numbers: numberText) : nil
    }

    /// Takes the new configuration on save; reloading and non-configuration actions keep the unsaved draft.
    /// `refused` takes the extension's own word when its check stopped the change (`refusal`); without it that is the
    /// page's error.
    @discardableResult private func run(preservingDraft: Bool = false, refused: ((String) -> Void)? = nil,
                                        _ call: @escaping (APIClient) async throws -> HubExtensionDetail) async -> Bool {
        do {
            guard let api else { throw APIError(status: 0, message: String(localized: "Not connected to the hub yet")) }
            let d = try await call(api)
            let editing = preservingDraft && detail != nil ? draft : nil
            let numbers = preservingDraft && detail != nil ? numberText : nil
            let kept = editing ?? (preservingDraft ? draftKey.flatMap { deps.drafts.values[$0]?.config } : nil)
            let keptNumbers = numbers ?? (preservingDraft ? draftKey.flatMap { deps.drafts.values[$0]?.numbers } : nil)
            detail = d
            draft = kept ?? d.config
            numberText = keptNumbers ?? [:]
            error = nil
            return true
        } catch let e as APIError where e.code == "invalid_config" {
            fieldErrors = [e.fields["field"] ?? "": e.fields["message"] ?? e.message]
            error = nil
            return false
        } catch {
            if let refused, let words = Self.refusal(error) {
                refused(words)
                self.error = nil
            } else {
                self.error = error.localizedDescription
            }
            return false
        }
    }

    /// The hub's words when a change of the settings or a secret was stopped before it was saved (#5906): the
    /// extension checked it and said no (check_failed, its own words), it was asked too often (check_rate_limited), or
    /// the settings changed meanwhile (settings_moved). nil for any other error.
    nonisolated static func refusal(_ error: Error) -> String? {
        guard let e = error as? APIError, let code = e.code,
              ["check_failed", "check_rate_limited", "settings_moved"].contains(code) else { return nil }
        let words = e.message.trimmingCharacters(in: .whitespacesAndNewlines)
        return words.isEmpty ? String(localized: "The extension didn't accept this change") : words
    }

    // MARK: the draft by path

    func value(_ path: [String]) -> JSONValue? { Self.value(in: draft, path) }

    static func value(in obj: [String: JSONValue], _ path: [String]) -> JSONValue? {
        var cur: JSONValue = .object(obj)
        for key in path {
            guard let next = cur[key] else { return nil }
            cur = next
        }
        return cur == .null ? nil : cur
    }

    /// Sets (nil: removes) the value at path; an emptied group goes too.
    func set(_ value: JSONValue?, at path: [String]) {
        draft = Self.setting(draft, path[...], value)
    }

    private static func setting(_ obj: [String: JSONValue], _ path: ArraySlice<String>, _ value: JSONValue?) -> [String: JSONValue] {
        guard let key = path.first else { return obj }
        var out = obj
        if path.count == 1 {
            out[key] = value
        } else {
            let inner = setting(obj[key]?.object ?? [:], path.dropFirst(), value)
            out[key] = inner.isEmpty ? nil : .object(inner)
        }
        return out
    }

    /// Without empty strings and lists the user cleared: "not set" (the schema's default applies).
    static func compact(_ obj: [String: JSONValue]) -> [String: JSONValue] {
        obj.compactMapValues { v in
            switch v {
            case .null: nil
            case .string(let s) where s.isEmpty: nil
            case .array(let a) where a.isEmpty: nil
            case .object(let o): compact(o).isEmpty ? nil : .object(compact(o))
            default: v
            }
        }
    }

    /// The error of a field (the hub's, or a number that does not read as one).
    func fieldError(_ path: [String]) -> String? {
        let key = path.joined(separator: ".")
        return fieldErrors[key] ?? numberProblems[key]
    }
}
