import SwiftUI

/// The host-owned settings shell on iOS and macOS. Extensions supply configuration
/// sections only; identity and lifecycle controls cannot be replaced by extension UI.
///
/// These are rows of the page's List: what the page does as a whole (reading the update's metadata and the managers,
/// the Update sheet) is `ExtensionSettingsPageWork`, on the List itself.
struct ExtensionSettingsView<Configuration: View, AdvancedInformation: View>: View {
    @Bindable var page: ExtensionModel
    let lifecycle: ExtensionSettingsLifecycle
    /// The managers, for whether this one is online (`ExtensionSettingsPageWork` reads them).
    let roster: ManagerRoster?
    let detail: HubExtensionDetail
    let hasConfiguration: Bool
    @ViewBuilder var configuration: Configuration
    @ViewBuilder var advancedInformation: AdvancedInformation

    init(page: ExtensionModel, lifecycle: ExtensionSettingsLifecycle, roster: ManagerRoster?, detail: HubExtensionDetail,
         hasConfiguration: Bool, @ViewBuilder configuration: () -> Configuration,
         @ViewBuilder advancedInformation: () -> AdvancedInformation) {
        self.page = page
        self.lifecycle = lifecycle
        self.roster = roster
        self.detail = detail
        self.hasConfiguration = hasConfiguration
        self.configuration = configuration()
        self.advancedInformation = advancedInformation()
    }

    private var policy: ExtensionSettingsPolicy { .init(detail: detail) }
    private var offline: Bool {
        guard AppModel.shared.isLive else { return true }
        guard let roster else { return false }
        let managerID = page.manager ?? roster.defaultID
        return roster.managers.first(where: { $0.id == managerID })?.online == false
    }
    private var currentUpgrade: ExtensionUpgrade? {
        lifecycle.checked ? lifecycle.candidate : (detail.plugin.upgrade ?? lifecycle.candidate)
    }
    private var busy: Bool {
        offline || lifecycle.busy || page.loading || page.saving || page.changingEnabled || page.changingAutoUpgrade || page.changingContribution != nil
    }
    private var offlineReason: String { String(localized: "The assistant is offline. Reconnect to change this extension.") }

    var body: some View {
        Group {
            enabled
            Section {
                if !hasConfiguration { Text("No configuration required").foregroundStyle(.secondary) }
            } header: {
                SettingsHeading(String(localized: "Extension configuration"))
                    .accessibilityIdentifier("extension-section-configuration")
            }
            configuration.disabled(lifecycle.busy)
            information
            advancedInformation
            updates
            // Review's download, checks and install, right under the button that started it (#3692).
            if let op = lifecycle.operation {
                ExtensionOperationSection(operation: op, name: detail.plugin.name) {
                    Task { if await lifecycle.retry() { await page.load() } }
                } review: {
                    Task {
                        do { try await lifecycle.review(op) } catch { AppModel.shared.toast = Toast(error.localizedDescription, style: .error) }
                    }
                } dismiss: {
                    Task { await lifecycle.dismissOperation() }
                }
            }
            danger
        }
    }

    private var enabled: some View {
        Section {
            Toggle(isOn: Binding(get: { detail.plugin.enabled ?? true }, set: { on in Task { await page.setEnabled(on) } })) {
                Text("Enabled").lineLimit(1)
            }
            .disabled(busy || !policy.canEnable)
            .accessibilityIdentifier("plugin-enabled")
        } header: {
            SettingsHeading(String(localized: "Enabled"))
                .accessibilityIdentifier("extension-section-enabled")
        } footer: {
            Text(enabledReason)
        }
    }

    private var enabledReason: String {
        if offline { return offlineReason }
        if policy.canEnable {
            return String(localized: "When off, this extension’s capabilities are unavailable. Your configuration is kept.")
        }
        if detail.readOnly || ["hub.json", "manager.json"].contains(detail.plugin.source ?? "") {
            return String(localized: "This extension is set in its assistant's manager.json; change its switch and settings there, then restart the assistant.")
        }
        return String(localized: "This assistant does not provide this action.")
    }

