import BeautifulUI
import SwiftUI

// Settings › Channels (docs/extensions/channels.md「UI」): the list and one channel's page; the Channels row on Settings; the line on a
// connector's page that says it is a channel; the 「来自 …」 under a message and the mark beside a conversation's title.

/// A channel's mark: its brand's (Lark), else a symbol of its kind in the accent color.
struct ChannelMark: View {
    var channel: HubChannel
    var side: CGFloat

    var body: some View {
        if let brand = ChannelText.brand(channel.id) {
            BrandMark(brand, side: side)
        } else {
            Image(systemName: ChannelText.symbol(channel)).resizable().scaledToFit().foregroundStyle(Theme.accent)
                .scaledFrame(width: side, height: side)
                .accessibilityHidden(true)
        }
    }
}

/// The dot beside a channel's state.
private struct ChannelStateDot: View {
    var channel: HubChannel?

    var body: some View {
        Circle().fill(ChannelText.tone(channel)).scaledFrame(width: Theme.Size.dotS, height: Theme.Size.dotS)
    }
}

/// Settings' row before Connectors: Channels, how many are connected (red when one went wrong). Only with a hub that has channels.
struct ChannelsEntryRow: View {
    @Environment(AppModel.self) private var model
    @State private var channels: [HubChannel]?

    var body: some View {
        if model.hubFeatures.contains("channels") {
            Section {
                Group {
                    NavigationLink(value: SettingsPage.channels) {
                        if AppStyle.current.usesBeautifulUI {
                            SettingsRow(title: String(localized: "Channels"), symbol: Theme.Icon.channels, tint: Theme.accent, value: ChannelText.summary(channels), look: .oneLine)
                        } else {
                            LabeledContent {
                                Text(ChannelText.summary(channels)).lineLimit(1)
                                    .foregroundStyle(ChannelText.hasProblem(channels) ? Theme.danger : Color.secondary)
                            } label: {
                                Label { Text("Channels").lineLimit(1) } icon: { ThemedIcon(Theme.Icon.channels) }
                            }
                        }
                    }
                    .settingsListRow(SettingsPage.channels)
                    .accessibilityIdentifier("settings-channels")
                }
                .bareListRow()
            } footer: {
                Text("Where you can talk to Persona: its own apps, WeChat, the Lark bot…")
            }
            .task(id: model.settingsPath.count) { // again when a channel's page closes
                if let l = try? await model.api?.channels() {
                    channels = l
                    ChannelDirectory.shared.update(l)
                }
            }
        }
    }
}

/// Settings › Channels: every way of talking to Persona, in three sections (the apps, the installed, a market's).
struct ChannelsView: View {
    @State private var page = ChannelsModel()

    var body: some View {
        SettingsForm {
            if let list = page.channels, list.isEmpty {
                Section {
                    EmptyStateView(symbol: Theme.Icon.channels, title: String(localized: "No Channels"), message: String(localized: "This hub doesn't list any channels yet."))
                        .listRowBackground(Color.clear)
                }
            }
            ForEach(page.sections) { section in
                Section {
                    Group {
                        ForEach(section.channels) { c in
                            NavigationLink(value: Self.destination(c)) { ChannelRowLabel(channel: c) }
                                .accessibilityIdentifier("channel-\(c.id)")
                        }
                    }
                    .bareListRow()
                } header: {
                    SettingsHeading(section.title)
                } footer: {
                    Text(section.footer)
                }
            }
            if let error = page.error {
                Section { Text(error).font(Theme.Font.label).foregroundStyle(Theme.danger).textSelection(.enabled) }
            }
        }
        .themedPage()
        .overlay { if page.channels == nil && page.loading { ProgressView() } }
        .detailTitle(String(localized: "Channels"))
        .navigationBarTitleDisplayMode(.inline)
        .task { await page.load() }
        .refreshable { await page.load() }
    }

