import PersonaKit
import SwiftUI

// Settings › Connectors (2026-11-01): where Persona' data comes from and acts — this device's data (phone_* tools), the mail
// accounts, Lark (the hub's Lark gateway), and the Lark Bot the user talks to Persona through (FeishuBotView). Mail used to be managed from the inbox's ⋯ menu, this device's data was Phone Data.

/// What a connector is called and says in the settings list.
enum ConnectorText {
    /// This device as a connector: This iPhone / iPad / Mac.
    static var device: String {
        #if os(macOS)
        String(localized: "This Mac")
        #else
        UIDevice.current.userInterfaceIdiom == .pad ? String(localized: "This iPad") : String(localized: "This iPhone")
        #endif
    }

    static func deviceSummary() -> String {
        let on = PhoneDataSource.onThisDevice.filter(\.isEnabled).count
        return on == 0 ? String(localized: "Off [Connectors Views a0a4c7]") : String(localized: "\(on) on [Connectors Views 8df743]")
    }

    static func mailSummary(_ accounts: [MailAccount]?) -> String {
        guard let accounts else { return "" }
        if accounts.isEmpty { return String(localized: "None added") }
        if accounts.contains(where: { $0.status == "error" }) { return String(localized: "\(accounts.count) · Error") }
        return String(localized: "\(accounts.count) accounts")
    }

    static func larkSummary(_ st: LarkStatus?) -> String {
        guard let st else { return "" }
        if !st.configured { return String(localized: "Not set up") }
        return st.connected ? (st.name ?? String(localized: "Connected [V N C Page 5be032]")) : String(localized: "Can't connect")
    }

    /// A hub connector's state in a word or two (its row, its page). Only "error" is Something Went Wrong: a state this app does not
    /// know yet (a newer hub's) is not one.
    static func state(_ c: HubConnector) -> String {
        switch c.status {
        case "ok": String(localized: "Connected [V N C Page 5be032]")
        case "not_configured": String(localized: "Not set up")
        case "not_signed_in": String(localized: "Not signed in")
        case "no_permission": String(localized: "No permission")
        case "rate_limited": String(localized: "Too many requests; paused")
        case "disabled": String(localized: "Off [Git Hub Thread Models 6744b4]")
        case "error": String(localized: "Something Went Wrong")
        default: String(localized: "Unknown [Connectors Views 93e474]")
        }
    }

    /// The dot beside the state: red only for what is broken; switched off, not set up yet, unknown or still loading are gray.
    static func tone(_ c: HubConnector?) -> Color {
        switch c?.status {
        case "ok": Theme.positive
        case "rate_limited": Theme.caution
        case "error", "no_permission": Theme.danger
        default: .secondary
        }
    }

    static func summary(_ c: HubConnector) -> String {
        c.status == "ok" ? (c.account ?? state(c)) : state(c)
    }

    /// The symbol of a connector without a mark of its own.
    static func symbol(_ id: String) -> String {
        switch id {
        case "imessage": Theme.Icon.messagesApp
        case "wechat": Theme.Icon.conversation // a chat with a bot, not a feed
        default: Theme.Icon.connectors
        }
    }

    /// The connector's own mark (`Theme.Brand`), nil for one the app has none for.
    nonisolated static func brand(_ id: String) -> String? { profile(id).brand }

    /// What this app knows about a connector by its id; one not listed (any plugin's) gets the generic icon and the
    /// generic wording, built from its own name. The web keeps the same table (web/src/lib/connectors.ts `PROFILES`).
    nonisolated static func profile(_ id: String) -> ConnectorProfile {
        switch id {
        case "github": ConnectorProfile(brand: Theme.Brand.github)
        case "telegram": ConnectorProfile(brand: Theme.Brand.telegram, app: "Telegram", reads: String(localized: "its private chats and groups"),
                                          signInFooter: String(localized: "The API credentials and sign-in are stored encrypted on the hub; only the api_id is shown. Both are deleted when you sign out."),
                                          logoutEndsRemoteSession: true)
        default: ConnectorProfile()
        }
    }

    /// The sign-in step's footer: where the credentials live.
    nonisolated static func signInFooter(_ c: HubConnector) -> String {
        profile(c.id).signInFooter ?? String(localized: "Your sign-in is stored encrypted on the hub and deleted when you sign out.")
    }