    private var information: some View {
        Section {
            identity("Version", value: detail.plugin.version)
            identity("Extension ID", value: detail.plugin.id)
            identity("Source", value: policy.source)
            identity("Runtime", value: policy.runtime)
            identity("Installed from", value: policy.provenance)
            if let attached = detail.plugin.attached {
                if let version = attached.version, !version.isEmpty { identity("Reported version", value: version) }
                if !attached.online {
                    Text(attached.error ?? String(localized: "Offline [Machine Entity be1b4f]"))
                        .font(Theme.Font.label).foregroundStyle(Theme.caution)
                }
            }
            if let sandbox = detail.plugin.sandbox {
                VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                    Text("Sandbox").font(Theme.Font.body)
                    Text(sandbox.active ? String(localized: "Sandbox is active.") : String(localized: "Sandbox is inactive."))
                        .font(Theme.Font.label).foregroundStyle(sandbox.enabled && !sandbox.active ? Theme.cautionText : Color.secondary)
                    if let reason = sandbox.detail ?? sandbox.why, !reason.isEmpty {
                        Text(reason).font(Theme.Font.label).foregroundStyle(.secondary)
                    }
                }
                .accessibilityIdentifier("extension-sandbox")
            }
            if let publisher = detail.publisher, !publisher.isEmpty { identity("Publisher", value: publisher) }
            if let home = detail.homepage, let url = URL(string: home), url.scheme?.lowercased() == "https", url.host != nil {
                Link(destination: url) { Label { Text("Website").lineLimit(1) } icon: { ThemedIcon(Theme.Icon.openExternal) } }
            }
            ForEach(detail.plugin.warnings ?? [], id: \.self) { warning in
                Label { Text(warning).font(Theme.Font.label) } icon: { ThemedIcon(Theme.Icon.error).foregroundStyle(Theme.caution) }
            }
            if let tools = detail.plugin.tools {
                ForEach(tools) { tool in
                    LabeledContent(ExtensionAuthText.toolName(tool), value: ExtensionAuthText.toolVersion(tool))
                        .accessibilityIdentifier("plugin-tool-\(tool.name)")
                }
            }
        } header: {
            SettingsHeading(String(localized: "Information"))
                .accessibilityIdentifier("extension-section-information")
        }
    }

    private func identity(_ title: LocalizedStringKey, value: String?) -> some View {
        LabeledContent(title) {
            Text(value?.isEmpty == false ? value! : String(localized: "Not provided"))
                .textSelection(.enabled)
                .multilineTextAlignment(.trailing)
        }
    }

    private var updates: some View {
        Section {
            VStack(alignment: .leading, spacing: Theme.Space.xs) {
                Text("Update status").font(Theme.Font.body)
                Text(updateStatus).font(Theme.Font.label).foregroundStyle(.secondary)
                Button { Task { if await lifecycle.checkForUpdates() { await page.load() } } } label: {
                    Label { Text("Check for updates").lineLimit(1) } icon: { ThemedIcon(Theme.Icon.retry) }
                }
                .themedButton()
                .disabled(busy || !policy.canCheck)
                .accessibilityIdentifier("extension-check-updates")
            }
            if let upgrade = currentUpgrade {
                VStack(alignment: .leading, spacing: Theme.Space.xs) {
                    ExtensionUpgradeLine(title: String(localized: "New Version \(upgrade.to) [Extensions Views fc9053]"),
                                         detail: ExtensionText.upgradeDetail(upgrade), attention: upgrade.state == "needs_permission")
                    Button { Task { await lifecycle.prepareUpgrade() } } label: {
                        Text(ExtensionText.upgradeAction(upgrade) ?? String(localized: "Update [Extensions Model 326f67]")).lineLimit(1)
                    }
                    .themedButton()
                    .disabled(busy || !policy.canCheck || !lifecycle.canReview)
                    .accessibilityIdentifier("plugin-upgrade")
                }
            }
            Toggle(isOn: Binding(get: { ExtensionText.autoUpgradeShown(detail.plugin) }, set: { on in Task { await page.setAutoUpgrade(on) } })) {
                Text("Auto-upgrade").lineLimit(1)
            }
            .disabled(busy || !policy.canFlipAutoUpgrade(globalAuto: lifecycle.globalAuto))
            .accessibilityIdentifier("plugin-auto-upgrade")
            Text(autoUpgradeReason).font(Theme.Font.label).foregroundStyle(.secondary)
            if let note = detail.plugin.upgradeNote, let text = ExtensionText.upgradeNote(note) {
                ExtensionUpgradeLine(title: text, detail: note.at.formatted(.relative(presentation: .named)), attention: note.kind == "rolled_back")
                    .accessibilityIdentifier("plugin-upgrade-note")
            }
            if let previous = detail.previousVersion, !previous.isEmpty {
                identity("Previous Version", value: previous)
                Text("This assistant does not provide a rollback preview.").font(Theme.Font.label).foregroundStyle(.secondary)
            }
            // The operation's card says what is going on once there is one; a spinner only while asking for it (or an
            // older hub's synchronous preview).
            if lifecycle.busy && lifecycle.operation == nil { ProgressView() }
            if let error = lifecycle.error {
                Text(error).font(Theme.Font.label).foregroundStyle(Theme.danger).textSelection(.enabled)
                    .accessibilityIdentifier("extension-lifecycle-error")
            }
        } header: {
            SettingsHeading(String(localized: "Updates"))
                .accessibilityIdentifier("extension-section-updates")
        }
    }

    private var updateStatus: String {
        if offline { return offlineReason }
        if !policy.canCheck { return policy.updateReason }
        if let upgrade = currentUpgrade { return ExtensionText.upgradeDetail(upgrade) }
        if lifecycle.checked { return String(localized: "Up to date") }
        return String(localized: "Check for updates to see whether a new version is available.")
    }

    private var autoUpgradeReason: String {
        if offline { return offlineReason }
        if detail.plugin.marketWasRemoved {
            return String(localized: "Auto-upgrade is paused because its market was removed. Your setting is kept and applies again once the market is added back with the same address.")
        }
        if detail.plugin.enabled == false || detail.plugin.autoUpgradePaused == "disabled" {
            return String(localized: "Auto-upgrade is paused while this extension is off.")
        }
        if !policy.canAutoUpgrade { return policy.updateReason }
        if lifecycle.globalAuto == false { return ExtensionSettingsPolicy.globalAutoOffReason }
        return String(localized: "Compatible updates run automatically. Permission changes require review.")
    }

    private var danger: some View {
        Section {
            Button(role: .destructive) {} label: {
                Label { Text("Uninstall").lineLimit(1) } icon: { ThemedIcon(Theme.Icon.delete) }
            }
            .themedButton()
            .tint(Theme.danger)
            .disabled(busy || !policy.canUninstall)
            .accessibilityIdentifier("extension-uninstall")
        } header: {
            SettingsHeading(String(localized: "Danger zone"))
                .accessibilityIdentifier("extension-section-danger")
        } footer: {
            Text(offline ? offlineReason : policy.uninstallReason)
        }
    }
}

