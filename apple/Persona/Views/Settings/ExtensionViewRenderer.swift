import SwiftUI

/// An extension's View drawn with the app's design system (docs/extensions/extensions.md「Declarative UI」「UI 版本 3」): each section a
/// section of the form (a divider starts another one), each component a row: text, Markdown, a number with what it is,
/// a bar, a list row (with an icon, an avatar or a thumbnail), a table, a button, a link opened in the app, a field to
/// fill in, an empty state, a spinner. Tones are the app's role colors. Controls that ask first ask; the ones that
/// submit take the page's fields along; a section's `more` loads the next part when it is scrolled to. The page's
/// search field is the hosting page's (`ExtensionPageView`), its filter is drawn here.
///
/// Without a model (the assistant's cards, docs/extensions/extensions.md「Assistant cards: `persona-view` and `persona-card`」) nothing can be used: rows and buttons do
/// nothing, fields, search, filter and more are not drawn; links open as usual.
struct ExtensionViewRenderer: View {
    @Environment(\.assistantHTMLRendering) private var htmlContext
    var view: ExtensionView
    var model: (any ExtensionViewControlling)?
    /// The first section's title when it has none of its own (a launcher's section: what the extension calls it).
    var heading: String? = nil
    /// Where links and `open_url` open: the page's link card.
    @Binding var link: LinkTarget?

    var body: some View {
        #if !os(macOS)
        // Mac pages already expose the same search in their toolbar.
        if let search = view.search, let model, model.managesConfirmation {
            Section { ExtensionSearchRow(search: search, model: model, link: $link) }
                .listRowInsets(EdgeInsets()).listRowBackground(Color.clear)
        }
        #endif
        if let f = view.filter, let model {
            Section { ExtensionFilterChips(filter: f, model: model, link: $link) }
                .listRowInsets(EdgeInsets())
                .listRowBackground(Color.clear)
        }
        ForEach(ExtensionParts.of(view, heading: heading, includeHTML: htmlContext != nil)) { g in
            Section {
                ForEach(g.items) { item in
                    ExtensionItemRow(item: item.component, model: model, link: $link)
                }
                if let more = g.more, let model { ExtensionMoreRow(more: more, model: model, link: $link) }
            } header: {
                if let t = g.title, !t.isEmpty { SettingsHeading(t) }
            } footer: {
                if let f = g.footer, !f.isEmpty { Text(f) }
            }
        }
    }
}

/// A View's sections split at their dividers: the title goes with the first part, the footer and `more` with the last.
/// Components are kept apart by their `key` when they have one (a page drawn again keeps its place), else by where
/// they are.
struct ExtensionParts: Identifiable {
    struct Item: Identifiable {
        var id: String
        var component: ExtensionComponent

        /// A component's identity in its View: its key, else its place (`index` in section `section`).
        nonisolated static func id(_ c: ExtensionComponent, section: Int, index: Int) -> String {
            c.key.map { "k:" + $0 } ?? "\(section).\(index)"
        }
    }

    var id: String
    var title: String?
    var footer: String?
    var more: ExtensionMore?
    var items: [Item]

    static func of(_ view: ExtensionView, heading: String? = nil, includeHTML: Bool = false) -> [ExtensionParts] {
        var out: [ExtensionParts] = []
        for (i, s) in view.sections.enumerated() {
            var parts: [[Item]] = [[]]
            for (j, c) in s.items.enumerated() {
                if case .divider = c { parts.append([]); continue }
                if case .unknown = c { continue }
                if case .html = c, !includeHTML { continue } // Extensions never receive the assistant sandbox context.
                parts[parts.count - 1].append(Item(id: Item.id(c, section: i, index: j), component: c))
            }
            parts = parts.filter { !$0.isEmpty }
            if parts.isEmpty { parts = [[]] }
            for (j, part) in parts.enumerated() {
                let title = j == 0 ? (s.title?.isEmpty == false ? s.title : (i == 0 ? heading : nil)) : nil
                let last = j == parts.count - 1
                out.append(ExtensionParts(id: s.key.map { "k:" + $0 + ".part:\(j)" } ?? "\(out.count)", title: title, footer: last ? s.footer : nil, more: last ? s.more : nil,
                                          items: part))
            }
        }
        return out
    }
}

