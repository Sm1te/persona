import Foundation
import Testing
@testable import Persona

/// What the phone gives the watch (#1185): each group's answer-only watch credential, never the phone's own in its
/// place (that one opens everything), and which groups have none yet so the watch can ask for a sync.
@MainActor struct WatchCredentialTests {
    private static func cfg(_ host: String, _ token: String) -> ServerConfig {
        ServerConfig(url: URL(string: "http://\(host).example.com")!, token: token)
    }

    private static let registry = HubRegistry(hubs: [
        PairedHub(id: "a", config: cfg("a", "phone-a"), watch: cfg("a", "watch-a")),
        PairedHub(id: "b", config: cfg("b", "phone-b"), group: "g2"), // the hub did not give a watch token
        PairedHub(id: "c", config: cfg("c", "phone-c"), group: "g2", watch: cfg("c", "watch-c")), // not the group's first
    ])

    @Test func onlyTheWatchsOwnCredentialGoesToTheWatch() {
        let (configs, missing) = Self.registry.watchCredentials()
        #expect(configs == ["": Self.cfg("a", "watch-a")])
        #expect(missing == ["g2"])
        let phone = Set(Self.registry.hubs.map(\.config.token))
        #expect(configs.values.allSatisfy { !phone.contains($0.token) })
    }

    @Test func aPhoneWithoutAnyWatchCredentialGivesTheWatchNone() {
        let r = HubRegistry(hubs: [PairedHub(id: "a", config: Self.cfg("a", "phone-a"))])
        let (configs, missing) = r.watchCredentials()
        #expect(configs.isEmpty)
        #expect(missing == [""])
    }

    @Test func theModelsWatchConfigsSayWhatIsMissing() {
        let model = AppModel()
        model.registry = Self.registry
        let all = model.watchConfigs()
        #expect(all.configs == ["": Self.cfg("a", "watch-a")])
        #expect(all.missing == ["g2"])
        #expect(Set(all.names.keys) == ["", "g2"])
    }

    /// What `unpair` pushes once every hub is forgotten (#1870): no credential and nothing missing, so the watch drops the
    /// one it holds and shows "Not paired yet", not "No watch credential yet".
    @Test func aPhoneWithNoHubLeftTellsTheWatchItIsNotPaired() {
        let model = AppModel()
        model.registry = HubRegistry()
        let all = model.watchConfigs()
        #expect(all.configs.isEmpty)
        #expect(all.missing == [])
        #expect(all.names.isEmpty)
        #expect(all.deviceIDs == [:])
    }