    /// A market's opens its page in the plugin market; the rest their own.
    static func destination(_ c: HubChannel) -> SettingsPage {
        if c.isMarket {
            if let market = c.market, let plugin = c.plugin { return .marketExtension(market: market, id: plugin) }
            return .pluginMarket
        }
        return .channel(c.id)
    }
}

/// A channel's row: its mark, name and account (or what it is), its state on the right.
private struct ChannelRowLabel: View {
    var channel: HubChannel

    var body: some View {
        let subtitle = ChannelText.subtitle(channel)
        if AppStyle.current.usesBeautifulUI {
            BUIListRow(channel.name, detail: subtitle.isEmpty ? nil : subtitle, value: ChannelText.state(channel), detailLines: 1) {
                if let brand = ChannelText.brand(channel.id) {
                    SettingsBrandMark(asset: brand)
                } else {
                    IconTile(ChannelText.symbol(channel), tint: Theme.accent)
                }
            } trailing: {
                ChannelStateDot(channel: channel)
            }
        } else {
            HStack(spacing: Theme.Space.m) {
                ChannelMark(channel: channel, side: Theme.Size.icon)
                VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                    Text(channel.name).lineLimit(1)
                    if !subtitle.isEmpty {
                        Text(subtitle).font(Theme.Font.label)
                            .foregroundStyle(channel.state == "error" ? Theme.danger : Color.secondary).lineLimit(1)
                    }
                }
                Spacer(minLength: Theme.Space.s)
                HStack(spacing: Theme.Space.sm) {
                    ChannelStateDot(channel: channel)
                    Text(ChannelText.state(channel)).font(Theme.Font.secondary).foregroundStyle(.secondary).lineLimit(1)
                }
            }
            .padding(.vertical, Theme.Space.xxs)
        }
    }
}

/// Settings › Channels › one: its state, switch, binding, where its messages go, its features, what waits on it, what it
/// can do, a test message, and its own page if it has one. A read-only one (the apps, /agent) is only looked at.
struct ChannelDetailView: View {
    @Environment(AppModel.self) private var model
    @State private var page: ChannelModel
    @State private var binding: ConnectorSignInModel
    @State private var confirmUnbind = false

    init(id: String) {
        let page = ChannelModel(id: id)
        _page = State(initialValue: page)
        _binding = State(initialValue: ConnectorSignInModel(id: id, deps: ChannelModel.bindingDeps(.live, report: { AppModel.shared.show($0) })))
    }

    var body: some View {
        SettingsForm {
            header
            if let c = page.channel {
                if c.isMarket {
                    market(c)
                } else if c.editable && !c.isApp {
                    enabled(c)
                }
                if c.bindsHere { bind(c) }
                if c.editable, c.kind == "im", c.target != nil { target(c) }
                if c.editable, !c.features.isEmpty { features(c) }
                if let d = page.detail { activity(d) }
                if c.isApp, !c.surfaces.isEmpty { surfaces(c) }
                if !c.isMarket { capabilities(c) }
                if c.editable, c.isBound, c.enabled { test(c) }
                more(c)
            }
        }
        .themedPage()
        .detailTitle(page.channel?.name ?? String(localized: "Channels"))
        .navigationBarTitleDisplayMode(.inline)
        .task(id: model.config) { await page.load() }
        .refreshable { await page.load() }
        .confirmationDialog("Unbind \(unbindName)?", isPresented: $confirmUnbind, titleVisibility: .visible) {
            Button("Unbind", role: .destructive) { Task { await page.unbind() } }
        } message: {
            Text("It will stop passing messages to Persona, and pending answers and questions are cleared. To use it again, bind it again.")
        }
    }

    // MARK: sections

