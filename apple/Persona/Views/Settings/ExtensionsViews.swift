import SwiftUI

/// Settings › Extensions: every plugin of the hub's (built in, from hub.json, installed on the hub's machine), its state, and
/// the way to its page. Extensions are installed on the hub's side only (docs/extensions/extensions.md「Install, upgrade, roll back, uninstall」): with `personad plugin` on
/// its machine or in its admin UI (/admin); the footer says how.
struct ExtensionsView: View {
    @State private var page = ExtensionsModel()
    @State private var cardLink: LinkTarget?

    var body: some View {
        SettingsForm {
            // Extension Market (docs/extensions/extensions.md「Extension marketplace」): only with a hub that has markets; first,
            // above the installed ones (#5017).
            if page.markets {
                Section {
                    Group {
                        NavigationLink(value: SettingsPage.pluginMarket) {
                            SettingsRow(title: String(localized: "Extension Market"), symbol: Theme.Icon.pluginMarket, tint: Theme.accent)
                        }
                        .accessibilityIdentifier("plugins-market")
                    }
                    .bareListRow()
                }
            }
            #if os(macOS)
            // The plugins' overview cards (docs/extensions/extensions.md「UI slots」): the Mac has no Overview, they lead the installed ones (their
            // pages are in the sidebar). The iPhone has them on Settings itself.
            ExtensionCardsSection(link: $cardLink)
            #endif
            if let plugins = page.plugins, plugins.isEmpty {
                Section {
                    EmptyStateView(symbol: Theme.Icon.plugins, title: String(localized: "No Extensions"),
                                   message: String(localized: "Install extensions in the hub's admin page (/admin) or on the hub machine with personad extension install, then turn them on and set them up here."))
                        .listRowBackground(Color.clear)
                }
            } else if let groups = page.groups {
                // Several managers (docs/assistant/managers.md "Grouping"): each one's under its name, the default first.
                ForEach(groups) { group in
                    Section {
                        Group { rows(group.items) }
                            .bareListRow()
                    } header: {
                        ManagerHeading(manager: group.manager, count: group.items.count)
                    } footer: {
                        if group.id == groups.last?.id { footer }
                    }
                }
            } else if let plugins = page.plugins {
                Section {
                    Group { rows(plugins) }
                        .bareListRow()
                } footer: {
                    footer
                }
            }
            if let error = page.error {
                #if os(macOS)
                // A page's empty state, not the request's error in red (#2113).
                Section {
                    EmptyStateView(symbol: Theme.Icon.plugins, title: page.missing ? String(localized: "No Extensions") : String(localized: "Couldn't Load Extensions"),
                                   message: page.missing
                                       ? (AppModel.shared.isDemo ? String(localized: "Demo mode has no extensions. Connect your own hub to turn on and set up installed extensions here.")
                                                                 : String(localized: "This hub's version doesn't support extensions yet. Update the hub and check back."))
                                       : error,
                                   action: page.missing ? nil : (String(localized: "Retry [Components b8784c]"), { Task { await page.load() } }))
                        .listRowBackground(Color.clear)
                }
                #else
                Section { Text(error).font(Theme.Font.label).foregroundStyle(Theme.danger).textSelection(.enabled) }
                #endif
            }
        }
        .accessibilityIdentifier("extensions-list")
        .themedPage()
        #if os(macOS)
        .pluginLinks($cardLink)
        #endif
        .overlay { if page.plugins == nil && page.loading { ProgressView() } }
        .detailTitle(String(localized: "Extensions"))
        .navigationBarTitleDisplayMode(.inline)
        .task { await page.load() }
        #if os(macOS)
        .task { await ExtensionSlots.shared.reload() }
        #endif
        .refreshable {
            await page.load()
            await ExtensionSlots.shared.reload()
        }
        .onChange(of: AppModel.shared.managersRevision) { Task { await page.load() } }
    }

    /// The rows of a list, each opening its manager's copy: an id is listed once per manager (each one's built-ins).
    private func rows(_ plugins: [HubExtension]) -> some View {
        ForEach(plugins, id: \.listKey) { p in
            let manager = page.manager(of: p)
            NavigationLink(value: SettingsPage.plugin(p.id, manager: manager)) { ExtensionRowLabel(plugin: p, manager: manager) }
                .accessibilityIdentifier(manager.map { "plugin-\($0)-\(p.id)" } ?? "plugin-\(p.id)")
        }
    }

    private var footer: some View {
        Text("Extensions are installed, updated and uninstalled in the hub's admin page (/admin) or on the hub machine (personad extension). Here you can turn them on or off, change settings and enter secrets.")
    }
}

/// A plugin's row: its name, and its state (or version) on the right with a dot that says whether it needs the user.
private struct ExtensionRowLabel: View {
    var plugin: HubExtension
    /// Whose copy (nil: the default manager's): its sign-in is that copy's.
    var manager: String?

    private var auth: ExtensionAuth? { ExtensionAuthStore.shared.auth(plugin.id, manager: manager, else: plugin.auth) }

    var body: some View {
        if AppStyle.current.usesBeautifulUI {
            SettingsRow(title: plugin.name, symbol: Theme.Icon.plugins, tint: ExtensionStateDot.tone(plugin), value: ExtensionText.summary(plugin, auth: auth), iconURL: plugin.iconURL)
        } else {
            LabeledContent {
                HStack(spacing: Theme.Space.sm) {
                    ExtensionStateDot(plugin: plugin)
                    Text(ExtensionText.summary(plugin, auth: auth)).lineLimit(1)
                }
            } label: {
                Label {
                    Text(plugin.name).lineLimit(1)
                } icon: {
                    if ExtensionImageCache.url(plugin.iconURL) != nil {
                        ExtensionLogo(url: plugin.iconURL, side: Theme.Size.iconL)
                    } else {
                        ThemedIcon(Theme.Icon.plugins)
                    }
                }
            }
        }
    }
}

/// A line of a plugin's Update section: what, and below it what happens (in the caution colour when it needs the user).
struct ExtensionUpgradeLine: View {
    var title: String
    var detail: String
    var attention: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.xxs) {
            Text(title).font(Theme.Font.body)
            if !detail.isEmpty {
                Text(detail).font(Theme.Font.label).foregroundStyle(attention ? Theme.cautionText : Color.secondary)
            }
        }
        .padding(.vertical, Theme.Space.xxs)
    }
}