    /// An older phone's message has no `missing`; a newer one's still reads on an older watch (unknown keys are skipped).
    @Test func theMessageReadsBothWays() throws {
        let old = try JSONDecoder().decode(WatchConfigs.self, from: Data(#"{"configs":{},"names":{}}"#.utf8))
        #expect(old.missing == nil)
        #expect(old.deviceIDs == nil)
        #expect(old.features == nil)
        let all = WatchConfigs(configs: [:], names: ["": "hub"], missing: [""])
        #expect(try JSONDecoder().decode(WatchConfigs.self, from: JSONEncoder().encode(all)) == all)
    }

    @Test func watchMetadataComesOnlyFromTheExportedHubsAuthenticatedHello() throws {
        let model = AppModel()
        model.registry = Self.registry
        model.buildLinks()
        #expect(model.watchConfigs().features?[""] == nil)
        let link = try #require(model.link(""))
        link.received(HubHello(features: ["extension_widgets", "extension_shortcuts"]))
        #expect(model.watchConfigs().features?[""] == ["extension_shortcuts", "extension_widgets"])
        link.fallback = Self.cfg("other", "fixture-other-phone")
        #expect(model.watchConfigs().features?[""] == ["extension_shortcuts", "extension_widgets"])
        link.received(HubHello(features: ["extension_shortcuts"]))
        #expect(model.watchConfigs().features?[""] == nil)
        link.fallback = try #require(Self.registry.primary("")).config
        link.received(HubHello(features: []))
        #expect(model.watchConfigs().features?[""] == [])
        #expect(model.watchConfigs().features?["g2"] == nil)
    }

    @Test func legacyWatchIdMigrationIssuesOnlyAnAnswerTokenAndFencesChanges() async throws {
        let old = try #require(Self.registry.primary(""))
        let full = FakeHub()
        full.on("POST", "/api/v1/auth/tokens", json: #"{"id":"watch-new","token":"fixture-watch-new","role":"answer"}"#)
        let token = try #require(try await DeviceCredentialTrade.issueWatchToken(for: old, api: full.client))
        let request = try #require(full.sent("POST", "/api/v1/auth/tokens").first?.body)
        #expect(try JSON.decoder.decode(JSONValue.self, from: request)["kind"] == .string("watch"))
        #expect(!full.requests.contains { $0.method == "GET" || $0.method == "DELETE" })
        let updated = try #require(DeviceCredentialTrade.watchRegistry(Self.registry, expected: old, token: token))
        #expect(updated.primary("")?.watch?.token == token.token)
        #expect(updated.primary("")?.watchDeviceID == token.id)
        #expect(updated.primary("")?.config == old.config)
        let migrated = try #require(updated.primary(""))
        #expect(try await DeviceCredentialTrade.issueWatchToken(for: migrated, api: full.client) == nil)
        #expect(full.sent("POST", "/api/v1/auth/tokens").count == 1)
        #expect(DeviceCredentialTrade.watchRegistry(updated, expected: old, token: token) == nil)
        var rotated = Self.registry
        rotated.hubs[0].config.token = "fixture-new-phone-token"
        #expect(DeviceCredentialTrade.watchRegistry(rotated, expected: old, token: token) == nil)
        var changedPrimary = Self.registry
        changedPrimary.hubs.insert(PairedHub(id: "new-primary", config: Self.cfg("new", "fixture-other")), at: 0)
        #expect(DeviceCredentialTrade.watchRegistry(changedPrimary, expected: old, token: token) == nil)
        #expect(DeviceCredentialTrade.watchRegistry(HubRegistry(), expected: old, token: token) == nil)
        let wrongRole = APIClient.NewToken(id: "wrong", token: "fixture-full", role: "full")
        #expect(DeviceCredentialTrade.watchRegistry(Self.registry, expected: old, token: wrongRole) == nil)
        full.on("POST", "/api/v1/auth/tokens", json: #"{"id":"wrong","token":"fixture-full","role":"full"}"#)
        do {
            _ = try await DeviceCredentialTrade.issueWatchToken(for: old, api: full.client)
            Issue.record("A non-answer token must not be exported to the watch")
        } catch { #expect((error as? URLError)?.code == .cannotParseResponse) }
    }

    /// Settings › Devices revoked the watch's credential (#1871): the hub that issued it forgets it, so the next sync
    /// issues a new one, and until then the group is missing (the watch drops the revoked one, Sync says it failed).
    @Test func aRevokedWatchCredentialIsForgottenAndIssuedAgain() async throws {
        var issued = Self.registry
        issued.hubs[0].watchDeviceID = "watch-a-id"
        issued.hubs[2].watchDeviceID = "watch-c-id"
        #expect(DeviceCredentialTrade.forgettingWatch(issued, id: "watch-a-id", group: "g2") == nil) // another group's
        #expect(DeviceCredentialTrade.forgettingWatch(issued, id: "phone-a-id", group: "") == nil) // not a watch's
        #expect(DeviceCredentialTrade.forgettingWatch(issued, id: "", group: "") == nil)
        let revoked = try #require(DeviceCredentialTrade.forgettingWatch(issued, id: "watch-a-id", group: ""))
        let a = try #require(revoked.primary(""))
        #expect(a.watch == nil && a.watchDeviceID == nil)
        #expect(a.config == issued.hubs[0].config)
        #expect(revoked.hubs[2] == issued.hubs[2]) // the other group's watch credential stays
        let (configs, missing) = revoked.watchCredentials()
        #expect(configs[""] == nil)
        #expect(missing.contains(""))
        let full = FakeHub()
        full.on("POST", "/api/v1/auth/tokens", json: #"{"id":"watch-a-new","token":"fixture-watch-a-new","role":"answer"}"#)
        #expect(try await DeviceCredentialTrade.issueWatchToken(for: try #require(issued.primary("")), api: full.client) == nil)
        let token = try #require(try await DeviceCredentialTrade.issueWatchToken(for: a, api: full.client))
        let reissued = try #require(DeviceCredentialTrade.watchRegistry(revoked, expected: a, token: token))
        #expect(reissued.primary("")?.watch?.token == "fixture-watch-a-new")
        #expect(reissued.primary("")?.watchDeviceID == "watch-a-new")
    }

    @Test func complicationExportContainsOnlyTheWatchsOwnGroupsAndIds() throws {
        let (configs, _) = Self.registry.watchCredentials()
        let all = WatchConfigs(configs: configs, names: ["": "Example", "g2": "Unavailable"],
                               deviceIDs: ["": "watch-device", "g2": "must-not-export"],
                               features: ["": ["extension_widgets"], "g2": ["extension_shortcuts"]])
        let exported = ExtensionSurfaceSync.export(all.configs, names: all.names, devices: all.deviceIDs ?? [:],
                                                  answerOnly: true, capabilities: all.features ?? [:])
        #expect(Set(exported.keys) == [""])
        #expect(exported[""]?.token == "watch-a")
        #expect(exported[""]?.deviceID == "watch-device")
        #expect(exported[""]?.answerOnly == true)
        #expect(exported[""]?.capabilities == ["extension_widgets"])
        let phoneTokens = Set(Self.registry.hubs.map(\.config.token))
        #expect(exported.values.allSatisfy { !phoneTokens.contains($0.token) })
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: root) }
        let store = ExtensionSurfaceStore(root: root)
        try store.replaceCredentials(exported)
        try store.replaceCredentials(ExtensionSurfaceSync.export([:], names: all.names, devices: all.deviceIDs ?? [:]))
        #expect(store.read().groups.isEmpty)
    }
}
