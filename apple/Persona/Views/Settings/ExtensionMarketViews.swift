import SwiftUI

/// Settings › Extensions › Extension Market (docs/extensions/extensions.md「Extension marketplace」): the plugins of every market the hub has, searched and filtered by
/// market; a row each with its name, version, what it does, its tags and how it stands on this hub. The markets
/// themselves are one tap away (Markets).
struct ExtensionMarketView: View {
    @State private var page = ExtensionMarketModel()
    @State private var tokenFor: ExtensionMarket?

    var body: some View {
        SettingsForm {
            // Laid out as Knowledge's kind chips under its search field: a compact section whose row has no inset of its own,
            // so the chips sit right under the search field and line up with the cards below.
            if page.markets.count > 1 {
                #if os(macOS)
                marketChips
                #else
                Section { marketChips }
                    .listSectionSpacing(.compact)
                #endif
            }
            ForEach(page.markets.filter { $0.error?.isEmpty == false }) { m in
                Section {
                    Label { Text("Couldn't read “\(m.title)”: \(m.error ?? "")") } icon: {
                        ThemedIcon(Theme.Icon.error).foregroundStyle(Theme.caution)
                    }
                    .font(Theme.Font.label)
                    // Most often GitHub's limit for a network without a token: the fix right where it shows. Not for the
                    // official market, which asks nothing of a newcomer here (docs/assistant/onboarding.md 4.5): the hub reads it
                    // elsewhere when GitHub rate-limits it, and a quiet way to Markets is there for whoever wants a token.
                    if m.wantsGitHubToken && m.official == true {
                        NavigationLink(value: SettingsPage.pluginMarkets) {
                            Text("Rate-limited? Add a GitHub token in Markets").font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1)
                                .minimumScaleFactor(0.8)
                        }
                        .accessibilityIdentifier("market-error-official-token-\(m.id)")
                    } else if m.wantsGitHubToken {
                        Button { tokenFor = m } label: { Text("Add Token").lineLimit(1) }
                            .accessibilityIdentifier("market-error-token-\(m.id)")
                    }
                } footer: {
                    if m.wantsGitHubToken && m.official != true { Text(ExtensionMarketText.gitHubTokenHint) }
                }
            }
            if let items = page.items {
                if page.markets.isEmpty {
                    Section {
                        EmptyStateView(symbol: Theme.Icon.pluginMarket, title: String(localized: "No Extension Markets Yet"),
                                       message: String(localized: "An extension market is an index of extensions published by a repository or website. Add one to list its extensions here and install them directly."))
                        NavigationLink(value: SettingsPage.pluginMarkets) { Text("Add Market").lineLimit(1) }
                            .accessibilityIdentifier("market-add-first")
                    }
                    .listRowBackground(Color.clear)
                } else if items.isEmpty {
                    Section {
                        EmptyStateView(symbol: Theme.Icon.search, title: String(localized: "No Extensions Found"),
                                       message: page.query.isEmpty ? String(localized: "This market has no extensions yet.") : String(localized: "Try another word. You can search names, descriptions, publishers and tags."))
                    }
                    .listRowBackground(Color.clear)
                } else {
                    Section {
                        ForEach(items) { p in
                            NavigationLink(value: SettingsPage.marketExtension(market: p.market, id: p.pluginID)) {
                                MarketExtensionRow(item: p, showsMarket: page.market == nil && page.markets.count > 1)
                            }
                            .accessibilityIdentifier("market-plugin-\(p.pluginID)")
                        }
                    } footer: {
                        if let p = page.platform, !p.isEmpty { Text("This hub runs \(p). Before installing, you'll see what an extension can do, and it installs only after you agree.") }
                    }
                }
            }
            if let e = page.error {
                Section {
                    LoadProblem(text: e)
                    Button { Task { await page.load() } } label: { Text("Retry [Components b8784c]").lineLimit(1) }
                }
            }
            #if os(macOS)
            // In the page, as the Mac's settings panes have their buttons (#1223): the window's toolbar put it over the
            // settings list's column.
            if !page.markets.isEmpty {
                Section {
                    NavigationLink(value: SettingsPage.pluginMarkets) {
                        Label { Text("Manage Markets").lineLimit(1) } icon: { ThemedIcon(Theme.Icon.pluginMarket) }
                    }
                    .accessibilityIdentifier("market-manage")
                }
            }
            #endif
        }
        .themedPage()
        #if os(iOS) || os(visionOS)
        // The chips come right under the search field: the list's own top margin (a first section's header band) made a
        // gap several times the page's other spacing.
        .contentMargins(.top, Theme.Space.s, for: .scrollContent)
        #endif
        .overlay { if page.items == nil && page.error == nil { ProgressView() } }
        .detailTitle(String(localized: "Extension Market"))
        .navigationBarTitleDisplayMode(.inline)
        .pageSearchable(text: $page.query, placement: .navigationBarDrawer(displayMode: .always), prompt: String(localized: "Name, description, publisher or tag"))
        #if os(iOS) || os(visionOS)
        .pageToolbar {
            ToolbarItem(placement: .topBarTrailing) {
                NavigationLink(value: SettingsPage.pluginMarkets) { Text("Markets").lineLimit(1) }
                    .accessibilityIdentifier("market-manage")
            }
        }
        #endif
        // The search waits for the typing to pause; the first read goes at once.
        .task(id: page.query) {
            if page.items != nil { try? await Task.sleep(for: .milliseconds(300)) }
            guard !Task.isCancelled else { return }
            await page.load()
        }
        .refreshable { await page.load(refresh: true) }
        .sheet(item: $tokenFor) { m in
            MarketTokenSheet(title: String(localized: "Token for “\(m.title)”"), asksSpec: false, hasToken: m.hasToken, marketSpec: m.spec) { _, token in
                if let why = await page.setToken(m.id, token: token) { return why }
                await page.load()
                return nil
            }
        }
    }

    private var marketChips: some View {
        FlowLayoutRow {
            FilterChip(title: String(localized: "All"), on: page.market == nil) { choose(nil) }
                .accessibilityIdentifier("market-filter-all")
            ForEach(page.markets) { m in
                FilterChip(title: m.title, on: page.market == m.id) { choose(m.id) }
                    .accessibilityIdentifier("market-filter-\(m.id)")
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .listRowBackground(Color.clear)
        .listRowInsets(EdgeInsets(top: Theme.Space.xxs, leading: 0, bottom: Theme.Space.xxs, trailing: 0))
    }

    private func choose(_ market: String?) {
        guard page.market != market else { return }
        page.market = market
        Task { await page.load() }
    }
}

/// A plugin of the catalog: name and version, what it does, its tags, how it stands here.
private struct MarketExtensionRow: View {
    var item: ExtensionCatalogItem
    var showsMarket: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.xs) {
            HStack(spacing: Theme.Space.s) {
                Text(item.name).font(Theme.Font.bodyStrong).lineLimit(1)
                Text(item.version).font(Theme.Font.label.monospacedDigit()).foregroundStyle(.secondary).lineLimit(1)
                Spacer(minLength: Theme.Space.s)
                MarketStateChip(item: item)
            }
            if let d = item.description, !d.isEmpty {
                Text(d).font(Theme.Font.secondary).foregroundStyle(.secondary).lineLimit(2)
            }
            let tags = (showsMarket ? [item.marketName ?? ""] : []) + (item.tags ?? [])
            if tags.contains(where: { !$0.isEmpty }) {
                FlowLayoutRow {
                    ForEach(Array(tags.filter { !$0.isEmpty }.enumerated()), id: \.offset) { i, t in
                        Chip(text: t, symbol: showsMarket && i == 0 ? Theme.Icon.pluginMarket : nil, look: showsMarket && i == 0 ? .outline : .neutral)
                    }
                }
            }
            if let p = item.problem, !p.isEmpty {
                Text(p).font(Theme.Font.label).foregroundStyle(Theme.danger).lineLimit(2)
            }
        }
        .padding(.vertical, Theme.Space.xxs)
    }
}