    @ViewBuilder private var header: some View {
        Section {
            HStack(spacing: Theme.Space.m) {
                if let c = page.channel {
                    ChannelMark(channel: c, side: Theme.Size.wellM)
                } else {
                    Image(systemName: Theme.Icon.channels).resizable().scaledToFit().foregroundStyle(Theme.accent)
                        .scaledFrame(width: Theme.Size.wellM, height: Theme.Size.wellM)
                }
                VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                    Text(page.channel?.name ?? page.id).font(Theme.Font.heading).lineLimit(1)
                    HStack(spacing: Theme.Space.sm) {
                        ChannelStateDot(channel: page.channel)
                        Text(stateText).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(2)
                    }
                }
                Spacer(minLength: Theme.Space.s)
                if page.loading && page.detail == nil { ProgressView() }
            }
            .padding(.vertical, Theme.Space.xs)
            .accessibilityElement(children: .combine)
            .accessibilityIdentifier("channel-header")
            if let c = page.channel {
                if let text = c.description, !text.isEmpty {
                    Text(text).font(Theme.Font.secondary).foregroundStyle(.secondary)
                }
                if let at = c.lastEventAt, at.timeIntervalSince1970 > 0 {
                    LabeledContent("Last Message") {
                        TimelineView(.periodic(from: .now, by: 30)) { ctx in
                            Text(Fmt.relative(at, now: ctx.date)).lineLimit(1)
                        }
                    }
                }
                if let e = c.error, !e.isEmpty, c.state != "connected" {
                    // Not bound yet is a sentence, not something broken: red only for an error.
                    Text(e).font(Theme.Font.label).foregroundStyle(c.state == "error" ? Theme.danger : Color.secondary)
                        .textSelection(.enabled)
                        .accessibilityIdentifier("channel-error")
                }
            }
            if let e = page.error, !e.isEmpty {
                Text(e).font(Theme.Font.label).foregroundStyle(Theme.danger).textSelection(.enabled)
            }
        } footer: {
            if let c = page.channel, c.readOnly, !c.isApp {
                Text("This channel is set up elsewhere (manager.json or its own page); this page only shows its status.")
            }
        }
    }

    /// The channel in the unbind question.
    private var unbindName: String { page.channel?.name ?? String(localized: "it", comment: "The channel, when its name is unknown") }

    private var stateText: String {
        guard let c = page.channel else { return page.loading ? String(localized: "Loading… [File Viewer 86b6d0]") : String(localized: "Couldn't load [Connector Thread Models 153e20]") }
        var parts = [ChannelText.state(c)]
        if let a = c.account?.name, !a.isEmpty, !c.isMarket { parts.append(a) }
        return parts.joined(separator: " · ")
    }

    private func market(_ c: HubChannel) -> some View {
        Section {
            NavigationLink(value: ChannelsView.destination(c)) {
                Label { Text("Install from Extension Market").lineLimit(1) } icon: { ThemedIcon(Theme.Icon.pluginMarket) }
            }
            .accessibilityIdentifier("channel-install")
        } footer: {
            Text("After installing, come back here to bind it.")
        }
    }

    private func enabled(_ c: HubChannel) -> some View {
        Section {
            Toggle(isOn: Binding(get: { c.enabled }, set: { on in Task { await page.setEnabled(on) } })) {
                Text("Enabled").lineLimit(1)
            }
            .disabled(page.busy)
            .accessibilityIdentifier("channel-enabled")
        } footer: {
            Text(c.enabled ? String(localized: "When off, it stops passing messages to Persona; the binding and settings stay.") : String(localized: "Turn it on to bind it and receive messages."))
        }
    }

    @ViewBuilder private func bind(_ c: HubChannel) -> some View {
        if c.isBound {
            Section {
                LabeledContent("Bound Account") { Text(c.account?.name ?? String(localized: "Bound")).lineLimit(1) }
                Button(role: .destructive) { confirmUnbind = true } label: { Text("Unbind").lineLimit(1) }
                    .disabled(page.busy)
                    .accessibilityIdentifier("channel-unbind")
            } header: {
                SettingsHeading(String(localized: "Bind"))
            } footer: {
                Text("Only messages from the bound person go to Persona. Others get a short explanation, and group chats are ignored.")
            }
        } else if c.enabled {
            ConnectorSignInSteps(name: c.name, model: binding, reload: { await page.load(quietly: true) },
                                 footer: bindFooter(c), heading: String(localized: "Bind"), start: ChannelText.bindTitle(c))
        }
    }

    private func bindFooter(_ c: HubChannel) -> String {
        switch c.bind {
        case "qr": String(localized: "Scan with \(c.name); whoever scans becomes this channel's owner. The sign-in is stored encrypted on the hub and deleted when you unbind.")
        case "code": String(localized: "Send the pairing code to the bot; whoever sends it becomes this channel's owner.")
        default: String(localized: "Credentials are stored encrypted on the hub and only given to this channel. You may need to pair once afterward; whoever pairs becomes this channel's owner.")
        }
    }

    // Send Messages To: the main conversation, a new one (opened by the next message), or one of the assistant's.

    private func target(_ c: HubChannel) -> some View {
        Section {
            Picker(selection: Binding(get: { Self.tag(c.target) }, set: { tag in
                guard let t = targetFor(tag), tag != Self.tag(c.target) else { return }
                Task { await page.setTarget(t) }
            })) {
                Text("Main Chat").tag("main")
                Text("New chat (starts with the next message)").tag("new")
                ForEach(choices(c)) { s in
                    Text(s.title).lineLimit(1).tag(s.tag)
                }
                if c.target?.kind == "agent" { Text(ChannelText.target(c.target)).lineLimit(1).tag("agent") }
            } label: {
                Text("Send Messages To").lineLimit(1)
            }
            .disabled(page.busy)
            .accessibilityIdentifier("channel-target")
        } footer: {
            Text(c.target?.kind == "agent" ? String(localized: "Messages now go to the agent picked with /agents in Lark. You can switch back to a Persona chat here.")
                 : String(localized: "In \(c.name) you can also use /new for a new chat and /main to go back to the main chat."))
        }
    }

    private struct Choice: Identifiable {
        var tag: String
        var title: String
        var id: String { tag }
    }

    /// The assistant's recent conversations (the main one is Main Chat), with the current one if it is not among them:
    /// the default manager's, whose channels these are, whichever manager the Persona page shows.
    private func choices(_ c: HubChannel) -> [Choice] {
        let main = model.defaultAssistantMain
        var out = model.defaultAssistantConversations.filter { $0.key != main }.prefix(15).map { s in
            Choice(tag: "c:" + s.machineID + "/" + s.sessionID, title: s.title.isEmpty ? String(localized: "New Chat [Quick Actions ea4b29]") : s.title)
        }
        if let t = c.target, t.kind == "conversation", case let tag = Self.tag(t), !out.contains(where: { $0.tag == tag }) {
            out.insert(Choice(tag: tag, title: ChannelText.target(t)), at: 0)
        }
        return Array(out)
    }

    private static func tag(_ t: ChannelTarget?) -> String {
        switch t?.kind {
        case "new": "new"
        case "conversation": "c:" + (t?.machineID ?? "") + "/" + (t?.sessionID ?? "")
        case "agent": "agent"
        default: "main"
        }
    }

    private func targetFor(_ tag: String) -> ChannelTarget? {
        switch tag {
        case "main": return .main
        case "new": return .new
        default:
            guard tag.hasPrefix("c:"), let slash = tag.firstIndex(of: "/") else { return nil }
            let machine = String(tag[tag.index(tag.startIndex, offsetBy: 2)..<slash])
            let session = String(tag[tag.index(after: slash)...])
            return ChannelTarget(kind: "conversation", machineID: machine, sessionID: session)
        }
    }

    private func features(_ c: HubChannel) -> some View {
        Section {
            ForEach(c.features) { f in
                Toggle(isOn: Binding(get: { f.on }, set: { on in Task { await page.setFeature(f.id, on: on) } })) {
                    VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                        Text(f.title).lineLimit(1)
                        if let d = f.description, !d.isEmpty {
                            Text(d).font(Theme.Font.label).foregroundStyle(.secondary)
                        }
                    }
                }
                .disabled(page.busy)
                .accessibilityIdentifier("channel-feature-\(f.id)")
            }
        } header: {
            SettingsHeading(String(localized: "Features"))
        }
    }

    /// What waits on it, and the conversations its messages went to lately (tapped: opened).
    @ViewBuilder private func activity(_ d: HubChannelDetail) -> some View {
        if d.waiting.answers > 0 || d.waiting.asks > 0 || !d.conversations.isEmpty {
            Section {
                if d.waiting.answers > 0 {
                    LabeledContent("Pending Answers") { Text("\(d.waiting.answers) [Chats View cb60bd]").monospacedDigit() }
                }
                if d.waiting.asks > 0 {
                    LabeledContent("Questions Sent Here") { Text("\(d.waiting.asks) [Chats View cb60bd]").monospacedDigit() }
                }
                ForEach(d.conversations) { conv in
                    Button {
                        model.openAssistantConversation(SessionKey(group: model.hub?.group ?? "", machineID: conv.machineID, sessionID: conv.sessionID))
                    } label: {
                        LabeledContent {
                            if let at = conv.at { Text(Fmt.relative(at)).lineLimit(1) }
                        } label: {
                            Label { Text(conv.title?.isEmpty == false ? conv.title! : String(localized: "A chat")).lineLimit(1) } icon: {
                                ThemedIcon(Theme.Icon.conversation)
                            }
                        }
                    }
                    .foregroundStyle(Color.primary)
                }
            } header: {
                SettingsHeading(String(localized: "Recent"))
            }
        }
    }

    private func surfaces(_ c: HubChannel) -> some View {
        Section {
            Text(c.surfaces.joined(separator: "、")).font(Theme.Font.body)
        } header: {
            SettingsHeading(String(localized: "Works In"))
        }
    }

    private func capabilities(_ c: HubChannel) -> some View {
        Section {
            ForEach(ChannelText.capabilities(c.capabilities), id: \.self) { line in
                Text(line).font(Theme.Font.body)
            }
        } header: {
            SettingsHeading(String(localized: "What It Can Do"))
        }
    }

    private func test(_ c: HubChannel) -> some View {
        Section {
            Button {
                Task { await page.test() }
            } label: {
                Text(page.busy ? String(localized: "Sending… [Channels Views 98c8e5]") : String(localized: "Send Test Message")).lineLimit(1)
            }
            .disabled(page.busy)
            .accessibilityIdentifier("channel-test")
        } footer: {
            Text(page.testResult ?? String(localized: "Sent only to you, the bound person, to check that \(c.name) receives it."))
        }
    }

    @ViewBuilder private func more(_ c: HubChannel) -> some View {
        if let dest = ChannelText.page(page.detail?.settingsURL) {
            Section {
                if dest == .sent {
                    Button { model.openSentRecords() } label: {
                        Label { Text("Records sent as you").lineLimit(1) } icon: { ThemedIcon(Theme.Icon.sentAsYou) }
                    }
                    .accessibilityIdentifier("channel-more")
                } else {
                    NavigationLink(value: dest) {
                        Label { Text(moreTitle(dest)).lineLimit(1) } icon: { ThemedIcon(Theme.Icon.options) }
                    }
                    .accessibilityIdentifier("channel-more")
                }
            }
        }
    }

    private func moreTitle(_ dest: SettingsPage) -> String {
        switch dest {
        case .modelConfiguration: String(localized: "Model Settings [Launcher Model Picker 8cbcf7]")
        case .plugin: String(localized: "Extension Settings")
        default: String(localized: "More Settings")
        }
    }
}