    /// The sign-out confirmation. Only a connector known to end the session at the service says so; signing out of any
    /// other (GitHub's device token, a plugin's key) only has the hub forget its copy, and the authorization stays there.
    nonisolated static func logoutMessage(_ c: HubConnector) -> String {
        profile(c.id).logoutEndsRemoteSession
            ? String(localized: "The API credentials and sign-in saved on the hub will be deleted, and this device's session with \(c.name) will end too. You'll need to sign in again to use it.")
            : String(localized: "The credentials and sign-in saved on the hub will be deleted, and you'll need to sign in again to use it. Access granted in the other service isn't revoked automatically; revoke it there if needed.")
    }

    /// A conversation's message opened where it came from: Open in Telegram; a connector without an app of its own
    /// says its name.
    nonisolated static func openInTitle(_ id: String, name: String) -> String {
        let place = profile(id).app ?? name
        return tightCJK(String(localized: "Open in \(place) [Connectors Views 25f065]"))
    }

    /// Drops the spaces where a Chinese name meets Chinese text: 「在 GitHub 里打开」 keeps them, 「在示例里打开」 does not.
    nonisolated static func tightCJK(_ text: String) -> String {
        text.replacingOccurrences(of: #"(?<=\p{Han}) (?=\p{Han})"#, with: "", options: .regularExpression)
    }
}

/// See `ConnectorText.profile`. Every part is optional.
nonisolated struct ConnectorProfile: Equatable, Sendable {
    /// Its mark (`Theme.Brand`).
    var brand: String?
    /// The app its items are answered in, for Reply in Telegram and Open in Telegram.
    var app: String?
    /// What switching it on brings in, in the switch's footer; the default is "its content".
    var reads: String?
    var signInFooter: String?
    /// Signing out also ends the session at the service (Telegram's log-out call).
    var logoutEndsRemoteSession = false
}

/// GET /api/v1/lark/status.
nonisolated struct LarkStatus: Codable, Hashable, Sendable {
    var configured: Bool
    var connected: Bool
    var name: String?
    var error: String?
    var lastEventAt: Int64?

    enum CodingKeys: String, CodingKey {
        case configured, connected, name, error
        case lastEventAt = "last_event_at"
    }
}

/// One of GET /api/v1/connectors: a source the hub reads (GitHub…) and how it is doing (docs/protocol/protocol.md「Connector API」).
/// Feishu and mail are in it too when asked for (`all`): `ConnectorRow` draws them as the rows they always had.
nonisolated struct HubConnector: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var name: String
    var status: String
    var account: String?
    var via: String?
    var error: String?
    /// How to fix it, a step a sentence, when the user can.
    var guide: [String]?
    var lastSync: Date?
    var lastEvent: Date?
    var capabilities: [String]?
    var hosts: [String]?
    /// Its own settings (GitHub's: GitHubInboxOptions), as the hub gives them.
    var options: JSONValue?
    /// Switched on in Settings (nil: a hub from before the switch, always on).
    var enabled: Bool?
    /// Save to Knowledge and Show in Inbox (nil: it has no archive, no inbox, or the hub is from before these switches).
    var archive: Bool?
    var inbox: Bool?
    /// Whose it is (docs/protocol/protocol.md "Per-manager routing"): the hub stamps every item of its merged list, and
    /// one id can be there once per manager. nil: a hub from before managers.
    var manager: String?

    enum CodingKeys: String, CodingKey {
        case id, name, status, account, via, error, guide, capabilities, hosts, options, enabled, archive, inbox, manager
        case lastSync = "last_sync"
        case lastEvent = "last_event"
    }

    /// Its identity in a merged list: the id and the manager.
    var listKey: String { manager.map { "\($0)/\(id)" } ?? id }

    /// The user signs in to it here, a step at a time (Telegram), rather than on the hub's side.
    var signsInHere: Bool { capabilities?.contains("login") == true }

    /// It brings things into the knowledge base or the inbox (Telegram), so it has data to delete and its switch syncs;
    /// a chat bot (WeChat) only talks. Not described yet counts as bringing them, as before there were bots.
    var storesData: Bool {
        guard let caps = capabilities, !caps.isEmpty else { return true }
        return caps.contains("archive") || caps.contains("inbox")
    }

    /// A program the hub runs (Telegram) that has not said what it has: switched off since the hub started, or still
    /// starting. Its capabilities come from the program, so an empty list (nil is a hub from before them) does not
    /// mean it signs in on the hub's side.
    var undescribed: Bool { capabilities?.isEmpty == true }
}

/// Where a hub connector's things go besides the assistant's tools, each switched on its page.
nonisolated enum ConnectorFeed: String, Sendable {
    case archive, inbox
}