/// The dot beside a plugin's state.
private struct ExtensionStateDot: View {
    var plugin: HubExtension?

    var body: some View {
        Circle().fill(Self.tone(plugin)).scaledFrame(width: Theme.Size.dotS, height: Theme.Size.dotS)
    }

    static func tone(_ p: HubExtension?) -> Color {
        guard let p else { return .secondary }
        if ExtensionText.isProblem(p) { return Theme.danger }
        switch p.status {
        case "running", "stopped": return Theme.positive // on, running or idle (#1684)
        case "needs_setup": return Theme.caution
        default: return .secondary
        }
    }
}

/// Settings › Extensions › one: what it is and how it is doing, its switch, its settings as a form drawn from its schema, its
/// secrets (set here, never shown again), what it contributes and what it may do. A plugin written in hub.json is only
/// looked at here.
struct ExtensionDetailView: View {
    @State private var page: ExtensionModel
    @State private var editingSecret: ExtensionSecret?
    /// An Always rule the user asked to remove: confirmed first.
    @State private var removingRule: HandoffRule?
    @State private var declarations = ExtensionTextContributions()
    @State private var connectorInbox: ExtensionConnectorsModel
    /// Its connectors' source subscriptions (#2041): the entry row's counts, nil while it is hidden.
    @State private var subscriptions: SubscriptionCounts?
    @Environment(\.scenePhase) private var phase

    @State private var signIn: ExtensionSignInModel
    private var authStore: ExtensionAuthStore { .shared }
    /// Its updates and the managers: the page's, not a row's (`ExtensionSettingsPageWork`, #2988).
    @State private var lifecycle: ExtensionSettingsLifecycle
    @State private var roster: ManagerRoster?

    /// `manager`: another manager's copy (docs/assistant/managers.md "Grouping"): everything the page reads and changes is
    /// that manager's, and so are the pages and connectors it opens. nil: the default manager's.
    init(id: String, manager: String? = nil) {
        _page = State(initialValue: ExtensionModel(id: id, manager: manager))
        _signIn = State(initialValue: ExtensionSignInModel(id: id, manager: manager))
        _connectorInbox = State(initialValue: ExtensionConnectorsModel(manager: manager))
        _lifecycle = State(initialValue: ExtensionSettingsLifecycle(id: id, manager: manager))
    }

    /// Its sign-in, with a hub that has them (#4507): the latest of the page's and the pushes'.
    private var auth: ExtensionAuth? {
        guard ExtensionAuthStore.available(AppModel.shared.hubFeatures), let d = page.detail else { return nil }
        return authStore.auth(d.plugin.id, manager: page.manager, else: d.plugin.auth)
    }

    private struct ConnectorSectionsKey: Equatable {
        var ids: [String]
        var depth: Int
        var hub: String?
    }

    /// The connectors it contributes while it is on: switched off, they are off too.
    private var connectorIDs: [String] {
        guard let p = page.detail?.plugin, p.enabled ?? true else { return [] }
        return p.contributes?["connectors"] ?? []
    }

    var body: some View {
        SettingsForm {
            header
            if let d = page.detail {
                ExtensionSettingsView(page: page, lifecycle: lifecycle, roster: roster, detail: d,
                                      hasConfiguration: hasConfiguration(d)) {
                    configuration(d)
                } advancedInformation: {
                    contributions(d)
                    dependencies(d)
                    permissions(d)
                }
            }
        }
        .modifier(ExtensionSettingsPageWork(page: page, lifecycle: lifecycle, roster: $roster))
        .themedPage()
        .detailTitle(page.detail?.plugin.name ?? String(localized: "Extensions"))
        .navigationBarTitleDisplayMode(.inline)
        .confirmationDialog("Also Turn Off Extensions That Depend on It?", isPresented: Binding(
            get: { page.disableConfirmation != nil },
            set: { if !$0 { page.disableConfirmation = nil } }), titleVisibility: .visible) {
            Button(role: .destructive) { Task { await page.confirmDisable() } } label: { Text("Turn Off All").lineLimit(1) }
                .accessibilityIdentifier("extension-cascade-confirm")
            Button("Cancel", role: .cancel) {}
        } message: {
            Text(page.disableConfirmation?.message ?? "")
        }
        .sheet(item: $editingSecret) { secret in
            // Closed by clearing the item it is shown for, so the page and the sheet cannot disagree about whether it is
            // open (tapping the same secret again must open it).
            ExtensionSecretSheet(secret: secret, save: { await page.setSecret(secret.name, value: $0) },
                              saveReference: { await page.setSecretReference(secret.name, reference: $0) },
                              clear: { await page.clearSecret(secret.name) }, close: { editingSecret = nil })
        }
        .confirmationDialog(String(localized: "Remove this rule?"), isPresented: Binding(get: { removingRule != nil }, set: { if !$0 { removingRule = nil } }),
                            titleVisibility: .visible, presenting: removingRule) { rule in
            Button(role: .destructive) { Task { await page.removeHandoffRule(rule.id) } } label: { Text("Remove [Handoff Rule]").lineLimit(1) }
                .accessibilityIdentifier("handoff-rule-remove-confirm")
            Button("Cancel", role: .cancel) {}
        } message: { _ in
            Text("The next time an Agent asks for it, you approve it again.")
        }
        .task { await page.load() }
        // A launcher field's choices (format launcher): the hub asks every machine for them, so apart from the page.
        .task(id: page.detail?.plugin.id) { await page.loadLaunchersIfNeeded() }
        .subscriptionCounts($subscriptions, enabled: page.manager == nil) { [id = page.id] in $0.extensionID == id }
        // Again when the connector's page closes (its switch may have changed there) or the extension is switched.
        // Also once paired (opened at launch, before the hub's address is known) or paired to another hub.
        .task(id: ConnectorSectionsKey(ids: connectorIDs, depth: AppModel.shared.settingsPath.count,
                                       hub: AppModel.shared.api?.config.url.absoluteString)) {
            await connectorInbox.load(ids: connectorIDs)
        }
        .tabActiveTask(id: ExtensionTextSource(group: AppModel.shared.hub?.group ?? "", features: AppModel.shared.hubFeatures,
                                               hub: AppModel.shared.api?.config.url.absoluteString, active: phase == .active)) {
            guard phase == .active else { return }
            await declarations.refreshWhileActive(api: AppModel.shared.api)
        }
        .refreshable { await page.load() }
        // What the page read is the hub's latest word on the sign-in too; an install that ended waiting for a sign-in
        // opens it here (`next: "auth"`).
        .onChange(of: page.detail?.plugin.auth, initial: true) { _, a in
            if let a, let id = page.detail?.plugin.id { authStore.received(id, a, manager: page.manager) }
        }
        .task(id: auth != nil) {
            // The market installs into the default manager: its sign-in opens on the default's page.
            guard let auth, page.manager == nil, authStore.openSignIn == page.id else { return }
            authStore.openSignIn = nil
            if !auth.isSignedIn, !signIn.signingIn, page.detail?.plugin.enabled ?? true { await signIn.start() }
        }
    }

