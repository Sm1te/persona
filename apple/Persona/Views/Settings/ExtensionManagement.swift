import Foundation

/// Dependency status in the details and the install preview; not the manifest's version constraint itself.
nonisolated struct ExtensionDependencyStatus: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var version: String?
    var state: String
    var installed: String?
    var name: String?

    var title: String { name?.isEmpty == false ? name! : id }
    var description: String {
        switch state {
        case "ok": return installed.map { String(localized: "Ready · \($0)") } ?? String(localized: "Ready [Extension Management ab27f8]")
        case "missing": return String(localized: "Install this extension first")
        case "disabled": return String(localized: "Turn on this extension first")
        case "old":
            let needed = version ?? String(localized: "a newer version"), current = installed ?? String(localized: "Unknown [Session Modes 4d8c1c]")
            return String(localized: "Needs \(needed), has \(current)")
        default: return String(localized: "Unknown [Connectors Views 93e474]")
        }
    }
}

/// Each contribution's effective switch; an older hub has no contributions field, and no fake switches are made up.
nonisolated struct ExtensionContributionSwitch: Codable, Hashable, Sendable, Identifiable {
    var point: String
    var contributionID: String
    var title: String?
    var enabled: Bool
    var canDisable: Bool
    var via: String?

    var id: String { point + "/" + contributionID }
    var label: String { title?.isEmpty == false ? title! : contributionID }

    enum CodingKeys: String, CodingKey {
        case point, title, enabled, via
        case contributionID = "id", canDisable = "can_disable"
    }
}

/// Before disabling, shows the affected extensions the server returned; cascade is sent only after confirming.
nonisolated struct ExtensionDisableConfirmation {
    var message: String
    var dependents: [String]

    init(error: APIError) {
        dependents = (error.details?["dependents"]?.array ?? []).compactMap { item in
            item["name"]?.string ?? item["id"]?.string
        }
        let names = dependents.joined(separator: "、")
        message = dependents.isEmpty ? error.message : String(localized: "These extensions depend on it and will be turned off too: \(names). They're only turned off, not uninstalled; their settings and data stay.")
    }
}

/// A pack's uninstall with its members that stopped part way (409 `partial_uninstall`, #4072): the pack is still
/// installed, the members already uninstalled stay uninstalled, nothing was put back, and it can be tried again. Only
/// the synchronous answer has the lists; an async uninstall's operation carries just the hub's sentence.
nonisolated struct ExtensionPartialRemoval: Equatable, Sendable {
    struct Failure: Equatable, Sendable {
        var id: String
        /// The hub's reason, in the request's language (may be empty).
        var error: String
    }

    /// Members uninstalled; they stay uninstalled.
    var removed: [String]
    /// Members the hub couldn't uninstall (or the pack itself, once every member went), each with its reason.
    var failed: [Failure]
    /// Members not tried and still installed. The hub's `remaining_members` is every member still installed, the failed
    /// ones included: those are said once, under `failed`.
    var remaining: [String]

    init(removed: [String], failed: [Failure], remaining: [String]) {
        self.removed = removed
        self.failed = failed
        let failedIDs = Set(failed.map(\.id))
        self.remaining = remaining.filter { !failedIDs.contains($0) }
    }

    /// nil for any other error.
    init?(error: Error) {
        guard let e = error as? APIError, e.code == "partial_uninstall", let body = e.details else { return nil }
        // Ids are strings; anything else in the lists is left out (`JSONValue.string` would make a number one).
        let text = { (v: JSONValue?) -> String? in if case .string(let s)? = v { s } else { nil } }
        let ids = { (key: String) in (body[key]?.array ?? []).compactMap { text($0) } }
        let failed = (body["failed_members"]?.array ?? []).compactMap { item in
            text(item["id"]).map { Failure(id: $0, error: text(item["error"]) ?? "") }
        }
        self.init(removed: ids("removed_members"), failed: failed, remaining: ids("remaining_members"))
    }

    static var title: String { String(localized: "Pack partly uninstalled") }

    /// What it means for `pack` (its name), then one line per group that has members, each member by its installed
    /// name in `names` (its id when it has none).
    func lines(pack: String, names: [String: String]) -> [String] {
        let separator = String(localized: "、", comment: "Separator between list items")
        let named = { (ids: [String]) in ids.map { names[$0] ?? $0 }.joined(separator: separator) }
        var out = [String(localized: "“\(pack)” is still installed. Members already uninstalled stay uninstalled, and nothing was put back. You can try again.")]
        if !removed.isEmpty { out.append(String(localized: "Uninstalled: \(named(removed))")) }
        if !failed.isEmpty {
            let items = failed.map { f in
                let name = names[f.id] ?? f.id
                return f.error.isEmpty ? name : String(localized: "\(name) (\(f.error))", comment: "A member's name and why it wasn't uninstalled")
            }
            out.append(String(localized: "Not uninstalled: \(items.joined(separator: separator))"))
        }
        if !remaining.isEmpty { out.append(String(localized: "Not tried, still installed: \(named(remaining))")) }
        return out
    }
}