/// What deleting a connector's data removed (DELETE /api/v1/connectors/{id}/data, …/archive).
nonisolated struct ConnectorDataDeleted: Codable, Sendable {
    var chats: Int?
    var people: Int?
    var inbox: Int?
}

extension APIClient {
    func larkStatus() async throws -> LarkStatus {
        if let source = messagingSource {
            return try await messagingGet(source, "status") { raw in
                guard var o = raw as? [String: Any] else { return raw }
                if (o["signed_in"] as? Bool) == false, o["error"] == nil { o["error"] = String(localized: "Not signed in to Lark") }
                return o
            }
        }
        return try await get("/api/v1/lark/status")
    }

    func connectorLogin(_ id: String, step: String, values: [String: String]) async throws -> ConnectorPrompt {
        struct Body: Encodable { var step: String; var values: [String: String] }
        return try await post("/api/v1/connectors/\(id)/login", body: Body(step: step, values: values), timeout: 90)
    }

    func connectorLogout(_ id: String) async throws { _ = try await post("/api/v1/connectors/\(id)/logout", timeout: 60) }

    func setConnector(_ id: String, enabled: Bool) async throws -> HubConnector {
        try JSON.decoder.decode(HubConnector.self, from: try await request("PUT", "/api/v1/connectors/\(id)/settings", body: ["enabled": enabled]))
    }

    func deleteConnectorData(_ id: String) async throws -> ConnectorDataDeleted {
        try JSON.decoder.decode(ConnectorDataDeleted.self, from: try await request("DELETE", "/api/v1/connectors/\(id)/data"))
    }

    /// Turns Save to Knowledge (`ConnectorFeed.archive`) or Show in Inbox on or off.
    func setConnector(_ id: String, feed: ConnectorFeed, on: Bool) async throws -> HubConnector {
        try JSON.decoder.decode(HubConnector.self, from: try await request("PUT", "/api/v1/connectors/\(id)/settings", body: [feed.rawValue: on]))
    }

    /// Delete Saved Knowledge: the knowledge base's chats and people of it; Save to Knowledge is turned off with it.
    func deleteConnectorArchive(_ id: String) async throws -> ConnectorDataDeleted {
        try JSON.decoder.decode(ConnectorDataDeleted.self, from: try await request("DELETE", "/api/v1/connectors/\(id)/archive"))
    }

    /// The hub's connectors; `all` takes Feishu and mail too (a hub from before M6 leaves them out anyway).
    func connectors(all: Bool = false) async throws -> [HubConnector] {
        struct List: Decodable { var connectors: [HubConnector] }
        let list: List = try await get("/api/v1/connectors", query: all ? ["all": "1"] : [:])
        return list.connectors
    }
}

/// A row of Settings › Connectors after this device's, from the hub's list (GET /api/v1/connectors?all=1): mail and Feishu
/// first and drawn as they always were (their own pages, their own status), then the rest in the hub's order.
nonisolated enum ConnectorRow: Hashable, Identifiable {
    case mail, lark
    case hub(HubConnector)

    var id: String {
        switch self {
        case .mail: "mail"
        case .lark: "lark"
        case .hub(let c): c.listKey
        }
    }

    /// A hub that lists Feishu says whether it has mail; one that does not (from before M6, or no list yet) gets both
    /// rows, as before. Feishu's row (its page reads `/api/v1/lark/…`) is there only while the hub has Feishu chats
    /// (`LarkFeatures`, the Lark extension).
    static func rows(_ list: [HubConnector], lark: Bool = true) -> [ConnectorRow] {
        let listed = list.contains { $0.id == "lark" }
        let legacy: [ConnectorRow] = listed ? (list.contains { $0.id == "mail" } ? [.mail, .lark] : [.lark]) : [.mail, .lark]
        return legacy.filter { lark || $0 != .lark } + list.filter { $0.id != "mail" && $0.id != "lark" }.map(ConnectorRow.hub)
    }
}

/// An extension's page (Settings › Extensions › …) for the connectors it contributes (#3228, as the web's
/// `ExtensionConnectorInbox`): each one's Show in Inbox switch, the hub's own (PUT /api/v1/connectors/{id}/settings, the same
/// switch as on the connector's page, not a copy), and the way to that page for the rest. Messages instead has its own
/// Hand to Persona switch there. Another manager's extension contributes that manager's copies: every request names it.
@Observable final class ExtensionConnectorsModel {
    struct Deps {
        var api: () -> APIClient?
        var report: (Error) -> Void

        static var live: Deps {
            let model = AppModel.shared
            return Deps(api: { model.api }, report: { model.show($0) })
        }
    }