    // MARK: sections

    @ViewBuilder private var header: some View {
        Section {
            HStack(spacing: Theme.Space.m) {
                ExtensionLogo(url: page.detail?.plugin.iconURL)
                VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                    Text(page.detail?.plugin.name ?? page.id).font(Theme.Font.heading).lineLimit(1)
                    HStack(spacing: Theme.Space.sm) {
                        ExtensionStateDot(plugin: page.detail?.plugin)
                        Text(stateText).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(2)
                    }
                }
                Spacer(minLength: Theme.Space.s)
                if page.loading && page.detail == nil { ProgressView() }
            }
            .padding(.vertical, Theme.Space.xs)
            let text = page.detail?.plugin.description ?? ""
            let note = page.detail.flatMap { ExtensionText.runNote($0.plugin) }
            if !text.isEmpty || note != nil {
                // Whether it runs right now is small print under what it is, in the same row (#1684).
                VStack(alignment: .leading, spacing: Theme.Space.xs) {
                    if !text.isEmpty { Text(text).font(Theme.Font.secondary).foregroundStyle(.secondary) }
                    if let note {
                        Text(note).font(Theme.Font.label).foregroundStyle(.secondary)
                            .accessibilityIdentifier("plugin-run-note")
                    }
                }
            }
            if let needs = page.detail?.plugin.needsSetup, !needs.isEmpty {
                Text(String(localized: "Still needs: \(ExtensionText.list(needs))")).font(Theme.Font.label).foregroundStyle(Theme.caution)
            }
            if let e = page.detail?.plugin.error ?? page.error, !e.isEmpty {
                Text(e).font(Theme.Font.label).foregroundStyle(Theme.danger).textSelection(.enabled)
            }
        }
    }

    private var stateText: String {
        guard let p = page.detail?.plugin else { return page.loading ? String(localized: "Loading… [File Viewer 86b6d0]") : String(localized: "Couldn't load [Connector Thread Models 153e20]") }
        // Another manager's copy says whose it is (the default's page is as before).
        return [ExtensionText.state(p), page.managerName].compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: " · ")
    }

    private func hasConfiguration(_ d: HubExtensionDetail) -> Bool {
        auth != nil || !d.ui.isEmpty || !connectorInbox.connectors.isEmpty || d.readOnly || !d.form.isEmpty || !d.secrets.isEmpty
            || VoiceTimbreSection.shows(d.plugin, manager: page.manager)
    }

    @ViewBuilder private func configuration(_ d: HubExtensionDetail) -> some View {
        if let auth {
            ExtensionAuthSection(auth: auth, enabled: d.plugin.enabled ?? true, model: signIn)
        }
        // The voice extension: the voice this device's calls speak with (#2411), whoever's settings the rest are.
        if VoiceTimbreSection.shows(d.plugin, manager: page.manager) {
            VoiceTimbreSection(extensionID: d.plugin.id, group: AppModel.shared.api?.group ?? "")
        }
        if d.readOnly {
            Section {
                Text("This extension is set in its assistant's manager.json; change its switch and settings there, then restart the assistant.")
                    .font(Theme.Font.label).foregroundStyle(.secondary)
            }
        }
        pages(d)
        if d.plugin.enabled ?? true {
            SubscriptionsEntrySection(counts: subscriptions, state: SubscriptionsUI.state(extension: d.plugin.id))
        }
        connectorSections(d)
        if !d.readOnly && !d.form.isEmpty { form(d) }
        if !d.readOnly && !d.secrets.isEmpty { secrets(d) }
        SecretProviderSections(users: d.secretUsers, rules: d.handoffRules, editable: !d.readOnly) { removingRule = $0 }
        if !d.readOnly && !d.form.isEmpty {
            Section {
                FormAction {
                    Button { Task { await page.save() } } label: {
                        Group {
                            if page.saving { ProgressView() } else { Text("Save").lineLimit(1) }
                        }
                        .formActionWidth()
                    }
                    .themedButton(prominent: true)
                    .disabled(!page.canSave)
                    .accessibilityIdentifier("plugin-save")
                }
            }
        }
    }

    /// The pages it draws (docs/extensions/extensions.md「Declarative UI」), a row each.
    @ViewBuilder private func pages(_ d: HubExtensionDetail) -> some View {
        // The extension's own sign-in page is for apps without the Sign In section.
        let ui = auth?.page.map { page in d.ui.filter { $0.id != page } } ?? d.ui
        if !ui.isEmpty {
            Section {
                ForEach(ui) { place in
                    NavigationLink(value: SettingsPage.pluginPage(plugin: d.plugin.id, ui: place.id, title: place.title, manager: page.manager)) {
                        Label { Text(place.title).lineLimit(1) } icon: { ThemedIcon(Theme.Icon.pluginPage) }
                    }
                    .accessibilityIdentifier("plugin-page-\(place.id)")
                }
            } header: {
                SettingsHeading(String(localized: "Pages"))
            }
        }
    }

    /// A section per connector it contributes that has an inbox (#3228): the connector's own Show in Inbox switch and the
    /// way to its page. Not while the extension is off, nor when the hub's list cannot be read or has no such switch.
    @ViewBuilder private func connectorSections(_ d: HubExtensionDetail) -> some View {
        if d.plugin.enabled ?? true {
            ForEach(connectorInbox.connectors) { c in
                Section {
                    if let inbox = c.inbox {
                        Toggle(isOn: Binding(get: { inbox }, set: { on in
                            Task { await connectorInbox.setInbox(c.id, on: on) }
                        })) {
                            Text("Show in Inbox").lineLimit(1)
                        }
                        .disabled(connectorInbox.changing != nil)
                        .accessibilityIdentifier("extension-connector-inbox-\(c.id)")
                    }
                    NavigationLink(value: SettingsPage.connector(c.id, manager: page.manager)) {
                        Label {
                            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                                Text("Connector Settings").lineLimit(1)
                                Text(c.id == "imessage" ? String(localized: "Access, status and Hand to Persona [Extensions Views 1224]") : String(localized: "Sign-in, status and more Inbox settings")).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1)
                            }
                        } icon: {
                            ThemedIcon(Theme.Icon.connectors)
                        }
                    }
                    .accessibilityIdentifier("extension-connector-open-\(c.id)")
                } header: {
                    SettingsHeading(String(localized: "Connector \(c.name)"))
                } footer: {
                    if let inbox = c.inbox {
                        if inbox {
                            Text("New \(c.name) items show in the Inbox. This is the same switch as Show in Inbox on the connector's page.")
                        } else {
                            Text("Not showing in Inbox: nothing new arrives; existing items stay.")
                        }
                    }
                }
            }
        }
    }

    private func form(_ d: HubExtensionDetail) -> some View {
        Group {
            Section {
                ForEach(d.form.filter { $0.type != "object" }) { f in ExtensionFieldRow(field: f, path: [f.name], page: page, templates: declarations.templates) }
            } header: {
                SettingsHeading(String(localized: "Settings"))
            } footer: {
                if page.formError != nil || ExtensionText.saveFooter(d.form) != nil {
                    VStack(alignment: .leading, spacing: Theme.Space.xs) {
                        // The extension's own word on settings it refused (#5906), where Save is.
                        if let e = page.formError {
                            Text(e).foregroundStyle(Theme.danger).accessibilityIdentifier("plugin-form-refused")
                        }
                        if let footer = ExtensionText.saveFooter(d.form) { Text(footer) }
                    }
                }
            }
            ForEach(d.form.filter { $0.type == "object" }) { group in
                Section {
                    ForEach(group.fields ?? []) { f in ExtensionFieldRow(field: f, path: [group.name, f.name], page: page, templates: declarations.templates) }
                } header: {
                    SettingsHeading(group.label)
                } footer: {
                    if let text = group.description, !text.isEmpty { Text(text) }
                }
            }
        }
    }

    private func secrets(_ d: HubExtensionDetail) -> some View {
        Section {
            ForEach(d.secrets) { s in
                Button { editingSecret = s } label: {
                    LabeledContent {
                        Text(ExtensionText.secretState(s))
                            .foregroundStyle(s.set || s.required != true ? Color.secondary : Theme.caution)
                            .lineLimit(1)
                    } label: {
                        Label {
                            VStack(alignment: .leading, spacing: Theme.Space.lines) {
                                Text(s.title).lineLimit(1)
                                // what a provider's reference names, under the secret's title
                                if let r = s.reference {
                                    Text(verbatim: r.title?.isEmpty == false ? r.title! : r.reference)
                                        .font(Theme.Font.caption).foregroundStyle(.secondary).lineLimit(1)
                                }
                            }
                        } icon: { ThemedIcon(Theme.Icon.credentials) }
                    }
                }
                .foregroundStyle(Color.primary)
                .accessibilityIdentifier("plugin-secret-\(s.name)")
            }
        } header: {
            SettingsHeading(String(localized: "Secrets"))
        } footer: {
            Text("Secrets are stored encrypted on the hub and given only to this extension. Once saved they aren't shown again; you can only replace or clear them.")
        }
    }

    @ViewBuilder private func contributions(_ d: HubExtensionDetail) -> some View {
        if let contributions = d.contributions {
            if !contributions.isEmpty {
                Section {
                    ForEach(contributions) { c in
                        Toggle(isOn: Binding(get: { c.enabled }, set: { on in
                            Task { await page.setContribution(c, enabled: on) }
                        })) {
                            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                                Text(c.label).lineLimit(1)
                                Text(c.via == "connector" ? String(localized: "\(ExtensionText.point(c.point)) · with connector") : ExtensionText.point(c.point))
                                    .font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1)
                            }
                        }
                        .disabled(d.readOnly || !c.canDisable || page.changingContribution != nil || page.changingEnabled || page.saving)
                        .accessibilityIdentifier("extension-contribution-\(c.id)")
                    }
                } header: {
                    SettingsHeading(String(localized: "Provides"))
                } footer: {
                    Text("Switches take effect right away. Turning one off stops only that contribution; other features and settings stay.")
                }
            }
        } else if let c = d.plugin.contributes, !c.isEmpty {
            Section {
                ForEach(c.keys.sorted(), id: \.self) { point in
                    LabeledContent(ExtensionText.point(point)) {
                        Text((c[point] ?? []).joined(separator: "、")).lineLimit(2).multilineTextAlignment(.trailing)
                    }
                }
            } header: {
                SettingsHeading(String(localized: "Provides"))
            }
        }
    }

    @ViewBuilder private func dependencies(_ d: HubExtensionDetail) -> some View {
        if let dependencies = d.depends, !dependencies.isEmpty {
            Section {
                ForEach(dependencies) { dependency in
                    VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                        Text(dependency.title).font(Theme.Font.body).lineLimit(1)
                        Text(dependency.description).font(Theme.Font.label)
                            .foregroundStyle(dependency.state == "ok" ? Color.secondary : Theme.cautionText)
                    }
                }
            } header: { SettingsHeading(String(localized: "Dependencies")) }
        }
        if let members = d.extensionPack, !members.isEmpty {
            Section {
                ForEach(members, id: \.id) { member in
                    ExtensionPackMemberRow(line: ExtensionPackMemberLine(member, name: page.memberNames[member.id]))
                }
            } header: { SettingsHeading(String(localized: "Pack Members")) }
        }
    }

    @ViewBuilder private func permissions(_ d: HubExtensionDetail) -> some View {
        if !d.permissions.isEmpty {
            Section {
                ForEach(d.permissions) { p in
                    VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                        Text(ExtensionText.permissionTitle(p)).font(Theme.Font.body)
                        if let detail = p.detail, !detail.isEmpty {
                            Text(detail).font(Theme.Font.label).foregroundStyle(.secondary)
                        }
                    }
                    .padding(.vertical, Theme.Space.xxs)
                }
            } header: {
                SettingsHeading(String(localized: "It Can"))
            } footer: {
                Text("You agreed to these when installing. If a new version asks for more, you'll be asked again before the update.")
            }
        }
    }

}