/// How an extension's words map onto the app's look.
enum ExtensionUIStyle {
    /// neutral | positive | caution | danger → the role colors; anything else reads as neutral.
    static func tone(_ t: String?) -> Color {
        switch t {
        case "positive": Theme.positive
        case "caution": Theme.caution
        case "danger": Theme.danger
        default: .secondary
        }
    }

    /// A metric's number: its tone, or the text's own color when neutral.
    static func ink(_ t: String?) -> Color {
        t == nil || t == "neutral" ? .primary : tone(t)
    }

    /// A chart series' color: a role color when the extension names a tone, nil (the next data hue) when it does not.
    static func series(_ t: String?) -> Color? {
        switch t {
        case "positive": Theme.positive
        case "caution": Theme.caution
        case "danger": Theme.danger
        case nil: nil
        // neutral, and a tone this app does not know.
        default: Theme.Hue.gray
        }
    }

    /// A bar's fill: neutral takes the accent.
    static func bar(_ t: String?) -> Color {
        t == nil || t == "neutral" ? Theme.accent : tone(t)
    }

    static func font(_ style: String?) -> Font {
        switch style {
        case "secondary": Theme.Font.secondary
        case "caption": Theme.Font.caption
        case "code": Theme.Font.code
        default: Theme.Font.body
        }
    }

    /// The icon set's names (docs/extensions/extensions.md「New components」: a row's `icon`, an empty state's, a nav_page's) as the app's
    /// symbols: one meaning each; anything else (and `extension`, `plugin`) is the extensions' own.
    static func icon(_ name: String?) -> String {
        switch name {
        case "chart": Theme.Icon.pluginChart
        case "gauge": Theme.Icon.pluginGauge
        case "list": Theme.Icon.pluginList
        case "bell": Theme.Icon.pluginBell
        case "doc": Theme.Icon.pluginDoc
        case "calendar": Theme.Icon.pluginCalendar
        case "money": Theme.Icon.pluginMoney
        case "person": Theme.Icon.person
        case "chat": Theme.Icon.message
        case "mail": Theme.Icon.mail
        case "link": Theme.Icon.link
        case "folder": Theme.Icon.folder
        case "star": Theme.Icon.pluginStar
        case "check": Theme.Icon.success
        case "warning": Theme.Icon.error
        case "clock": Theme.Icon.time
        case "tag": Theme.Icon.label
        case "search": Theme.Icon.search
        case "settings": Theme.Icon.settings
        case "speaker": Theme.Icon.voiceSpeaking
        case "music": Theme.Icon.music
        default: Theme.Icon.plugins
        }
    }
}

/// One component of an extension's View.
struct ExtensionItemRow: View {
    var item: ExtensionComponent
    @Environment(\.assistantHTMLRendering) private var htmlContext
    var model: (any ExtensionViewControlling)?
    @Binding var link: LinkTarget?