/// Available / Update / Installed / Can't Install, in its tone.
private struct MarketStateChip: View {
    var item: ExtensionCatalogItem

    var body: some View {
        if let op = ExtensionOperations.shared.operation(for: item.pluginID, market: item.market), let text = ExtensionOperationText.chip(op) {
            Chip(text: text, look: .tint(op.state == .failed ? Theme.danger : Theme.accent))
        } else {
            stateChip
        }
    }

    @ViewBuilder private var stateChip: some View {
        switch ExtensionMarketText.step(item) {
        case .install: Chip(text: String(localized: "Available [Extension Market Model 06c13d]"), look: .tint(Theme.accent))
        case .upgrade: Chip(text: String(localized: "Update [Extension Market Views 8a63ee]"), look: .tint(Theme.caution))
        case .upToDate: Chip(text: String(localized: "Installed [Extension Market Views a8b6c3]"), look: .tint(Theme.positive))
        case .unavailable: Chip(text: String(localized: "Can't Install"), look: .neutral)
        }
    }
}

/// Extension Market › one plugin: what it is, what it may do, and the one thing to do with it — install, upgrade (with what the
/// new version asks for in addition), or remove when it came from this market.
struct MarketExtensionDetailView: View {
    @Environment(AppModel.self) private var app
    @Environment(\.dynamicTypeSize) private var typeSize
    @State private var page: MarketExtensionModel
    @State private var confirmRemove = false
    /// The install ended waiting for a sign-in: the extension's page opens over this one.
    @State private var openSignIn = false

    init(market: String, id: String) { _page = State(initialValue: MarketExtensionModel(market: market, id: id)) }