/// One field of a plugin's form, by its type: a text field, a number, a switch, a choice, a list of texts or of
/// choices. Its title above, its description and the hub's word on it below.
private struct ExtensionFieldRow: View {
    @Environment(AppModel.self) private var model
    var field: ExtensionField
    var path: [String]
    @Bindable var page: ExtensionModel
    var templates: [ExtensionTextTemplate]
    /// A switch with x-confirm waiting on its alert before it is turned on.
    @State private var asking = false

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.xs) {
            control
            if field.name == "triage_rules", field.type == "string" {
                ExtensionTriageTemplatePicker(text: Binding(
                    get: { page.value(path)?.string ?? "" },
                    set: { page.set($0.isEmpty ? nil : .string($0), at: path) }), templates: templates)
            }
            if let text = field.description, !text.isEmpty {
                Text(text).font(Theme.Font.label).foregroundStyle(.secondary).accessibilityIdentifier(identifier)
            }
            if let e = page.fieldError(path) {
                Text(e).font(Theme.Font.label).foregroundStyle(Theme.danger).accessibilityIdentifier(identifier)
            }
        }
        .padding(.vertical, Theme.Space.xxs)
        // On the whole row, except for a list of machines: there it would replace its machines' own (their setup links,
        // #2446), so the list puts it on its title and switches itself.
        .modifier(ExtensionFieldIdentifier(id: field.items?.format == "machine" ? nil : identifier))
    }

    private var identifier: String { "plugin-field-\(path.joined(separator: "."))" }

    private var title: String { field.required == true && field.default == nil ? String(localized: "\(field.label) (required)") : field.label }

    @ViewBuilder private var control: some View {
        switch field.type {
        case "boolean":
            // With x-confirm, turning it on asks first, in the machine switches' alert (#3215); Turn On only edits the draft.
            Toggle(isOn: Binding(get: { page.value(path)?.bool ?? field.default?.bool ?? false }, set: { on in
                if on, let ask = field.confirm, !ask.isEmpty { asking = true } else { page.set(.bool(on), at: path) }
            })) {
                Text(title).lineLimit(2)
            }
            .alert("Turn On “\(field.label)”?", isPresented: $asking) {
                Button("Cancel", role: .cancel) {}
                Button(String(localized: "action.turnOn", defaultValue: "Turn On")) { page.set(.bool(true), at: path) }
            } message: {
                Text(field.confirm ?? "")
            }
        case "array":
            if field.items?.format == "machine" {
                ExtensionMachineSwitches(field: field, title: title, path: path, page: page)
            } else {
                ExtensionListField(field: field, title: title, path: path, page: page)
            }
        default:
            if let choices = field.enum {
                Picker(selection: Binding(get: { page.value(path) ?? .null }, set: { page.set($0 == .null ? nil : $0, at: path) })) {
                    Text(placeholder).tag(JSONValue.null)
                    ForEach(choices) { c in Text(c.label).tag(c.value) }
                } label: {
                    Text(title).lineLimit(2)
                }
            } else if field.picksMachine {
                // The page's hub's machines (the app's first group), as the machine switches list them.
                ExtensionChoicePicker(title: title, placeholder: placeholder, path: path, page: page,
                                      choices: ExtensionPickerChoice.machines(field, current: page.value(path)?.string,
                                                                              machines: model.machines.filter { $0.group.isEmpty }.map { ($0.id, $0.name, $0.os) }))
            } else if field.picksLauncher, let launchers = page.launchers {
                ExtensionChoicePicker(title: title, placeholder: placeholder, path: path, page: page,
                                      choices: ExtensionPickerChoice.launchers(current: page.value(path)?.string, launchers: launchers))
            } else if field.type == "integer" || field.type == "number" {
                labeled {
                    HStack(spacing: Theme.Space.s) {
                        TextField(placeholder, text: numberBinding)
                            .keyboardType(field.type == "integer" ? .numberPad : .decimalPad)
                        if field.type == "integer" {
                            // A step at a time within its bounds (#2385); typing still works for a far value.
                            Stepper(value: stepperBinding, in: stepperRange) { Text(title) }
                                .labelsHidden()
                                .fixedSize()
                        }
                    }
                }
            } else {
                labeled {
                    TextField(placeholder, text: Binding(get: { page.value(path)?.string ?? "" },
                                                         set: { page.set($0.isEmpty ? nil : .string($0), at: path) }))
                        .keyboardType(field.format == "uri" ? .URL : field.format == "email" ? .emailAddress : .default)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                    if field.picksLauncher, page.loadingLaunchers {
                        // Typed until the hub's launchers are in (or if they can't be read).
                        Text("Loading launchers…").font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1)
                    }
                }
            }
        }
    }

    /// The title above the field.
    private func labeled(@ViewBuilder _ content: () -> some View) -> some View {
        VStack(alignment: .leading, spacing: Theme.Space.xs) {
            Text(title).font(Theme.Font.labelStrong).foregroundStyle(.secondary).lineLimit(2)
            #if os(macOS)
            // A Mac form draws a field's title as its label beside an empty box: the title is the prompt here (#1221).
            content().labelsHidden()
            #else
            content()
            #endif
        }
    }

    /// What shows when it is empty: the default it falls back to, or Not set.
    private var placeholder: String {
        if let d = field.default, let s = field.enum?.first(where: { $0.value == d })?.label ?? d.string, !s.isEmpty {
            return String(localized: "Default: \(s)")
        }
        return String(localized: "Not set")
    }

    /// The stepper's value: what is set, else the default, else the lowest it may be. A step drops what was typed.
    private var stepperBinding: Binding<Double> {
        let key = path.joined(separator: ".")
        return Binding(get: { page.value(path)?.double ?? field.default?.double ?? field.minimum ?? 0 }, set: { n in
            page.numberText[key] = nil
            page.set(.number(n.rounded()), at: path)
        })
    }

    private var stepperRange: ClosedRange<Double> {
        let low = field.minimum ?? -1_000_000_000
        return low...max(low, field.maximum ?? 1_000_000_000)
    }

    /// A number as typed: kept as text until it reads as one (ExtensionModel.numberProblems), then the value.
    private var numberBinding: Binding<String> {
        let key = path.joined(separator: ".")
        return Binding(get: { page.numberText[key] ?? page.value(path)?.string ?? "" }, set: { text in
            page.numberText[key] = text
            let t = text.trimmingCharacters(in: .whitespaces)
            if t.isEmpty { page.set(nil, at: path) } else if let n = Double(t) { page.set(.number(n), at: path) }
        })
    }
}