/// The line at the top of a connector's page that is a channel too (the Feishu bot, WeChat): where it is set up as one.
struct ChannelLinkSection: View {
    @Environment(AppModel.self) private var model
    var channelID: String

    var body: some View {
        if model.hubFeatures.contains("channels") {
            Section {
                NavigationLink(value: SettingsPage.channel(channelID)) {
                    Label {
                        VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                            Text("This is a chat channel").lineLimit(1)
                            Text("Set it up in Settings › Channels").font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1)
                        }
                    } icon: {
                        ThemedIcon(Theme.Icon.channels).foregroundStyle(Theme.accent)
                    }
                }
                .accessibilityIdentifier("connector-channel-link")
            }
        }
    }
}

/// 「来自 微信」 under a message that came in from a channel (its client_msg_id is `<channel>-…`, docs/extensions/channels.md
/// 「来源标记」); nothing for the app's own, or on a hub without channels. The names come from `ChannelDirectory`, which
/// the root view keeps loaded (`.loadsChannels()`): this view is empty until then, and an empty view runs no task.
struct ChannelSourceLine: View {
    var clientMsgID: String?

    var body: some View {
        if let c = ChannelSource.of(clientMsgID, in: ChannelDirectory.shared.list) {
            HStack(spacing: Theme.Space.xs) {
                ChannelMark(channel: c, side: Theme.Size.markS)
                Text("From \(c.name)").font(Theme.Font.caption).foregroundStyle(.secondary).lineLimit(1)
            }
            .accessibilityElement(children: .combine)
            .accessibilityIdentifier("message-channel")
        }
    }
}