    /// The ones to draw, in the extension's order.
    private(set) var connectors: [HubConnector] = []
    /// The connector whose switch is being changed.
    private(set) var changing: String?
    /// Whose extension's (nil: the default manager's).
    let manager: String?
    @ObservationIgnored private let deps: Deps

    init(manager: String? = nil, deps: Deps = .live) {
        self.manager = manager
        self.deps = deps
    }

    /// The contributed connectors the hub lists with an inbox switch, in `ids`' order. Messages has its own Hand to
    /// Persona switch, so its page is linked without the generic inbox switch. A connector the hub does not list draws nothing.
    nonisolated static func shown(_ all: [HubConnector], ids: [String]) -> [HubConnector] {
        ids.compactMap { id in all.first { $0.id == id && ($0.inbox != nil || id == "imessage") } }
    }

    /// Unreadable (a hub without connectors answers 404), the sections stay away: the connector's page says what is
    /// wrong. Not gated on the hub's features: opened at launch, the page is there before they are.
    func load(ids: [String]) async {
        guard !ids.isEmpty, let api = deps.api()?.manager(manager) else { connectors = []; return }
        guard let all = try? await api.connectors() else { connectors = []; return }
        connectors = Self.shown(all, ids: ids)
    }

    func setInbox(_ id: String, on: Bool) async {
        guard changing == nil, let api = deps.api()?.manager(manager) else { return }
        changing = id
        defer { changing = nil }
        do {
            let updated = try await api.setConnector(id, feed: .inbox, on: on)
            if let i = connectors.firstIndex(where: { $0.id == id }) {
                // A hub that answers without the switch still switched it: kept as asked.
                connectors[i] = updated.inbox == nil ? { var c = updated; c.inbox = on; return c }() : updated
            }
        } catch {
            deps.report(error)
        }
    }
}

/// Settings › Connectors' list of the hub's connectors (GET /api/v1/connectors?all=1) and, when the hub has several managers,
/// each manager's under its name (docs/assistant/managers.md "Grouping"): one id can be there once per manager.
@Observable final class ConnectorsListModel {
    struct Deps {
        var api: () -> APIClient?
        static var live: Deps { .init(api: { AppModel.shared.api }) }
    }

    private(set) var connectors: [HubConnector] = []
    /// The hub's managers, read with the list; nil from a hub without a roster.
    private(set) var roster: ManagerRoster?
    @ObservationIgnored private let deps: Deps

    init(deps: Deps = .live) { self.deps = deps }

    /// The hub's connectors under each manager's name (mail and Lark keep their own rows); nil with one manager.
    var groups: [ManagerGroup<HubConnector>]? {
        roster?.groups(connectors.filter { $0.id != "mail" && $0.id != "lark" }) { $0.manager }
    }

    /// The Connectors section's own rows: mail and Lark, and the hub's connectors too unless they are grouped.
    func rows(lark: Bool) -> [ConnectorRow] {
        let all = ConnectorRow.rows(connectors, lark: lark)
        guard groups != nil else { return all }
        return all.filter { if case .hub = $0 { false } else { true } }
    }

    /// The manager a connector's page and requests name: nil for the default's.
    func manager(of c: HubConnector) -> String? { roster?.named(c.manager) }

    /// An older hub has no connectors (feature "connectors", `listed` false): its list stays as it was. Unreadable, it
    /// stays too.
    func load(listed: Bool) async {
        guard listed, let api = deps.api() else { return }
        let last = roster
        async let managers = ManagerRoster.read(api, keeping: last)
        guard let list = try? await api.connectors(all: true) else { return }
        roster = await managers
        connectors = list
    }
}

/// Settings › Connectors: the existing sources followed by the sent ledger, never a top-level Settings entry.
struct ConnectorsView: View {
    @Environment(AppModel.self) private var model

    var body: some View {
        SettingsForm {
            ConnectorsSection()
            if !model.isDemo {
                Section {
                    NavigationLink(value: SettingsPage.sent) {
                        SettingsRow(title: String(localized: "Records sent as you"), symbol: Theme.Icon.sentAsYou, tint: Theme.Hue.orange)
                            .lineLimit(1)
                    }
                    .accessibilityIdentifier("connectors.sent")
                } footer: {
                    Text("What Persona sent in your name, and why it was allowed")
                }
            }
        }
        .accessibilityIdentifier("connectors-list")
        .themedPage()
        .detailTitle(String(localized: "Connectors"))
    }
}