/// One of the hub's machines or launchers by name (format machine / launcher on a string, #2385), Not set first; saved
/// with the rest of the form.
private struct ExtensionChoicePicker: View {
    var title: String
    var placeholder: String
    var path: [String]
    @Bindable var page: ExtensionModel
    var choices: [ExtensionPickerChoice]

    var body: some View {
        Picker(selection: Binding(get: { page.value(path)?.string ?? "" }, set: { page.set($0.isEmpty ? nil : .string($0), at: path) })) {
            Text(placeholder).tag("")
            ForEach(choices) { c in Text(c.label).tag(c.value) }
        } label: {
            Text(title).lineLimit(2)
        }
    }
}

/// A field's accessibility identifier on all of a row, or on none of it (`nil`: its parts carry it).
private struct ExtensionFieldIdentifier: ViewModifier {
    var id: String?

    @ViewBuilder func body(content: Content) -> some View {
        if let id { content.accessibilityIdentifier(id) } else { content }
    }
}

/// A list of the hub's machines (schema format machine, docs/extensions/extensions.md): a switch per machine of the systems the field
/// names, by its name and how it is, in effect at once (`ExtensionModel.saveNow`), the same setting as where else it
/// shows (the Control Computer switch on a Mac's page). A value that names no machine still shows, to be turned off.
private struct ExtensionMachineSwitches: View {
    @Environment(AppModel.self) private var model
    @Environment(\.splitPush) private var splitPush
    var field: ExtensionField
    var title: String
    var path: [String]
    @Bindable var page: ExtensionModel