    var body: some View {
        SettingsForm {
            if let p = page.item {
                header(p)
                if let op = page.operation {
                    ExtensionOperationSection(operation: op, name: p.name, partial: page.partialLines(for: op)) {
                        Task { if await page.retry() { done(op.kind) } }
                    } review: {
                        Task {
                            do { try await page.review(op) } catch { app.toast = Toast(error.localizedDescription, style: .error) }
                        }
                    } dismiss: {
                        Task { await page.dismissOperation() }
                    }
                }
                actions(p)
                if let perms = p.permissions, !perms.isEmpty {
                    Section {
                        ForEach(perms) { PermissionLine(permission: $0) }
                    } header: {
                        SettingsHeading(String(localized: "It Can"))
                    } footer: {
                        Text("This is listed again before installing, based on the downloaded package. If a new version asks for more, the extra permissions are listed before the update.")
                    }
                }
                about(p)
            } else if let e = page.error {
                Section {
                    EmptyStateView(symbol: Theme.Icon.error, title: String(localized: "Couldn't Load This Extension"), message: e,
                                   action: (String(localized: "Retry [Components b8784c]"), { Task { await page.load() } }))
                }
                .listRowBackground(Color.clear)
            }
        }
        .themedPage()
        .overlay { if page.item == nil && page.error == nil { ProgressView() } }
        .detailTitle(page.item?.name ?? String(localized: "Extensions"))
        .navigationBarTitleDisplayMode(.inline)
        .confirmationDialog("Turn Off Dependent Extensions and Uninstall?", isPresented: Binding(
            get: { page.uninstallConfirmation != nil }, set: { if !$0 { page.uninstallConfirmation = nil } }),
            titleVisibility: .visible) {
            Button(role: .destructive) {
                Task {
                    if await page.uninstall(cascade: true, members: page.removingMembers) { done(.uninstall) }
                }
            } label: { Text("Turn Off Dependents and Uninstall").lineLimit(1) }
            .accessibilityIdentifier("market-uninstall-cascade")
            Button("Cancel", role: .cancel) {}
        } message: {
            Text(page.uninstallConfirmation?.message ?? "")
        }
        .sheet(item: $page.removalNotice) { notice in
            ExtensionRemovalNoticeSheet(notice: notice) { page.acknowledgeRemoval() }
        }
        #if DEBUG
        .task { if UserDefaults.standard.bool(forKey: "showRemovalWarnings") { page.showSampleRemovalNotice() } }
        #endif
        // Closed without Install: the operation waiting on this preview is canceled.
        .sheet(item: $page.preview, onDismiss: { page.declined() }) { pv in
            ExtensionInstallSheet(preview: pv, name: page.item?.name ?? pv.manifest.name ?? pv.manifest.id, installedNames: page.memberNames) {
                // The sheet closes at once; the steps show on this page (and stay if it is left). The operation is
                // taken before it closes, so its onDismiss does not cancel what was just agreed to.
                let kind: ExtensionOperation.Kind = pv.upgrade == true ? .upgrade : .install
                let consent = page.takeConsent()
                Task { if await page.confirm(pv, consent: consent) { done(kind) } }
                return true
            }
        }
        .task {
            async let ops: Void = page.loadOperations()
            await page.load()
            await ops
        }
        .refreshable {
            async let ops: Void = page.loadOperations()
            await page.load()
            await ops
        }
        // One started earlier (the page left and opened again) ends while this page is up: read it again.
        .onChange(of: ExtensionOperations.shared.operation(for: page.pluginID, market: page.market)?.state) { old, new in
            guard old == .running, new == .succeeded else { return }
            Task { await page.load() }
            // Installed and waiting for a sign-in (#4507): its page, with the sign-in started.
            if ExtensionOperations.shared.operation(for: page.pluginID, market: page.market)?.needsSignIn == true {
                ExtensionAuthStore.shared.openSignIn = page.pluginID
                openSignIn = true
            }
        }
        .navigationDestination(isPresented: $openSignIn) { ExtensionDetailView(id: page.pluginID) }
    }

    private func done(_ kind: ExtensionOperation.Kind) {
        // Cleanup left to do: its sheet says so and stays until closed (#4819), no toast over it.
        if kind == .uninstall, page.removalNotice != nil { Haptics.attention(); return }
        Haptics.success()
        app.toast = Toast(kind == .uninstall ? page.removalMessage : ExtensionOperationText.title(
            ExtensionOperation(id: "", kind: kind, extensionID: "", state: .succeeded)), style: .success)
    }