    var body: some View {
        switch item {
        case .text(let t):
            Text(t.text)
                .font(ExtensionUIStyle.font(t.style))
                .foregroundStyle(t.style == "secondary" || t.style == "caption" ? Color.secondary : Color.primary)
                .textSelection(.enabled)
        case .markdown(let m): ExtensionMarkdown(text: m.text, link: $link)
        case .metric(let m): metric(m)
        case .progress(let p): progress(p)
        case .row(let r):
            if let control = r.control, control.drawable {
                // UI version 6: a switch, a slider or a stepper in the row (`ExtensionControlRow`).
                ExtensionControlRow(row: r, control: control, model: model, link: $link)
            } else if let action = r.action, let model {
                ExtensionActionButton(action: action, title: r.title, model: model, link: $link) { ExtensionComponentRowLabel(row: r, model: model, chevron: true) }
                    .foregroundStyle(Color.primary)
            } else {
                ExtensionComponentRowLabel(row: r, model: model, chevron: false)
            }
        case .button(let b):
            if let model {
                ExtensionActionButton(action: b.action, title: b.title, destructive: b.role == "destructive", model: model, link: $link) {
                    HStack(spacing: Theme.Space.s) {
                        if model.selected(b.action.id) {
                            ThemedIcon(Theme.Icon.success).foregroundStyle(Theme.positive).accessibilityLabel("Selected [Extension Components 7ecf1c]")
                        }
                        Text(b.title).lineLimit(1).minimumScaleFactor(0.8)
                        if model.running == b.action.id { ProgressView().controlSize(.small) }
                    }
                }
            }
        case .link(let l):
            if let url = URL(string: l.url), url.scheme == "https" {
                Button { link = LinkTarget(url: url) } label: {
                    Label { Text(l.title.isEmpty ? (url.host() ?? "") : l.title).lineLimit(1) } icon: { ThemedIcon(Theme.Icon.link) }
                }
                .accessibilityIdentifier("plugin-ui-link")
            }
        case .field(let f): if let model { ExtensionFieldItem(field: f, model: model) }
        case .chart(let c): ExtensionChartItem(item: c)
        case .statGrid(let g): ExtensionStatGrid(stats: g.stats)
        case .segmented(let s): if let model { ExtensionSegmented(item: s, model: model, link: $link) }
        case .table(let t): ExtensionTable(table: t)
        case .empty(let e): ExtensionEmpty(empty: e, model: model, link: $link)
        case .loading(let l):
            HStack(spacing: Theme.Space.s) {
                ProgressView().controlSize(.small)
                Text(l.title?.isEmpty == false ? l.title! : String(localized: "Loading… [Personal Agent Session View 4927a5]")).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1)
            }
            .accessibilityIdentifier("plugin-ui-loading")
        // A divider splits sections before this; a component this app does not know (a newer extension's) is left out.
        case .html(let html):
            if let context = htmlContext {
                AssistantHTMLCard(title: html.title, html: html.html, height: html.height, fallback: context.fallback,
                    answered: context.answered, enabled: context.enabled, onReply: { _ in false }, onOpen: { _ in },
                    version: context.version, bridgeEnabled: context.bridgeAvailable && html.key?.isEmpty == false,
                    onConfirmedRequest: { request, version in
                        guard let key = html.key else { return false }
                        return await context.onRequest(request, key, version)
                    })
            }
        // UI version 6's picture, grid and carousel are not drawn yet (#827). Until all of version 6 is, the app asks
        // for an older version, so the hub turns them into older components before they get here.
        case .divider, .unknown, .image, .grid, .carousel: EmptyView()
        // UI version 4's code-hosting components: a View with them is drawn by the code page (`ExtensionCodePage`, #2961).
        case .header, .breadcrumb, .fileTree, .code, .diff, .commentThread, .composer: EmptyView()
        }
    }

    private func metric(_ m: ExtensionMetricComponent) -> some View {
        VStack(alignment: .leading, spacing: Theme.Space.xxs) {
            Text(m.label).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1)
            ExtensionChangingText(m.value).font(Theme.Font.title.monospacedDigit()).foregroundStyle(ExtensionUIStyle.ink(m.tone)).lineLimit(1)
                .minimumScaleFactor(0.6)
            if let d = m.detail, !d.isEmpty { Text(d).font(Theme.Font.label).foregroundStyle(.secondary) }
        }
        .padding(.vertical, Theme.Space.xxs)
        .accessibilityElement(children: .combine)
    }

    private func progress(_ p: ExtensionProgressComponent) -> some View {
        let ratio = min(max(p.ratio, 0), 1)
        return VStack(alignment: .leading, spacing: Theme.Space.xs) {
            HStack(spacing: Theme.Space.sm) {
                Text(p.label).font(Theme.Font.caption).lineLimit(1)
                Spacer(minLength: Theme.Space.s)
                Text("\(Int((ratio * 100).rounded()))%").font(Theme.Font.caption.monospacedDigit())
                    .foregroundStyle(p.tone == nil || p.tone == "neutral" ? Color.secondary : ExtensionUIStyle.tone(p.tone)).lineLimit(1)
            }
            ProgressView(value: ratio).tint(ExtensionUIStyle.bar(p.tone))
            if let d = p.detail, !d.isEmpty { Text(d).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(2) }
        }
        .padding(.vertical, Theme.Space.xxs)
        .accessibilityElement(children: .combine)
    }
}