    private var items: [String] { page.value(path)?.array?.compactMap(\.string) ?? [] }
    /// A machine waiting on the field's x-confirm before it is turned on, and its name (kept while the alert goes away).
    @State private var asking: String?
    @State private var askingName = ""
    /// The page's hub's machines (the app's first group), of the systems asked for.
    private var machines: [Machine] {
        let os = field.items?.os ?? []
        return model.machines.filter { $0.group.isEmpty && (os.isEmpty || os.contains($0.os ?? "")) }
    }
    private var unknown: [String] { items.filter { id in !model.machines.contains { $0.group.isEmpty && $0.id == id } } }
    /// A plugin for operating computers (extension point computer) or their browsers (browser): the rows say how Cua
    /// Driver or ego lite is there too.
    private var computers: Bool { page.detail?.plugin.contributes?["computer"] != nil }
    private var browsers: Bool { page.detail?.plugin.contributes?["browser"] != nil }

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            Text(title).font(Theme.Font.labelStrong).foregroundStyle(.secondary).lineLimit(2).accessibilityIdentifier(identifier)
            if machines.isEmpty && unknown.isEmpty {
                Text(field.items?.os == ["darwin"] ? String(localized: "This hub doesn't have a Mac yet.") : String(localized: "This hub doesn't have any machines yet."))
                    .font(Theme.Font.secondary).foregroundStyle(.secondary).accessibilityIdentifier(identifier)
            }
            ForEach(machines) { m in
                row(m.id, name: m.name, status: nodeStatus(m) ?? (browsers ? Self.browserStatus(m) : Self.status(m, computers: computers)))
                if computers || browsers { setUp(m) }
            }
            ForEach(unknown, id: \.self) { id in
                row(id, name: id, status: String(localized: "Can't find this machine"))
            }
        }
        // An alert, not a confirmation dialog: anchored to the row as a popover, that one drops its Cancel.
        .alert("Turn On “\(askingName)”?", isPresented: Binding(get: { asking != nil }, set: { if !$0 { asking = nil } })) {
            Button("Cancel", role: .cancel) { asking = nil }
            Button(String(localized: "action.turnOn", defaultValue: "Turn On")) {
                if let id = asking { save(id, on: true) }
                asking = nil
            }
        } message: {
            Text(field.confirm ?? "")
        }
    }

    /// Its machine page, where the plugin is set up there. A button pushing that page alone: the Macs share one row, and
    /// links in one row all open at once, every Mac's page on one tap (#2446).
    @ViewBuilder private func setUp(_ m: Machine) -> some View {
        let label = Label("Set Up [Extensions Views 1224]", systemImage: Theme.Icon.settings).font(Theme.Font.label).lineLimit(1)
        Group {
            if let splitPush {
                Button { splitPush.push(MachineDetailRoute(m.ref)) } label: { label }
            } else {
                NavigationLink(value: MachineDetailRoute(m.ref)) { label }
            }
        }
        .buttonStyle(.borderless)
        .accessibilityIdentifier("extension-machine-setup-\(m.id)")
    }

    /// The field's identifier, on its title and switches (the row leaves it to them: a setup link keeps its own).
    private var identifier: String { "plugin-field-\(path.joined(separator: "."))" }

    /// The field picking where the plugin's program runs: a machine switched on says how the program is there.
    private func nodeStatus(_ m: Machine) -> String? {
        guard let detail = page.detail, detail.nodeMachines == path.last, items.contains(m.id) else { return nil }
        let state = detail.plugin.nodes?.first { $0.machineID == m.id }
        return (m.online ? String(localized: "Online") : String(localized: "Offline [Machine Entity be1b4f]")) + " · " + ExtensionNodeState.status(state)
    }

    private func save(_ id: String, on: Bool) {
        var next = items.filter { $0 != id }
        if on {
            // A single-choice field replaces its old value in one save; asking the hub to keep both cannot succeed.
            if field.maxItems == 1 { next = [] }
            next.append(id)
        }
        Task { await page.saveNow(next.isEmpty ? nil : .array(next.map(JSONValue.string)), at: path) }
    }

    private func row(_ id: String, name: String, status: String) -> some View {
        Toggle(isOn: Binding(get: { items.contains(id) }, set: { on in
            if on, let ask = field.confirm, !ask.isEmpty { (asking, askingName) = (id, name) } else { save(id, on: on) }
        })) {
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                Text(name).lineLimit(1)
                Text(status).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1)
            }
        }
        .disabled(page.saving)
        .accessibilityIdentifier(identifier)
    }

    /// Online / Offline, then how ego lite is on it (Ready is the switch worth turning on).
    static func browserStatus(_ m: Machine) -> String {
        var parts = [m.online ? String(localized: "Online") : String(localized: "Offline [Machine Entity be1b4f]")]
        if m.online, let b = m.browser, b.checked {
            if b.job?.running == true { parts.append(b.job?.kind == "install" ? String(localized: "Installing ego") : String(localized: "Setting up ego")) }
            else if !b.installed { parts.append(String(localized: "ego not installed")) }
            else if !b.setUp { parts.append(String(localized: "ego setup not finished")) }
            else if b.ready { parts.append(String(localized: "ego ready")) }
            else { parts.append(String(localized: "ego not working")) }
        }
        return parts.joined(separator: " · ")
    }

    /// Online / Offline, then for a computer plugin how Cua Driver is on it, once its node has said (every Mac's while the
    /// plugin is on, whether or not it is turned on for it).
    static func status(_ m: Machine, computers: Bool) -> String {
        var parts = [m.online ? String(localized: "Online") : String(localized: "Offline [Machine Entity be1b4f]")]
        if computers, m.online, let c = m.computer, c.checked {
            if !c.installed {
                parts.append(String(localized: "Cua not installed"))
            } else if !c.daemon {
                parts.append(String(localized: "Cua not running"))
            } else if !c.missingPermissions.isEmpty {
                parts.append(String(localized: "Cua missing permissions"))
            } else if c.desktop == "locked" {
                parts.append(String(localized: "Screen locked"))
            } else if c.ready {
                parts.append(String(localized: "Cua ready"))
            } else {
                parts.append(String(localized: "Cua installed"))
            }
        }
        return parts.joined(separator: " · ")
    }
}