    @ViewBuilder private func header(_ p: ExtensionCatalogItem) -> some View {
        Section {
            // At the accessibility sizes the name and its line wrap and the state goes under them, or the chip beside them
            // left "Ex…" (#4058).
            let large = typeSize.isAccessibilitySize
            HStack(spacing: Theme.Space.m) {
                Image(systemName: Theme.Icon.plugins).resizable().scaledToFit().foregroundStyle(Theme.accent)
                    .scaledFrame(width: Theme.Size.wellM, height: Theme.Size.wellM)
                VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                    Text(p.name).font(Theme.Font.heading).lineLimit(large ? nil : 1)
                    Text([p.version, p.publisher, p.marketName].compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: " · "))
                        .font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(large ? nil : 1)
                    if large { MarketStateChip(item: p) }
                }
                Spacer(minLength: Theme.Space.s)
                if !large { MarketStateChip(item: p) }
            }
            .padding(.vertical, Theme.Space.xs)
            if let d = p.description, !d.isEmpty { Text(d).font(Theme.Font.secondary).foregroundStyle(.secondary) }
            if let tags = p.tags, !tags.isEmpty {
                FlowLayoutRow { ForEach(tags, id: \.self) { Chip(text: $0) } }
            }
            if let e = page.error { Text(e).font(Theme.Font.label).foregroundStyle(Theme.danger).textSelection(.enabled) }
        }
    }

    @ViewBuilder private func actions(_ p: ExtensionCatalogItem) -> some View {
        Section {
            switch ExtensionMarketText.step(p) {
            case .install:
                primary(String(localized: "Install"), symbol: Theme.Icon.install, id: "market-install")
            case .upgrade:
                primary(String(localized: "Update to \(p.version)"), symbol: Theme.Icon.upgrade, id: "market-upgrade")
            case .upToDate:
                LabeledContent("Installed [Extension Market Views a8b6c3]", value: p.installed ?? p.version)
            case .unavailable:
                Label { Text(p.problem ?? String(localized: "Can't install on this hub")) } icon: {
                    ThemedIcon(Theme.Icon.error).foregroundStyle(Theme.caution)
                }
                .font(Theme.Font.label)
            }
            if p.installed != nil {
                NavigationLink(value: SettingsPage.plugin(p.pluginID)) {
                    Label { Text("Open Extension Settings").lineLimit(1) } icon: { ThemedIcon(Theme.Icon.settings) }
                }
                .accessibilityIdentifier("market-open-plugin")
            }
            if page.canUninstall {
                Button(role: .destructive) { confirmRemove = true } label: {
                    Label { Text("Uninstall").lineLimit(1) } icon: { ThemedIcon(Theme.Icon.delete) }
                }
                .tint(Theme.danger) // a Mac form draws a button in the accent whatever its role (#1512)
                .disabled(page.working)
                .accessibilityIdentifier("market-uninstall")
                .confirmationDialog("Uninstall “\(p.name)”?", isPresented: $confirmRemove, titleVisibility: .visible) {
                    Button(role: .destructive) {
                        Task { if await page.uninstall() { done(.uninstall) } }
                    } label: { Text(page.isPack ? String(localized: "Uninstall Pack Only") : String(localized: "Uninstall")).lineLimit(1) }
                    .accessibilityIdentifier("market-uninstall-confirm")
                    if page.isPack {
                        Button(role: .destructive) {
                            Task { if await page.uninstall(members: true) { done(.uninstall) } }
                        } label: { Text("Uninstall with Members").lineLimit(1) }
                        .accessibilityIdentifier("market-uninstall-members")
                    }
                    Button("Cancel", role: .cancel) {}
                } message: {
                    Text(page.uninstallMessage)
                }
            }
        } footer: {
            if p.installed != nil && !page.canUninstall && p.installedSource != nil {
                Text("This extension wasn't installed from this market. Uninstall it in the hub's admin page (/admin) or on the hub machine.")
            }
        }
    }

    private func primary(_ title: String, symbol: String, id: String) -> some View {
        FormAction {
            Button { Task { await page.prepare() } } label: {
                HStack(spacing: Theme.Space.s) {
                    if page.previewing { ProgressView().controlSize(.small) } else { ThemedIcon(symbol) }
                    Text(title).lineLimit(1)
                }
                .formActionWidth()
            }
            .themedButton(prominent: true)
            .disabled(page.working)
            .accessibilityIdentifier(id)
        }
    }

    @ViewBuilder private func about(_ p: ExtensionCatalogItem) -> some View {
        Section {
            LabeledContent("Version", value: p.version)
            if let v = p.installed, !v.isEmpty { LabeledContent("Installed Version", value: v) }
            if let m = p.marketName, !m.isEmpty { LabeledContent("Markets", value: m) }
            if let pub = p.publisher, !pub.isEmpty { LabeledContent("Publisher", value: pub) }
            if let c = p.contributes, !c.isEmpty {
                ForEach(c.keys.sorted(), id: \.self) { point in
                    LabeledContent(ExtensionText.point(point)) {
                        Text((c[point] ?? []).joined(separator: "、")).lineLimit(2).multilineTextAlignment(.trailing)
                    }
                }
            }
            if let h = p.homepage, let url = URL(string: h) {
                Link(destination: url) { Label("Website", systemImage: Theme.Icon.openExternal).lineLimit(1) }
            }
        } header: {
            SettingsHeading(String(localized: "About"))
        }
    }
}

/// A line of what a plugin may do: what, and the detail under it.
private struct PermissionLine: View {
    var permission: ExtensionPermission
    var tone: Color? = nil
    /// The package's command-line programs (#4507): the Install command-line programs line lists them one per line, with where they come
    /// from and where they go, instead of the hub's sentence.
    var tools: [ExtensionToolSpec]? = nil