extension EnvironmentValues {
    /// An extension's View drawn as a card (`size: compact`): charts without axis text, the rest as usual.
    @Entry var pluginUICompact = false
}

/// An extension's View on a page that is not a list (a machine's page, a card, the assistant's reply): each section a
/// heading and a card of its components, a divider a line between them. The same components as
/// `ExtensionViewRenderer`, drawn by `ExtensionItemRow`.
struct ExtensionViewStack: View {
    struct SectionEntry: Identifiable {
        var index: Int
        var section: ExtensionSection
        var id: String { Self.id(section, index: index) }

        /// A section's identity in its View: its key, else its place.
        nonisolated static func id(_ s: ExtensionSection, index: Int) -> String { s.key.map { "k:" + $0 } ?? "\(index)" }
    }

    @Environment(\.assistantHTMLRendering) private var htmlContext
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    var view: ExtensionView
    var model: (any ExtensionViewControlling)?
    /// The first section's heading when it has none of its own.
    var heading: String? = nil
    /// Draw the sections' cards (a page); false: the caller's card holds them (an overview card).
    var cards = true
    /// Draw the sections' titles; nil: when it draws their cards (an overview card has its own title).
    var titles: Bool? = nil
    @Binding var link: LinkTarget?

    var body: some View {
        VStack(alignment: .leading, spacing: cards ? Theme.Space.l : Theme.Space.m) {
            if let search = view.search, let model, model.managesConfirmation {
                ExtensionSearchRow(search: search, model: model, link: $link)
            }
            if let f = view.filter, let model { ExtensionFilterChips(filter: f, model: model, link: $link) }
            ForEach(view.sections.enumerated().map { SectionEntry(index: $0.offset, section: $0.element) }) { entry in
                let i = entry.index
                let s = entry.section
                VStack(alignment: .leading, spacing: Theme.Space.s) {
                    if titles ?? cards, let t = (s.title?.isEmpty == false ? s.title : (i == 0 ? heading : nil)), !t.isEmpty {
                        Text(t).font(Theme.Font.sectionHeader).foregroundStyle(.secondary).lineLimit(1)
                            .padding(.horizontal, Theme.Space.xs)
                    }
                    let items = s.items.enumerated().compactMap { j, item -> ExtensionParts.Item? in
                        if case .unknown = item { return nil }
                        if case .html = item, htmlContext == nil { return nil }
                        return ExtensionParts.Item(id: ExtensionParts.Item.id(item, section: i, index: j), component: item)
                    }
                    if !items.isEmpty || (s.more != nil && model != nil) {
                        let body = VStack(alignment: .leading, spacing: Theme.Space.m) {
                            ForEach(items) { entry in
                                Group {
                                    if case .divider = entry.component { Divider() } else {
                                        ExtensionItemRow(item: entry.component, model: model, link: $link)
                                            .frame(maxWidth: .infinity, alignment: .leading)
                                    }
                                }
                                .transition(.opacity)
                            }
                            if let more = s.more, let model { ExtensionMoreRow(more: more, model: model, link: $link) }
                        }
                        if cards { body.card() } else { body }
                    }
                    if let f = s.footer, !f.isEmpty {
                        Text(f).font(Theme.Font.label).foregroundStyle(.secondary).padding(.horizontal, Theme.Space.xs)
                    }
                }
                .transition(.opacity)
            }
        }
        // Drawn again with blocks that came or went, they fade in and out and the rest moves (#37); a model's redraw
        // comes animated already (`ExtensionMotion.redraw`), this covers a View changed by its caller (a card).
        .animation(reduceMotion ? nil : Theme.Motion.layout, value: ExtensionMotion.blocks(view))
    }
}