/// What an extension's settings page does as a whole, on the page's List and never on one of its rows (#2988): reading
/// the update's metadata and the managers, and the Update sheet. A List builds its rows lazily and takes one down once
/// it scrolls off, so a sheet on a row went with it and took Settings, the shell's sheet, along: in TestFlight 220 it was
/// on a Group of the sections (a sheet per section, #2547), then on the first section, which a long page has off screen
/// by the time Review is tapped at the bottom. A task on a row also runs again each time the row comes back.
struct ExtensionSettingsPageWork: ViewModifier {
    let page: ExtensionModel
    let lifecycle: ExtensionSettingsLifecycle
    @Binding var roster: ManagerRoster?

    func body(content: Content) -> some View {
        content
            .task(id: page.detail) {
                if let detail = page.detail { await lifecycle.loadMetadata(detail: detail) }
            }
            .task(id: AppModel.shared.managersRevision) {
                if let api = AppModel.shared.api {
                    roster = await ManagerRoster.read(api.manager(nil), keeping: roster)
                }
            }
            .task { await lifecycle.loadOperations() }
            // One started earlier (the page left and opened again) went through while this page is up: read it again.
            .onChange(of: lifecycle.operationState) { old, new in
                if old == .running, new == .succeeded { Task { await page.load() } }
            }
            // Closed without Update: the operation waiting on this preview is canceled.
            .sheet(isPresented: Binding(get: { lifecycle.preview != nil }, set: { if !$0 { lifecycle.dismissPreview() } })) {
                if let preview = lifecycle.preview {
                    ExtensionInstallSheet(preview: preview, name: page.detail?.plugin.name ?? page.id, installedNames: page.memberNames) {
                        guard let consent = lifecycle.takeConsent() else {
                            let succeeded = await lifecycle.confirmUpgrade()
                            if succeeded { await page.load() }
                            return succeeded
                        }
                        // The sheet closes at once; the steps show on the page. The operation is taken before it
                        // closes, so closing does not cancel what was just agreed to.
                        Task { if await lifecycle.confirmUpgrade(preview, consent: consent) { await page.load() } }
                        return true
                    }
                }
            }
    }
}