    private var programs: [ExtensionToolSpec] {
        permission.key == "tools" && !permission.title.contains("更多") ? tools ?? [] : [] // i18n-ignore
    }

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.xxs) {
            Text(ExtensionText.permissionTitle(permission)).font(Theme.Font.body).foregroundStyle(tone ?? Color.primary)
            if !programs.isEmpty {
                ForEach(programs, id: \.name) { t in
                    HStack(spacing: Theme.Space.s) {
                        ThemedIcon(Theme.Icon.terminal).font(Theme.Font.label).foregroundStyle(.secondary)
                        Text(ExtensionAuthText.spec(t)).font(Theme.Font.label)
                    }
                }
                Text(ExtensionAuthText.toolsNote).font(Theme.Font.label).foregroundStyle(.secondary)
            } else if let d = permission.detail, !d.isEmpty {
                Text(d).font(Theme.Font.label).foregroundStyle(.secondary)
            }
        }
        .padding(.vertical, Theme.Space.xxs)
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("permission-\(permission.key)")
    }
}

/// The preview to agree to: the package downloaded and checked, what it may do (for an upgrade, what it asks for in
/// addition first), then Install / Update.
/// A pack member's row (#4122): its name, and under it its id beside the version and market (only the version and
/// market under the id of one not installed). The lines wrap at the accessibility sizes.
struct ExtensionPackMemberRow: View {
    var line: ExtensionPackMemberLine
    @Environment(\.dynamicTypeSize) private var typeSize

    var body: some View {
        let large = typeSize.isAccessibilitySize
        VStack(alignment: .leading, spacing: Theme.Space.xxs) {
            Text(line.title).font(Theme.Font.body).lineLimit(large ? nil : 1)
            if let detail = line.detail {
                Text(detail).font(Theme.Font.label).foregroundStyle(.secondary)
                    .lineLimit(large ? nil : 1).truncationMode(.middle)
            }
        }
        .accessibilityElement(children: .combine)
    }
}

struct ExtensionInstallSheet: View {
    var preview: ExtensionPreview
    var name: String
    /// The installed extensions' names by id, for a pack's members (#4122); the ones installed with it are named by
    /// their own manifests.
    var installedNames: [String: String] = [:]
    var confirm: () async -> Bool
    @Environment(\.dismiss) private var dismiss
    @State private var busy = false
    @State private var previewTime = Date.now

    private var upgrade: Bool { preview.upgrade == true }

    var body: some View {
        NavigationStack {
            List {
                Section {
                    LabeledContent(upgrade ? String(localized: "Update [Extension Market Views 94996c]") : String(localized: "Version"),
                                   value: upgrade ? "\(preview.from ?? "") → \(preview.manifest.version ?? "")" : preview.manifest.version ?? "")
                    if let d = preview.manifest.description, !d.isEmpty { Text(d).font(Theme.Font.secondary).foregroundStyle(.secondary) }
                }
                if let dependencies = preview.depends, !dependencies.isEmpty {
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
                if let members = preview.manifest.extensionPack, !members.isEmpty {
                    let names = ExtensionPackMemberLine.names(installed: installedNames, preview: preview)
                    Section {
                        ForEach(members, id: \.id) { member in
                            ExtensionPackMemberRow(line: ExtensionPackMemberLine(member, name: names[member.id]))
                        }
                    } header: { SettingsHeading(String(localized: "Pack Members")) }
                }
                if let bundle = preview.bundle, !bundle.isEmpty {
                    ForEach(bundle) { member in
                        Section {
                            LabeledContent(member.manifest.name ?? member.manifest.id, value: member.manifest.version ?? "")
                            ForEach(member.permissions) { PermissionLine(permission: $0, tools: member.manifest.tools) }
                        } header: { SettingsHeading(String(localized: "Installed Together")) }
                    }
                }
                if let unresolved = preview.unresolved, !unresolved.isEmpty {
                    Section {
                        Text("Not found in any market: \(unresolved.joined(separator: "、"))")
                            .font(Theme.Font.secondary).foregroundStyle(Theme.cautionText)
                    } footer: { Text("Add the markets that provide these extensions, then preview again. Nothing is installed until its dependencies are.") }
                }
                if let problem = preview.confirmationProblem(at: previewTime) {
                    Section {
                        Text(problem).font(Theme.Font.secondary).foregroundStyle(Theme.cautionText)
                            .accessibilityIdentifier("market-preview-problem")
                    }
                }
                if upgrade, let added = preview.addedPermissions, !added.isEmpty {
                    Section {
                        ForEach(added) { PermissionLine(permission: $0, tone: Theme.caution) }
                    } header: {
                        SettingsHeading(String(localized: "New Version Also Asks For"))
                    } footer: {
                        Text("Updating means agreeing to these.")
                    }
                }
                Section {
                    if preview.permissions.isEmpty {
                        Text(preview.manifest.extensionPack?.isEmpty == false
                             ? String(localized: "The pack itself asks for no extra permissions; each member runs with its own permissions above.") : String(localized: "This package asks for no extra permissions."))
                            .font(Theme.Font.secondary).foregroundStyle(.secondary)
                    } else {
                        ForEach(preview.permissions) { PermissionLine(permission: $0, tools: preview.manifest.tools) }
                    }
                } header: {
                    SettingsHeading(upgrade ? String(localized: "New Version Can") : String(localized: "It Can"))
                } footer: {
                    Text("The extension runs with the permissions listed in the preview and the hub's isolation settings. The package was verified against the market's checksum.")
                }
                if let w = preview.warnings, !w.isEmpty {
                    Section {
                        ForEach(w, id: \.self) { t in
                            Label { Text(t) } icon: { ThemedIcon(Theme.Icon.error).foregroundStyle(Theme.caution) }
                                .font(Theme.Font.label)
                        }
                    }
                }
            }
            .themedPage()
            .navigationTitle(upgrade ? String(localized: "Update \(name)") : String(localized: "Install \(name)"))
            .navigationBarTitleDisplayMode(.inline)
            .sheetCancel(unsaved: false) { dismiss() }
            .task(id: preview.expirationBoundary) {
                previewTime = .now
                guard let expires = preview.expirationBoundary else { return }
                if expires > .now { try? await Task.sleep(for: .seconds(expires.timeIntervalSinceNow)) }
                guard !Task.isCancelled else { return }
                previewTime = .now
            }
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button {
                        Task {
                            busy = true
                            if await confirm() { dismiss() }
                            busy = false
                        }
                    } label: {
                        if busy { ProgressView() } else { Text(upgrade ? String(localized: "Update [Extension Market Views 94996c]") : String(localized: "Install")).lineLimit(1) }
                    }
                    .disabled(busy || preview.confirmationProblem(at: previewTime) != nil)
                    .accessibilityIdentifier("market-confirm")
                }
            }
        }
        #if os(macOS)
        .macSheet()
        #endif
    }
}