extension View {
    /// Where an extension's links open: the link card (iPhone) or the panel at the trailing edge (Mac).
    @ViewBuilder func pluginLinks(_ link: Binding<LinkTarget?>) -> some View {
        #if os(macOS)
        macLinkPanel(link)
        #else
        modifier(ExtensionLinkSheet(link: link))
        #endif
    }

    /// The pages `models` ask to push (docs/extensions/extensions.md「Page navigation」), on the navigation stack this page is on; the one
    /// that asked draws again when its page closes. Put on the page itself, not a row: a list's rows cannot push.
    func extensionPushes(_ models: [ExtensionViewModel]) -> some View { modifier(ExtensionPushes(models: models)) }
}

#if os(iOS) || os(visionOS)
private struct ExtensionLinkSheet: ViewModifier {
    @Environment(AppModel.self) private var model
    @Binding var link: LinkTarget?

    func body(content: Content) -> some View {
        content.sheet(item: $link) { t in LinkSheet(target: t, key: model.assistantMain ?? model.assistantCurrent) }
    }
}
#endif

/// A page an extension pushes: its extension, which of its interfaces (its `ui`), the state it starts with.
nonisolated struct ExtensionPushTarget: Hashable, Sendable {
    var plugin: String
    var ui: String
    var state: String?
    var title: String
    var depth: Int
    var group: String
    var manager: String?
    var context: ExtensionUIContext

    @MainActor init(model: ExtensionViewModel, push: ExtensionPush) {
        plugin = model.plugin
        ui = push.ui?.isEmpty == false ? push.ui! : model.ui
        state = push.state
        title = push.title ?? ""
        depth = model.depth + 1
        group = model.group
        manager = model.selectedManager
        context = model.context
        context.size = nil
    }
}

private struct ExtensionPushes: ViewModifier {
    var models: [ExtensionViewModel]
    /// The stack's own way to push by value (settings, the inbox): the page goes where the list's pages go.
    @Environment(\.splitPush) private var splitPush

    func body(content: Content) -> some View {
        // Read here, so that a model asking to push draws this again.
        let asking = models.first { $0.pushed != nil }
        let target = asking.flatMap { m in
            m.pushed.map { ExtensionPushTarget(model: m, push: $0) }
        }
        if let splitPush {
            content
                .onChange(of: target) { _, t in
                    guard let t, let asking else { return }
                    asking.pushed = nil
                    asking.returning = true
                    splitPush.push(t)
                }
                // Back from the page it pushed (closed, or `pop`): the page below is drawn again.
                .onAppear {
                    for m in models where m.returning {
                        m.returning = false
                        Task { await m.refresh() }
                    }
                }
        } else {
            content.navigationDestination(item: Binding(get: { target }, set: { new in
                guard new == nil, let asking else { return }
                asking.pushed = nil
                Task { await asking.refresh() }
            })) { t in
                ExtensionPageView(plugin: t.plugin, ui: t.ui, title: t.title, state: t.state, depth: t.depth,
                                  group: t.group, context: t.context, manager: t.manager)
            }
        }
    }
}

extension View {
    /// Where the pages extensions push open, registered with a stack's other destinations (`splitPush` pushes them).
    func extensionPushDestination() -> some View {
        navigationDestination(for: ExtensionPushTarget.self) { t in
            ExtensionPageView(plugin: t.plugin, ui: t.ui, title: t.title, state: t.state, depth: t.depth,
                                  group: t.group, context: t.context, manager: t.manager).splitDetailPage()
        }
    }
}