/// The Connectors section: one row per connector with what it has now; with several managers the hub's
/// connectors follow in a section per manager, under its name.
struct ConnectorsSection: View {
    @Environment(AppModel.self) private var model
    @State private var accounts: [MailAccount]?
    @State private var lark: LarkStatus?
    @State private var hub = ConnectorsListModel()
    @State private var bot: FeishuBotStatus?

    var body: some View {
        let groups = hub.groups
        Section {
            Group {
                #if os(iOS) || os(visionOS)
                // A Mac is a desktop, not the phone: the hub never calls its tools (AppModel.registration).
                NavigationLink(value: SettingsPage.phoneData) {
                    ConnectorRowLabel(title: ConnectorText.device, symbol: Theme.Icon.thisDevice, value: ConnectorText.deviceSummary())
                }
                .settingsListRow(SettingsPage.phoneData)
                #endif
                ForEach(hub.rows(lark: model.larkFeatures.chats)) { row in
                    switch row {
                    case .mail:
                        NavigationLink(value: SettingsPage.mail) {
                            ConnectorRowLabel(title: String(localized: "Mail [Mail Views 730752]"), symbol: Theme.Icon.mailAccounts, value: ConnectorText.mailSummary(accounts))
                        }
                        .settingsListRow(SettingsPage.mail)
                    case .lark:
                        NavigationLink(value: SettingsPage.feishu) {
                            ConnectorRowLabel(title: String(localized: "Lark [Inbox Event Views 7e4419]"), symbol: Theme.Icon.mailAccounts, brand: Theme.Brand.lark, value: ConnectorText.larkSummary(lark))
                        }
                        .settingsListRow(SettingsPage.feishu)
                    case .hub(let c):
                        hubRow(c)
                    }
                }
                NavigationLink(value: SettingsPage.feishuBot) {
                    ConnectorRowLabel(title: String(localized: "Lark Bot"), symbol: Theme.Icon.chatBot, value: FeishuBotModel.summary(bot))
                }
                .settingsListRow(SettingsPage.feishuBot)
            }
            .bareListRow()
        } header: {
            SettingsHeading(String(localized: "Connectors"))
        } footer: {
            if groups?.isEmpty ?? true { footer }
        }
        .task(id: model.settingsPath.count) { await load() } // again when a page closes: it may have changed them
        if let groups {
            ForEach(groups) { group in
                Section {
                    Group {
                        ForEach(group.items, id: \.listKey) { c in hubRow(c) }
                    }
                    .bareListRow()
                } header: {
                    ManagerHeading(manager: group.manager, count: group.items.count)
                } footer: {
                    if group.id == groups.last?.id { footer }
                }
            }
        }
    }

    /// A hub connector's row, opening its manager's copy.
    private func hubRow(_ c: HubConnector) -> some View {
        let page = SettingsPage.connector(c.id, manager: hub.manager(of: c))
        return NavigationLink(value: page) {
            ConnectorRowLabel(title: c.name, symbol: ConnectorText.symbol(c.id), brand: ConnectorText.brand(c.id), value: ConnectorText.summary(c))
        }
        .settingsListRow(page)
    }

    private var footer: some View {
        Text("Data sources Persona can read and act on for you: the assistant, AI twin and agents use them to check your calendar, read messages and send email. Anything done on your behalf asks you first.")
    }

    private func load() async {
        guard let api = model.api else { return }
        // An older hub has no connectors (feature "connectors"): its list stays as it was.
        let listed = model.hubFeatures.contains("connectors")
        async let a = try? api.mailAccounts()
        async let l = model.larkFeatures.chats ? try? api.larkStatus() : nil
        async let b = try? api.feishuBot()
        await hub.load(listed: listed)
        let (accs, st, bs) = await (a, l, b)
        if let accs { accounts = accs }
        if let st { lark = st }
        if let bs { bot = bs }
    }
}

/// One row of the Connectors section: the connector's mark (its brand's, or a symbol) and name, what it has now on the right.
private struct ConnectorRowLabel: View {
    var title: String
    var symbol: String
    var brand: String? = nil
    var value: String

    var body: some View {
        if AppStyle.current.usesBeautifulUI {
            SettingsRow(title: title, symbol: symbol, tint: Theme.accent, value: value, brand: brand)
        } else {
            LabeledContent {
                Text(value).lineLimit(1)
            } label: {
                Label {
                    Text(title)
                } icon: {
                    if let brand {
                        BrandMark(brand, side: Theme.Size.icon)
                    } else {
                        ThemedIcon(symbol)
                    }
                }
            }
        }
    }
}