/// Extension Market › Markets: the markets the hub reads, each with what it is, how many plugins, when it was read and what went
/// wrong; added with an address and an optional token (written, never shown), refreshed, given a new token, removed.
struct ExtensionMarketsView: View {
    @State private var page = ExtensionMarketModel()
    @State private var adding = false
    @State private var tokenFor: ExtensionMarket?

    var body: some View {
        SettingsForm {
            if page.markets.isEmpty && !page.loading && page.error == nil {
                Section {
                    EmptyStateView(symbol: Theme.Icon.pluginMarket, title: String(localized: "No Extension Markets Yet"),
                                   message: String(localized: "Enter a market's address: an https://… index, github:owner/repo (a private repo needs a read-only token; a public one is rate-limited less by GitHub with one) or git:… (uses the hub machine's SSH credentials)."),
                                   action: (String(localized: "Add Market"), { adding = true }))
                }
                .listRowBackground(Color.clear)
            }
            if let auto = page.autoUpgrade, !page.markets.isEmpty {
                Section {
                    Toggle(isOn: Binding(get: { auto }, set: { on in Task { await page.setAutoUpgrade(on) } })) {
                        Text("Auto-Update Extensions").lineLimit(1)
                    }
                    .accessibilityIdentifier("market-auto-upgrade")
                } footer: {
                    Text(auto ? String(localized: "When an extension from these markets has a new version, the hub installs it while the extension is idle, and asks you first if it wants more permissions. You can turn this off for each extension on its own page.")
                              : String(localized: "Off: new versions are only shown on the extension's page, and you update it yourself."))
                }
            }
            ForEach(page.markets) { m in
                Section {
                    MarketRow(market: m, busy: page.busyMarket == m.id, removalCount: page.removalMarket == m.id ? page.removalCount : nil,
                              prepareRemoval: { await page.prepareRemoval(of: m.id) }) { act in
                        switch act {
                        case .refresh: Task { await page.refreshMarket(m.id) }
                        case .token: tokenFor = m
                        case .remove: Task { await page.removeMarket(m.id) }
                        }
                    }
                }
            }
            if let e = page.error {
                Section { LoadProblem(text: e) }
            }
            #if os(macOS)
            Section {
                HStack {
                    Spacer(minLength: 0)
                    Button("Add Market…") { adding = true }.lineLimit(1).formButton()
                        .accessibilityIdentifier("market-add")
                }
            }
            #endif
        }
        .themedPage()
        .overlay { if page.markets.isEmpty && page.loading { ProgressView() } }
        .detailTitle(String(localized: "Markets"))
        .navigationBarTitleDisplayMode(.inline)
        #if os(iOS) || os(visionOS)
        .pageToolbar {
            ToolbarItem(placement: .topBarTrailing) {
                Button { adding = true } label: { ThemedIcon(Theme.Icon.add) }
                    .accessibilityLabel("Add Market")
                    .accessibilityIdentifier("market-add")
            }
        }
        #endif
        .sheet(isPresented: $adding) {
            MarketTokenSheet(title: String(localized: "Add Market"), asksSpec: true, hasToken: false) { spec, token in
                await page.addMarket(spec: spec, token: token)
            }
        }
        .sheet(item: $tokenFor) { m in
            MarketTokenSheet(title: String(localized: "Token for “\(m.title)”"), asksSpec: false, hasToken: m.hasToken, marketSpec: m.spec) { _, token in
                await page.setToken(m.id, token: token)
            }
        }
        .task { await page.loadMarkets(); await page.loadAutoUpgrade() }
        .refreshable { await page.loadMarkets(); await page.loadAutoUpgrade() }
    }
}