/// Settings › Extensions › an extension › one of its pages (placement extension_page / plugin_page), and the pages it pushes: its
/// View as a page of its own, with its search field when it has one.
struct ExtensionPageView: View {
    @State private var page: ExtensionViewModel
    @State private var link: LinkTarget?
    @Environment(\.dismiss) private var dismiss
    @Environment(AppModel.self) private var app
    @Environment(\.scenePhase) private var phase
    @Environment(\.tabIsActive) private var tabActive
    @State private var pageVisible = false
    @FocusState private var focusedField: String?
    var title: String
    var initial: ExtensionSystemOpen?

    init(plugin: String, ui: String, title: String, state: String? = nil, depth: Int = 1,
         group: String? = nil, context: ExtensionUIContext = .here(), initial: ExtensionSystemOpen? = nil,
         manager: String? = nil) {
        let deps: ExtensionViewModel.Deps = group.map { group in
            .init(api: { AppModel.shared.api(group) }, connection: { AppModel.shared.socket(group) })
        } ?? .live
        // A whole page draws UI version 4's code-hosting parts (#2961); cards and sections keep the shared version.
        _page = State(initialValue: ExtensionViewModel(plugin: plugin, ui: ui, context: context.forPage(), state: state, depth: depth,
                                                       deps: deps, manager: manager))
        self.title = title
        self.initial = initial
    }

    var body: some View {
        // Keep one page host while a committed snapshot changes the native renderer.
        ZStack {
            if page.view?.wantsCodePage == true {
                // UI version 4's code-hosting parts (#2961): tabs, a split, a tree, diffs, code, comments.
                ExtensionCodePage(model: page, link: $link)
                    // One container, so that the page's own identifier (extension-ui-ready) stays on it and does not
                    // replace its first control's (it took the back row's and the code's).
                    .accessibilityElement(children: .contain)
            } else {
                form
            }
        }
        .accessibilityIdentifier(page.view == nil ? "extension-ui-waiting" : "extension-ui-ready")
        .modifier(ExtensionStreamProbe(stream: page.stream,
            lifecycle: "mounted=\(pageVisible):tab=\(tabActive):phase=\(String(describing: phase))"))
        .overlay { if page.view == nil && page.error == nil && !page.unsupported { ProgressView() } }
        .detailTitle(page.view?.title ?? title)
        .navigationBarTitleDisplayMode(.inline)
        .extensionSearch(page)
        .pluginLinks($link)
        .environment(\.extensionFieldFocus, $focusedField)
        .environment(\.extensionDisplayedRevision, page.stream?.displayedRevision)
        .extensionPushes([page])
        // Alert dismissal writes its binding before running the selected action. Consent/cancellation
        // is owned by the explicit buttons (and page lifecycle), never by that presentation write.
        .alert(page.confirmation?.action.confirm ?? "", isPresented: Binding(
            get: { page.confirmation != nil }, set: { _ in })) {
            Button { page.approveConfirmation() } label: { Text("Confirm").lineLimit(1) }
                .accessibilityIdentifier("plugin-ui-confirm")
            Button(role: .cancel) { page.cancelConfirmation() } label: { Text("Cancel").lineLimit(1) }
        }
        .onTabVisibility(appear: { pageVisible = true; updateVisibility() },
                         disappear: { pageVisible = false; updateVisibility() })
        .onChange(of: phase) { _, _ in updateVisibility() }
        .onChange(of: page.popped) { _, popped in
            guard popped else { return }
            if page.depth > 1 { dismiss() }
            else if let initial, app.extensionOpen?.id == initial.id { app.extensionOpen = nil }
        }
        .task {
            if let initial {
                let outcome = await initial.start(page)
                if let error = outcome.error { app.toast = Toast(error, style: .error) }
                else if let toast = outcome.toast { app.toast = Toast(toast) }
                if let url = outcome.url, !app.openIfExtensionSettings(url) { link = LinkTarget(url: url) }
            } else { await page.load() }
        }
        .pluginRefresh(page)
        .refreshable { await page.load() }
    }