/// Settings › Connectors › Lark: whether the hub reaches the Lark gateway, who is signed in, and what Persona does with it. The account
/// itself is signed in on the hub's machine (docs/extensions/feishu.md), not here.
struct FeishuConnectorView: View {
    @Environment(AppModel.self) private var model
    @Bindable private var larkHome = LarkHomePreference.shared
    @State private var status: LarkStatus?
    @State private var accounts: APIClient.LarkAccounts?
    @State private var loading = true
    @State private var failure: String?

    var body: some View {
        SettingsForm {
            Section {
                HStack(spacing: Theme.Space.m) {
                    BrandMark(Theme.Brand.lark, side: Theme.Size.wellM)
                    VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                        Text(status?.name ?? String(localized: "Lark [Inbox Event Views 7e4419]")).font(Theme.Font.heading).lineLimit(1)
                        HStack(spacing: Theme.Space.sm) {
                            Circle().fill(tint).scaledFrame(width: Theme.Size.dotS, height: Theme.Size.dotS)
                            Text(stateText).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(2)
                        }
                    }
                    Spacer(minLength: Theme.Space.s)
                    if loading { ProgressView() }
                }
                .padding(.vertical, Theme.Space.xs)
                if let at = status?.lastEventAt, at > 0 {
                    LabeledContent("Last Live Message") {
                        TimelineView(.periodic(from: .now, by: 30)) { ctx in
                            Text(Fmt.relative(Date(timeIntervalSince1970: TimeInterval(at)), now: ctx.date)).lineLimit(1)
                        }
                    }
                }
                if let e = status?.error ?? failure, !e.isEmpty {
                    Text(e).font(Theme.Font.label).foregroundStyle(Theme.danger).textSelection(.enabled)
                }
            } footer: {
                Text(status?.configured == false
                     ? String(localized: "The hub isn't connected to Lark yet: run the Lark gateway on the hub machine and add it under lark in the assistant's manager.json (docs/extensions/feishu.md).")
                     : String(localized: "The Lark account is signed in through the Lark gateway on the hub machine. Chats share this account with the Inbox, so there's no need to sign in again."))
            }

            if let accounts, accounts.accounts.count > 1 {
                Section {
                    ForEach(accounts.accounts) { account in
                        accountRow(account, isDefault: account.id == accounts.default)
                    }
                } header: {
                    Text("Accounts")
                } footer: {
                    Text("Every signed-in account goes into the Inbox, and you can switch accounts on the Messages page. /agent, the Assistant and Knowledge use the default account. Add or sign out of accounts in the Lark gateway.")
                }
            }

            Section {
                Toggle("Show Messages on Home", isOn: $larkHome.enabled)
                .accessibilityIdentifier("feishu-home-enabled")
            } footer: {
                #if os(macOS)
                Text("Adds Messages to the sidebar to browse all direct and group chats. It shares accounts and chat history with the Inbox.")
                #else
                Text("Adds a Messages entry on Home to browse all direct and group chats. It shares accounts and chat history with the Inbox.")
                #endif
            }

            Section {
                if let source = model.hub?.messagingSources.first, let page = source.settingsUi {
                    NavigationLink("Message Tweaks") { ExtensionPageView(plugin: source.`extension`, ui: page, title: String(localized: "Message Tweaks")) }
                } else if model.api?.messagingSource == nil {
                    NavigationLink("Message Tweaks") { LarkMessageDisplaySettingsView() }
                } else {
                    // A source is in use but its list could not be read yet: not the old page (its switches are the
                    // source's now), until the list comes.
                    Text("Message Tweaks").foregroundStyle(.secondary)
                }
            }

            Section("What Persona Does with Lark") {
                use(Theme.Icon.inbox, String(localized: "Inbox"), String(localized: "Direct messages, @mentions, group messages and mentions in docs; AI sorts them and drafts replies"))
                use(Theme.Icon.knowledge, String(localized: "Knowledge"), String(localized: "Chats, people, groups and projects are archived as context for the assistant and AI twin"))
                #if os(macOS)
                use(Theme.Icon.ai, String(localized: "Assistant"), String(localized: "Persona can search messages and read docs; sending messages and other changes ask you in the app first"))
                #else
                use(Theme.Icon.ai, String(localized: "Assistant"), String(localized: "Persona can search messages and read docs; sending messages and other changes ask you on your phone first"))
                #endif
                use(Theme.Icon.agents, "/agent", String(localized: "Start a message with /agent anywhere in Lark to have an agent handle it and reply for you"))
                ModelConfigurationLink(title: String(localized: "/agent Model"), purposeID: "lark.agent")
            }
        }
        .themedPage()
        .detailTitle(String(localized: "Lark [Inbox Event Views 7e4419]"))
        .navigationBarTitleDisplayMode(.inline)
        .task { await load() }
        .refreshable { await load() }
    }

    private var tint: Color {
        guard let st = status else { return .secondary }
        return st.connected ? Theme.positive : st.configured ? Theme.danger : .secondary
    }

    private var stateText: String {
        guard let st = status else { return loading ? String(localized: "Checking…") : String(localized: "Couldn't get status") }
        if !st.configured { return String(localized: "Not set up") }
        return st.connected ? String(localized: "Connected [V N C Page 5be032]") : String(localized: "Can't reach the Lark gateway")
    }

    private func accountRow(_ account: LarkAccount, isDefault: Bool) -> some View {
        HStack(spacing: Theme.Space.m) {
            LarkAvatarImage(name: account.title, url: account.avatar, size: Theme.Size.wellM)
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                Text(account.title).font(Theme.Font.body).lineLimit(1)
                Text(accountDetail(account)).font(Theme.Font.label).lineLimit(1)
                    .foregroundStyle(account.signedIn ? AnyShapeStyle(.secondary) : AnyShapeStyle(Theme.danger))
            }
            Spacer(minLength: Theme.Space.s)
            if isDefault { Chip(text: String(localized: "Default [Prompt Composer 844b8c]")) }
            if account.signedIn, account.unread > 0 { CountBadge(count: account.unread) }
        }
        .padding(.vertical, Theme.Space.xxs)
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("feishu-account-row-\(account.id)")
    }

    private func accountDetail(_ account: LarkAccount) -> String {
        if account.quarantined == true { return String(localized: "Can't open it. Retry or remove it in the Lark gateway.") }
        if !account.signedIn { return String(localized: "Not signed in") }
        return account.tenant?.isEmpty == false ? account.tenant! : String(localized: "Signed in")
    }

    private func use(_ symbol: String, _ title: String, _ detail: String) -> some View {
        Label {
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                Text(title)
                Text(detail).font(Theme.Font.label).foregroundStyle(.secondary)
            }
        } icon: {
            ThemedIcon(symbol).foregroundStyle(Theme.accent)
        }
        .padding(.vertical, Theme.Space.xxs)
    }

    private func load() async {
        loading = true
        defer { loading = false }
        async let list = try? await model.api?.larkAccounts() // a hub from before several accounts: none
        do {
            status = try await model.api?.larkStatus()
            failure = nil
        } catch {
            failure = error.shown.map { String(localized: "Couldn't get Lark's status: \($0)") }
        }
        accounts = await list
    }
}