extension View {
    /// Keeps `ChannelDirectory` loaded for the hub on screen: once it says it has channels, again for another hub.
    func loadsChannels() -> some View { modifier(ChannelDirectoryLoader()) }
}

private struct ChannelDirectoryLoader: ViewModifier {
    @Environment(AppModel.self) private var model

    func body(content: Content) -> some View {
        let has = model.hubFeatures.contains("channels")
        content.task(id: "\(has)|\(model.api?.config.url.absoluteString ?? "")|\(model.api?.group ?? "")") {
            ChannelDirectory.shared.ensure(model.api, features: model.hubFeatures)
        }
    }
}

/// The mark beside a conversation's title: the extension that opened it (#3758, `AutoConversationSource.external`), else
/// the channel its latest message came in from. One mark: an extension's conversation has no channel of its own.
struct ConversationSourceMark: View {
    var origin: String?
    var channels: [String]?

    var body: some View {
        if let source = AutoConversationSource.external(origin) {
            ExternalSourceMark(source: source)
                .accessibilityElement()
                .accessibilityLabel(source.from)
        } else {
            ConversationChannelMark(ids: channels)
        }
    }
}

/// Where an external conversation came from, as a mark of `side` points: Lark's brand for Lark /agent, the extensions
/// symbol for another extension (#3758).
struct ExternalSourceMark: View {
    var source: AutoConversationSource
    var side: CGFloat = Theme.Size.markS