    private func updateVisibility() {
        var foreground = phase == .active
        #if DEBUG && os(macOS)
        // A validated loopback capture uses the normal stream while its window stays non-activating.
        // This exception exists only for the DEBUG batch; production still requires an active scene.
        foreground = foreground || MacExtensionSnapshotBatch.isCapturing
        #endif
        page.setVisible(pageVisible && tabActive && foreground)
    }

    private var form: some View {
        SettingsForm {
            if let v = page.view {
                ExtensionViewRenderer(view: v, model: page, link: $link)
            } else if let e = page.error {
                Section {
                    EmptyStateView(symbol: Theme.Icon.error, title: String(localized: "Couldn't Load This Page"), message: e,
                                   action: (String(localized: "Retry [Components b8784c]"), { Task { await page.load() } }))
                }
                .listRowBackground(Color.clear)
            } else if page.unsupported {
                Section {
                    EmptyStateView(symbol: Theme.Icon.plugins, title: String(localized: "Nothing Here"), message: String(localized: "The extension isn't showing anything here right now."))
                }
                .listRowBackground(Color.clear)
            }
        }
        .themedPage()
    }
}

extension View {
    /// The page's `search` field, while its View has one.
    func extensionSearch(_ page: ExtensionViewModel) -> some View { modifier(ExtensionSearchField(page: page)) }
}

/// A page's `search`: the search field in the navigation bar while the View has one; what is typed goes to the
/// extension once the typing pauses for 0.3 s.
private struct ExtensionSearchField: ViewModifier {
    @Environment(\.extensionDisplayedRevision) private var displayed
    @Bindable var page: ExtensionViewModel
    @Environment(AppModel.self) private var app

    func body(content: Content) -> some View {
        if let s = page.view?.search {
            content
                .pageSearchable(text: $page.query, placement: .navigationBarDrawer(displayMode: .always),
                            prompt: s.placeholder?.isEmpty == false ? s.placeholder! : String(localized: "Search"))
                .task(id: page.query) {
                    // Only what was typed since the last search: coming back to the page asks nothing again.
                    guard page.query != page.searchedQuery else { return }
                    let query = page.query
                    var chosen = ["q": query]
                    if let filter = page.view?.filter { chosen[filter.name] = page.values[filter.name] ?? filter.value }
                    let execute = displayed.map { page.prepared(s.action, with: chosen, displayed: $0) }
                    try? await Task.sleep(for: .milliseconds(300))
                    guard !Task.isCancelled else { return }
                    let o: ExtensionViewModel.Outcome
                    if let execute { o = await execute(); page.searched(query, outcome: o) }
                    else { o = await page.search() }
                    if let e = o.error { app.toast = Toast(e, style: .error) }
                }
        } else {
            content
        }
    }
}

/// A launcher's page › what extensions draw for it (placement launcher_section): a section each, after its quota. An
/// extension that says the launcher's gateway is not its own (404 unsupported) draws nothing, and that is not an error.
/// The page owns the model and loads it (`ExtensionLauncherModel.load`, with a hub that has extensions).
struct ExtensionLauncherSections: View {
    var page: ExtensionLauncherModel
    @Binding var link: LinkTarget?

    var body: some View {
        ForEach(page.shown) { entry in
            if let v = entry.model.view {
                ExtensionViewRenderer(view: v, model: entry.model, heading: entry.place.title, link: $link)
                    .pluginRefresh(entry.model)
            } else {
                Section {
                    if let e = entry.model.error {
                        Label(e, systemImage: Theme.Icon.error).font(Theme.Font.label).foregroundStyle(Theme.danger)
                        Button { Task { await entry.model.load() } } label: { Text("Retry [Components b8784c]").lineLimit(1) }
                            .accessibilityIdentifier("plugin-ui-retry")
                    } else {
                        HStack(spacing: Theme.Space.s) {
                            ProgressView()
                            Text("Loading… [File Viewer 86b6d0]").font(Theme.Font.label).foregroundStyle(.secondary)
                        }
                    }
                } header: {
                    SettingsHeading(entry.place.title)
                }
            }
        }
    }
}