/// Settings › Connectors › GitHub (any connector of the hub's, GET /api/v1/connectors): whether it is signed in, as whom, where
/// the sign-in comes from, what is wrong. One the user signs in to here (Telegram; GitHub with its device flow, or a
/// computer's gh login set up on the hub's side) also gets its switch, sign-in, sign-out and delete
/// (ConnectorSignInSections).
struct HubConnectorView: View {
    var id: String
    /// Another manager's copy (docs/assistant/managers.md "Grouping"): what the page reads and changes is that manager's,
    /// every request naming it. nil: the default manager's.
    var manager: String? = nil
    @Environment(AppModel.self) private var model
    @State private var connector: HubConnector?
    @State private var loading = true
    @State private var failure: String?
    /// The manager's name on another manager's copy, for the page to say whose it is.
    @State private var managerName: String?
    /// Its source subscriptions (#2041): the entry row's counts, nil while it is hidden.
    @State private var subscriptions: SubscriptionCounts?

    var body: some View {
        SettingsForm {
            // A connector that is only a channel's binding (WeChat's): the channel is where it is set up.
            if ChannelDirectory.shared.list.contains(where: { $0.id == id && $0.source == "plugin" }) {
                ChannelLinkSection(channelID: id)
            }
            Section {
                HStack(spacing: Theme.Space.m) {
                    mark
                    VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                        Text(connector?.account ?? title).font(Theme.Font.heading).lineLimit(1)
                        HStack(spacing: Theme.Space.sm) {
                            Circle().fill(tint).scaledFrame(width: Theme.Size.dotS, height: Theme.Size.dotS)
                            Text(stateText).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(2)
                        }
                    }
                    Spacer(minLength: Theme.Space.s)
                    if loading { ProgressView() }
                }
                .padding(.vertical, Theme.Space.xs)
                if let via = connector?.via, !via.isEmpty {
                    LabeledContent("Sign In") { Text(via).lineLimit(2).multilineTextAlignment(.trailing) }
                }
                if let at = connector?.lastSync {
                    LabeledContent("Last Sync") {
                        TimelineView(.periodic(from: .now, by: 30)) { ctx in
                            Text(Fmt.relative(at, now: ctx.date)).lineLimit(1)
                        }
                    }
                }
                if let e = connector?.error ?? failure, !e.isEmpty {
                    // Waiting out a rate limit is not something broken: said in gray beside the yellow dot.
                    Text(e).font(Theme.Font.label)
                        .foregroundStyle(connector?.status == "rate_limited" ? Color.secondary : Theme.danger)
                        .textSelection(.enabled)
                }
            } footer: {
                if let connector, !connector.signsInHere, !connector.undescribed {
                    Text("Sign-in is set up in the assistant's configuration (manager.json, see docs/operations/config.md); this page only shows the status.")
                }
            }
            if connector != nil {
                SubscriptionsEntrySection(counts: subscriptions, state: SubscriptionsUI.state(connector: id))
            }
            // Telegram switched off lists nothing until it runs: its switch is here all the same.
            if id == "github", let connector, connector.signsInHere {
                GitHubConnectorSections(connector: connector, manager: manager, reload: { await load() })
            } else if let connector, connector.signsInHere || connector.undescribed {
                ConnectorSignInSections(connector: connector, manager: manager, reload: { await load() })
            }
            if connector?.status != "ok", let guide = connector?.guide, !guide.isEmpty {
                Section {
                    ForEach(Array(guide.enumerated()), id: \.offset) { i, step in
                        HStack(alignment: .firstTextBaseline, spacing: Theme.Space.m) {
                            Text("\(i + 1)").font(Theme.Font.labelStrong).monospacedDigit().foregroundStyle(Theme.accent)
                            Text(step).font(Theme.Font.body).textSelection(.enabled)
                        }
                    }
                    Button { Task { await load() } } label: {
                        Label("Check Again [Lark Chat View c25fb8]", systemImage: Theme.Icon.retry).lineLimit(1)
                    }
                    .disabled(loading)
                } header: {
                    SettingsHeading(String(localized: "How to Fix"))
                }
            }
            if id == "github", let connector, let options = GitHubInboxOptions(connector.options) {
                GitHubInboxSettings(options: options, manager: manager) { connector in self.connector = connector }
            }
        }
        .themedPage()
        .detailTitle(title)
        .navigationBarTitleDisplayMode(.inline)
        // Again once paired (opened at launch, before the hub's address is known) or paired to another hub.
        .task(id: model.config) { await load() }
        .subscriptionCounts($subscriptions, enabled: manager == nil) { [id] in $0.connector == id }
        // Switched on, its program takes a moment to start and say it signs in: asked again until it has (15 s at most).
        .task(id: starting) {
            for _ in 0..<15 where starting {
                try? await Task.sleep(for: .seconds(1))
                if Task.isCancelled { return }
                await load(quietly: true)
            }
        }
        .refreshable { await load() }
    }

    private var starting: Bool { connector?.enabled != false && connector?.undescribed == true }

    private var title: String { connector?.name ?? (id == "github" ? "GitHub" : id) }

    @ViewBuilder private var mark: some View {
        if let brand = ConnectorText.brand(id) {
            BrandMark(brand, side: Theme.Size.wellM)
        } else {
            Image(systemName: ConnectorText.symbol(id)).resizable().scaledToFit().foregroundStyle(Theme.accent)
                .scaledFrame(width: Theme.Size.wellM, height: Theme.Size.wellM)
        }
    }

    private var tint: Color { ConnectorText.tone(connector) }

    private var stateText: String {
        guard let connector else { return loading ? String(localized: "Checking…") : String(localized: "Couldn't get status") }
        // Another manager's copy says whose it is (the default's page is as before).
        return [ConnectorText.state(connector), managerName].compactMap { $0 }.joined(separator: " · ")
    }

    /// `quietly`: no spinner (asked again every second while the program starts).
    private func load(quietly: Bool = false) async {
        // Not paired yet: still Checking…. Another manager's: its own list, where the id is its copy.
        guard let api = model.api?.manager(manager) else { return }
        if !quietly { loading = true }
        defer { loading = false }
        let whose = managerName == nil ? manager : nil
        async let name = ManagerRoster.name(of: whose, api)
        do {
            connector = try await api.connectors().first { $0.id == id }
            failure = connector == nil ? String(localized: "The hub doesn't have this connector") : nil
        } catch {
            failure = error.shown.map { String(localized: "Couldn't get the connector's status: \($0)") }
        }
        if let name = await name { managerName = name }
    }
}