/// A list of texts (each editable, added and removed), or of choices (a switch each).
private struct ExtensionListField: View {
    var field: ExtensionField
    var title: String
    var path: [String]
    @Bindable var page: ExtensionModel
    @State private var adding = ""

    private var items: [String] { page.value(path)?.array?.compactMap(\.string) ?? field.default?.array?.compactMap(\.string) ?? [] }

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            Text(title).font(Theme.Font.labelStrong).foregroundStyle(.secondary).lineLimit(2)
            if let choices = field.items?.enum {
                ForEach(choices) { c in
                    Toggle(isOn: Binding(get: { items.contains(c.value.string ?? "") }, set: { on in
                        let v = c.value.string ?? ""
                        var next = items.filter { $0 != v }
                        if on { next.append(v) }
                        store(next)
                    })) { Text(c.label).lineLimit(1) }
                }
            } else {
                ForEach(Array(items.enumerated()), id: \.offset) { i, item in
                    HStack(spacing: Theme.Space.s) {
                        Text(item).lineLimit(1).frame(maxWidth: .infinity, alignment: .leading)
                        Button(role: .destructive) {
                            var next = items
                            next.remove(at: i)
                            store(next)
                        } label: {
                            ThemedIcon(Theme.Icon.clear).foregroundStyle(.secondary)
                        }
                        .buttonStyle(.plain)
                        .accessibilityLabel("Delete \(item)")
                    }
                }
                HStack(spacing: Theme.Space.s) {
                    TextField(String(localized: "Add item"), text: $adding)
                        #if os(macOS)
                        .labelsHidden() // the title is the prompt (#1221)
                        #endif
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .onSubmit(add)
                    Button(action: add) { ThemedIcon(Theme.Icon.add) }
                        .buttonStyle(.plain)
                        .foregroundStyle(Theme.accent)
                        .disabled(adding.trimmingCharacters(in: .whitespaces).isEmpty)
                        .accessibilityLabel("Add [Beautiful U I Bridge 7a8a11]")
                }
            }
        }
    }

    private func add() {
        let v = adding.trimmingCharacters(in: .whitespaces)
        guard !v.isEmpty else { return }
        store(items + [v])
        adding = ""
    }

    private func store(_ list: [String]) {
        page.set(list.isEmpty ? nil : .array(list.map(JSONValue.string)), at: path)
    }
}

/// Sets one of a plugin's secrets: typed in hidden, saved to the hub, never shown again; one set can be cleared.
private struct ExtensionSecretSheet: View {
    var secret: ExtensionSecret
    var save: (String) async -> ExtensionSecretSave
    /// Saves a provider's reference in its place, where the hub takes one (`extension_secret_references`).
    var saveReference: (String) async -> ExtensionSecretSave
    var clear: () async -> Void
    var close: () -> Void
    @Environment(AppModel.self) private var model
    @State private var value = ""
    /// A provider's reference (op://vault/item/field) in place of the value: on for a secret that holds one.
    @State private var byReference: Bool
    @State private var busy = false