/// A market: its name and address, how many plugins and when it was read, its error and warnings; then its actions
/// (refresh now, a token, remove) in a row of their own.
private struct MarketRow: View {
    enum Act { case refresh, token, remove }
    var market: ExtensionMarket
    var busy: Bool
    var removalCount: Int?
    var prepareRemoval: () async -> Bool
    var act: (Act) -> Void
    @State private var confirmRemove = false
    @State private var preparingRemoval = false
    @State private var removalAttempt = 0

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.xs) {
            HStack(spacing: Theme.Space.s) {
                Text(market.title).font(Theme.Font.bodyStrong).lineLimit(1)
                Spacer(minLength: Theme.Space.s)
                if busy || preparingRemoval { ProgressView().controlSize(.small) }
            }
            Text(market.spec).font(Theme.Font.code).foregroundStyle(.secondary).lineLimit(2).truncationMode(.middle).textSelection(.enabled)
            if let d = market.description, !d.isEmpty { Text(d).font(Theme.Font.secondary).foregroundStyle(.secondary) }
            Text(ExtensionMarketText.marketSummary(market)).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1)
            if let e = market.error, !e.isEmpty {
                Label { Text(e) } icon: { ThemedIcon(Theme.Icon.error) }
                    .font(Theme.Font.label)
                    .foregroundStyle(Theme.danger)
                if market.wantsGitHubToken {
                    Text(ExtensionMarketText.gitHubTokenHint).font(Theme.Font.label).foregroundStyle(.secondary)
                }
            }
            ForEach(market.warnings ?? [], id: \.self) { w in
                Label { Text(w) } icon: { ThemedIcon(Theme.Icon.error).foregroundStyle(Theme.caution) }
                    .font(Theme.Font.label)
            }
        }
        .padding(.vertical, Theme.Space.xxs)
        HStack(spacing: Theme.Space.l) {
            action(String(localized: "Refresh"), .refresh, id: "market-refresh-\(market.id)")
            action(market.hasToken ? String(localized: "Change Token") : String(localized: "Set Token"), .token, id: "market-token-\(market.id)")
            Spacer(minLength: 0)
            Button(role: .destructive) {
                removalAttempt += 1
                let attempt = removalAttempt
                Task {
                    preparingRemoval = true
                    let prepared = await prepareRemoval()
                    guard attempt == removalAttempt else { return }
                    preparingRemoval = false
                    if prepared { confirmRemove = true }
                }
            } label: { Text("Delete").lineLimit(1) }
            .buttonStyle(.borderless)
            .foregroundStyle(Theme.danger) // a Mac borderless button draws its role in the accent
            .accessibilityIdentifier("market-remove-\(market.id)")
            .confirmationDialog("Delete market “\(market.title)”?", isPresented: $confirmRemove, titleVisibility: .visible) {
                Button(role: .destructive) { act(.remove) } label: { Text("Delete").lineLimit(1) }
                    .accessibilityIdentifier("market-remove-confirm")
                Button("Cancel", role: .cancel) {}
            } message: {
                Text(ExtensionMarketText.removalReason(removalCount))
                    .accessibilityIdentifier("market-remove-note")
            }
        }
        .font(Theme.Font.secondary)
        .disabled(busy || preparingRemoval)
    }

    /// Several buttons in one row of a list: borderless, or the whole row is one button.
    private func action(_ title: String, _ a: Act, id: String) -> some View {
        Button { act(a) } label: { Text(title).lineLimit(1) }
        .buttonStyle(.borderless)
        .accessibilityIdentifier(id)
    }
}

/// Adding a market (its address and an optional token), or a market's new token: the token is typed in hidden and never
/// shown again; the hub reads the index before it answers, and why it could not stays on the sheet.
/// An uninstall that went through with cleanup left (#4819): it is uninstalled, and every warning with its path
/// stays readable and selectable until OK. Not swiped away by accident: closing it is the acknowledgement.
private struct ExtensionRemovalNoticeSheet: View {
    var notice: ExtensionRemovalNotice
    var done: () -> Void