    var body: some View {
        if let brand = source.brand {
            BrandMark(brand, side: side)
        } else {
            ThemedIcon(source.symbol).font(Theme.Font.caption).foregroundStyle(.secondary)
                .scaledFrame(width: side, height: side)
                .accessibilityHidden(true)
        }
    }
}

/// 「来自 飞书 /agent」 under the message an extension opened a conversation with (#3758), as a channel's message has
/// 「来自 微信」 (`ChannelSourceLine`).
struct ExternalSourceLine: View {
    var source: AutoConversationSource

    var body: some View {
        HStack(spacing: Theme.Space.xs) {
            ExternalSourceMark(source: source)
            Text(source.from).font(Theme.Font.caption).foregroundStyle(.secondary).lineLimit(1)
        }
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("message-external-source")
    }
}

/// The latest channel a conversation had messages from (`Session.channels`), small beside its title in the sidebar.
struct ConversationChannelMark: View {
    var ids: [String]?

    var body: some View {
        if let id = ids?.first {
            let c = ChannelDirectory.shared.list.first { $0.id == id } ?? HubChannel(id: id, name: id)
            ChannelMark(channel: c, side: Theme.Size.markS)
                .foregroundStyle(.secondary)
                .accessibilityLabel("From \(c.name)")
        }
    }
}