/// A pack member's row (#4122): its installed name as the title, and under it the id beside the version and market;
/// one that isn't installed (no name) keeps its id as the title, with the version and market under it.
nonisolated struct ExtensionPackMemberLine: Equatable, Sendable {
    var title: String
    var detail: String?
}

nonisolated extension ExtensionPackMemberLine {
    init(_ member: ExtensionPackMember, name: String?) {
        let named = name.flatMap { $0.isEmpty || $0 == member.id ? nil : $0 }
        title = named ?? member.id
        let parts = [named == nil ? nil : member.id, member.version, member.market].compactMap { $0 }.filter { !$0.isEmpty }
        detail = parts.isEmpty ? nil : parts.joined(separator: " · ")
    }

    /// The installed extensions' names by id, for naming a pack's members (an empty name is left out).
    static func names(_ installed: [HubExtension]) -> [String: String] {
        Dictionary(installed.filter { !$0.name.isEmpty }.map { ($0.id, $0.name) }, uniquingKeysWith: { first, _ in first })
    }

    /// An install preview's: the installed names, and the ones installed with it by their own manifests (those win: a
    /// member being upgraded is named as its new version is).
    static func names(installed: [String: String], preview: ExtensionPreview) -> [String: String] {
        var out = installed
        for b in preview.bundle ?? [] { if let n = b.manifest.name, !n.isEmpty { out[b.manifest.id] = n } }
        return out
    }
}

nonisolated struct ExtensionRemovalResult: Decodable, Sendable {
    var removedMembers: [String]?
    /// What the hub could not clean up (#4466): the extension is removed, these are for the user to do by hand. nil
    /// from an older hub.
    var warnings: [String]?
    var cleanupWarnings: [String] { warnings ?? [] }
    enum CodingKeys: String, CodingKey { case removedMembers = "removed_members", warnings }
}

/// An uninstall that went through with cleanup left to do (#4819): every warning, shown until the user closes it.
nonisolated struct ExtensionRemovalNotice: Identifiable, Equatable, Sendable {
    var id = UUID()
    var title: String
    var warnings: [String]
}

/// One confirmation covers each preview's own permission summary; for an older hub that needs no bundle, the new field is not sent.
nonisolated struct ExtensionInstallRequest: Encodable {
    struct BundleReference: Encodable {
        var previewID: String
        var permissionsDigest: String
        enum CodingKeys: String, CodingKey {
            case previewID = "preview_id", permissionsDigest = "permissions_digest"
        }
    }
    var previewID: String
    var permissionsDigest: String
    var bundle: [BundleReference]?
    /// `async`: a hub with `extension_operations` answers at once with the operation and pushes its steps (#3307).
    var background: Bool?

    init(preview: ExtensionPreview, background: Bool = false) {
        self.background = background ? true : nil
        previewID = preview.previewID
        permissionsDigest = preview.permissionsDigest
        let refs = (preview.bundle ?? []).map { BundleReference(previewID: $0.previewID, permissionsDigest: $0.permissionsDigest) }
        bundle = refs.isEmpty ? nil : refs
    }

    enum CodingKeys: String, CodingKey {
        case previewID = "preview_id", permissionsDigest = "permissions_digest", bundle, background = "async"
    }
}

/// Unsaved forms live only in memory, kept apart per hub and extension; rebuilding the view or reloading does not drop the drafts.
@MainActor final class ExtensionDrafts {
    static let shared = ExtensionDrafts()
    /// An extension's draft by hub, id and manager (another manager's copy of the same id has its own).
    struct Key: Hashable { var source: String; var id: String; var manager: String? = nil }
    struct Draft { var config: [String: JSONValue]; var numbers: [String: String] }
    var values: [Key: Draft] = [:]
}

extension APIClient {
    func setExtensionContribution(_ ext: String, point: String, id: String, enabled: Bool) async throws -> HubExtensionDetail {
        try JSON.decoder.decode(HubExtensionDetail.self, from: try await request("PUT",
            extensionPath("/\(ext)/-/contributions/\(point)/\(id)"), body: ["enabled": enabled]))
    }
}