    var body: some View {
        NavigationStack {
            List {
                Section {
                    NoticeBanner(text: String(localized: "Uninstalled. A few things still need you to clean them up by hand."))
                        .listRowInsets(EdgeInsets(top: 0, leading: Theme.Space.page, bottom: 0, trailing: Theme.Space.page))
                        .listRowSeparator(.hidden)
                        .listRowBackground(Color.clear)
                }
                Section {
                    ForEach(Array(notice.warnings.enumerated()), id: \.offset) { _, warning in
                        Text(warning)
                            .font(Theme.Font.secondary)
                            .textSelection(.enabled)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                } header: {
                    SettingsHeading(String(localized: "Needs manual cleanup"))
                }
            }
            .themedPage()
            .navigationTitle(notice.title)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button(action: done) { Text("OK [Recordings Section de32e2]").lineLimit(1) }
                        .accessibilityIdentifier("market-removal-done")
                }
            }
        }
        .interactiveDismissDisabled()
        .presentationDetents([.medium, .large])
        #if os(macOS)
        .macSheet()
        #endif
    }
}

private struct MarketTokenSheet: View {
    var title: String
    var asksSpec: Bool
    var hasToken: Bool
    /// The market's address when it has one (a new token): a GitHub market's sheet says how to get a token.
    var marketSpec = ""
    /// nil: done; otherwise why not.
    var save: (_ spec: String, _ token: String) async -> String?
    @Environment(\.dismiss) private var dismiss
    @State private var spec = ""
    @State private var token = ""
    @State private var busy = false
    @State private var problem: String?

    var body: some View {
        NavigationStack {
            List {
                if asksSpec {
                    Section {
                        // Sent as typed: the hub reads it and says what is wrong (#1373), the app does not judge the form.
                        TextField(String(localized: "https://…, github:owner/repo or git:…"), text: $spec)
                            .textInputAutocapitalization(.never)
                            .autocorrectionDisabled()
                            .accessibilityIdentifier("market-spec")
                    } header: {
                        SettingsHeading(String(localized: "Address"))
                    } footer: {
                        Text(String(localized: "Three forms: https://example.com/extensions/ (an address not ending in .json reads persona-extensions.json under it); github:owner/repo (the one at the repo root; private repos need a token); git:<address>[#branch] (git@host:path.git, ssh://…, https://…; uses the hub machine's own SSH credentials, no token)."))
                    }
                }
                Section {
                    SecretField(hasToken ? String(localized: "Enter a new token to replace it") : (asksSpec ? String(localized: "Not needed for public markets") : String(localized: "Filled in; tap Save [Extension Market Views 8b5050]")), filled: String(localized: "Filled in; tap Save [Extension Market Views 94348c]"),
                                text: $token, identifier: "market-token")
                } header: {
                    SettingsHeading(asksSpec ? String(localized: "Token (Optional)") : "Token")
                } footer: {
                    Text("GitHub markets: a private repo needs a read-only token (read access to this repo's Contents). A token helps a public repo too: it raises the limit from 60 reads an hour (shared by your whole network) to 5,000. Choose Public repositories with no permissions. It's stored encrypted on the hub, sent only to the market itself, and not shown again once saved. git: markets use the hub machine's SSH credentials and don't need one.")
                }
                if (asksSpec ? spec : marketSpec).trimmingCharacters(in: .whitespaces).hasPrefix("github:") {
                    Section {
                        // Safari, not the app's browser: GitHub's page needs the user's GitHub session.
                        Link(destination: ExtensionMarketText.gitHubTokenURL) {
                            Label { Text("Create on GitHub").lineLimit(1) } icon: { ThemedIcon(Theme.Icon.openExternal) }
                        }
                        .accessibilityIdentifier("market-token-create")
                    } footer: {
                        Text("No token yet? Create one on GitHub: the name, purpose and a one-year expiry are filled in and no permissions are needed. Tap Generate token, then paste it above.")
                    }
                }
                if !asksSpec && hasToken {
                    Section {
                        Button(role: .destructive) { submit(clearing: true) } label: { Text("Remove Token").lineLimit(1) }
                            .tint(Theme.danger)
                            .disabled(busy)
                    }
                }
                if let problem {
                    Section {
                        Label { Text(problem).textSelection(.enabled) } icon: { ThemedIcon(Theme.Icon.error) }
                            .font(Theme.Font.label)
                            .foregroundStyle(Theme.danger)
                    }
                }
            }
            .themedPage()
            .navigationTitle(title)
            .navigationBarTitleDisplayMode(.inline)
            .sheetCancel(unsaved: !spec.isEmpty || !token.isEmpty) { dismiss() }
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button { submit(clearing: false) } label: {
                        if busy { ProgressView() } else { Text(asksSpec ? String(localized: "Add [Beautiful U I Bridge 7a8a11]") : String(localized: "Save")).lineLimit(1) }
                    }
                    .disabled(busy || (asksSpec ? spec.trimmingCharacters(in: .whitespaces).isEmpty : token.trimmingCharacters(in: .whitespaces).isEmpty))
                    .accessibilityIdentifier("market-save")
                }
            }
        }
        .presentationDetents([.medium, .large])
        #if os(macOS)
        .macSheet()
        #endif
    }

    private func submit(clearing: Bool) {
        Task {
            busy = true
            problem = await save(spec, clearing ? "" : token)
            busy = false
            if problem == nil { dismiss() }
        }
    }
}