    init(secret: ExtensionSecret, save: @escaping (String) async -> ExtensionSecretSave,
         saveReference: @escaping (String) async -> ExtensionSecretSave, clear: @escaping () async -> Void, close: @escaping () -> Void) {
        self.secret = secret
        self.save = save
        self.saveReference = saveReference
        self.clear = clear
        self.close = close
        _byReference = State(initialValue: secret.reference != nil)
        _value = State(initialValue: secret.reference?.reference ?? "")
    }
    /// The hub's words when the extension refused what was typed (#5906): shown under the field, which keeps it.
    @State private var refusal: String?

    var body: some View {
        NavigationStack {
            List {
                if model.hubFeatures.hasExtensionFeature("secret_references") {
                    Section {
                        Toggle(isOn: $byReference) { Text("Use a Reference").lineLimit(1) }
                            .onChange(of: byReference) { _, on in value = on ? (secret.reference?.reference ?? "") : "" }
                            .accessibilityIdentifier("plugin-secret-use-reference")
                    }
                }
                Section {
                    if byReference {
                        // The placeholder is a reference's shape, the same in every language.
                        TextField(String(localized: "Use a Reference"), text: $value, prompt: Text(verbatim: "op://vault/item/field"))
                            .font(Theme.Font.code)
                            .textInputAutocapitalization(.never)
                            .autocorrectionDisabled()
                            .accessibilityIdentifier("plugin-secret-reference")
                    } else {
                        SecretField(secret.set ? String(localized: "Enter a new value to replace it") : String(localized: "Enter"), filled: String(localized: "Filled in; tap Save [Extension Market Views 94348c]"), text: $value, identifier: "plugin-secret-value")
                    }
                    if let refusal {
                        Text(refusal).font(Theme.Font.label).foregroundStyle(Theme.danger).textSelection(.enabled)
                            .accessibilityIdentifier("plugin-secret-refused")
                    }
                } footer: {
                    if byReference {
                        Text("Its provider gives the value whenever this extension needs it. Persona keeps only the reference.")
                    } else {
                        Text(secret.description?.isEmpty == false ? secret.description! : String(localized: "Stored on the hub and given only to this extension."))
                    }
                }
                if secret.set {
                    Section {
                        Button(role: .destructive) {
                            Task { busy = true; await clear(); busy = false; close() }
                        } label: {
                            Text("Clear [Image Editor bce237]").lineLimit(1)
                        }
                        .tint(Theme.danger) // a Mac form draws a button in the accent whatever its role (#1512)
                        .disabled(busy)
                    }
                }
            }
            .themedPage()
            .navigationTitle(secret.title)
            .navigationBarTitleDisplayMode(.inline)
            .onChange(of: value) { if refusal != nil { refusal = nil } }
            .sheetCancel(unsaved: !value.isEmpty) { close() }
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button {
                        Task {
                            busy = true
                            switch await (byReference ? saveReference(value) : save(value)) {
                            case .saved:
                                value = ""
                                refusal = nil
                                close()
                            case .refused(let words): refusal = words
                            case .failed: refusal = nil
                            }
                            busy = false
                        }
                    } label: {
                        if busy { ProgressView() } else { Text("Save").lineLimit(1) }
                    }
                    .disabled(busy || value.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                    .accessibilityIdentifier("plugin-secret-save")
                }
            }
        }
        .presentationDetents([.medium])
        #if os(macOS)
        .macSheet()
        #endif
    }
}

/// A secret provider's page (docs/extensions/secret-providers.md): the settings that hold its references, each the
/// user's standing approval, and its Always rules for hand-offs to Agents, each removable after a confirm (the page's
/// `removingRule`). Nothing shows for an extension that is no provider.
private struct SecretProviderSections: View {
    @Environment(AppModel.self) private var model
    var users: [ExtensionSecretUser]
    var rules: [HandoffRule]
    var editable: Bool
    var remove: (HandoffRule) -> Void

    var body: some View {
        if !users.isEmpty {
            Section {
                ForEach(users) { u in
                    VStack(alignment: .leading, spacing: Theme.Space.lines) {
                        Text(verbatim: label(u)).lineLimit(1)
                        Text(verbatim: u.reference).font(Theme.Font.caption).foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle)
                    }
                    .accessibilityElement(children: .combine)
                }
            } header: {
                SettingsHeading(String(localized: "Used By"))
            } footer: {
                Text("These settings take their values from this provider. Saving a setting lets it keep taking its value.")
            }
        }
        if !rules.isEmpty {
            Section {
                ForEach(rules) { r in
                    VStack(alignment: .leading, spacing: Theme.Space.lines) {
                        Text("\(r.launcher) on \(machineName(r.machine)) [Handoff Rule]").lineLimit(1)
                        Text(verbatim: r.reference).font(Theme.Font.caption).foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle)
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .accessibilityElement(children: .combine)
                    .accessibilityIdentifier("handoff-rule")
                    // Row actions as everywhere (DESIGN_SYSTEM 3.4): a swipe and the same in the context menu, and a
                    // named action for VoiceOver; the page confirms before anything goes.
                    .swipeActions(edge: .trailing) {
                        if editable {
                            Button(role: .destructive) { remove(r) } label: { Text("Remove [Handoff Rule]") }
                        }
                    }
                    .contextMenu {
                        if editable {
                            Button(role: .destructive) { remove(r) } label: { Text("Remove [Handoff Rule]") }
                        }
                    }
                    .accessibilityAction(named: Text("Remove [Handoff Rule]")) { if editable { remove(r) } }
                }
            } header: {
                SettingsHeading(String(localized: "Always for Agents"))
            } footer: {
                Text("An Agent with this launcher on this computer gets the value without asking, when you start it yourself. Swipe a rule to remove it.")
            }
        }
    }

    private func machineName(_ id: String) -> String { model.machines.first { $0.id == id }?.name ?? id }

    private func label(_ u: ExtensionSecretUser) -> String {
        if u.kind == "launcher" {
            let name = u.launcherName ?? u.launcher ?? ""
            return u.machine.map { String(localized: "\(name) key on \(machineName($0))") } ?? String(localized: "\(name) key")
        }
        return "\(u.extensionName ?? u.extension ?? "") › \(u.secretTitle ?? u.secret ?? "")"
    }
}
