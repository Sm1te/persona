import MarkdownUI
import SwiftUI

// The components of an extension's View (docs/extensions/extensions.md「Declarative UI」「UI 版本 3」), drawn by `ExtensionItemRow`.

/// A row's look: what leads it (an icon, an avatar or a thumbnail), its title and subtitle, its value and badge, and a
/// chevron when it can be used (a spinner while it runs); `trailing` at its very end (a control, UI version 6).
struct ExtensionComponentRowLabel<Trailing: View>: View {
    var row: ExtensionRowComponent
    var model: (any ExtensionViewControlling)?
    var chevron: Bool
    @ViewBuilder var trailing: Trailing
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        HStack(spacing: Theme.Space.m) {
            leading
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                Text(row.title).font(Theme.Font.body).lineLimit(1).minimumScaleFactor(0.8)
                if let s = row.subtitle, !s.isEmpty { Text(s).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(2) }
            }
            Spacer(minLength: Theme.Space.s)
            if let v = row.value, !v.isEmpty { ExtensionChangingText(v).font(Theme.Font.body.monospacedDigit()).foregroundStyle(.secondary).lineLimit(1) }
            if let b = row.badge {
                // Drawn again with another tone, its colors change; with other words, they fade in (#37).
                Chip(text: b.text, look: b.tone == nil || b.tone == "neutral" ? .neutral : .tint(ExtensionUIStyle.tone(b.tone)))
                    .contentTransition(.opacity)
                    .animation(reduceMotion ? nil : Theme.Motion.state, value: b)
            }
            if let action = row.action, model?.selected(action.id) == true {
                ThemedIcon(Theme.Icon.success).font(Theme.Font.labelStrong).foregroundStyle(Theme.positive)
                    .accessibilityLabel("Selected [Extension Components 7ecf1c]")
            } else if chevron {
                if let a = row.action, model?.running == a.id {
                    ProgressView().controlSize(.small)
                } else {
                    ThemedIcon(Theme.Icon.disclosure).font(Theme.Font.labelStrong).foregroundStyle(.tertiary)
                }
            }
            trailing
        }
        .contentShape(.rect)
    }

    @ViewBuilder private var leading: some View {
        if let t = row.thumbnail, let url = Self.image(t.url ?? "") {
            let wide = t.aspect == ExtensionThumbnailAspect.wide
            ExtensionRemoteImage(url: url)
                .scaledFrame(width: wide ? Theme.Size.box80 : Theme.Size.box50, height: Theme.Size.box50)
                .clipShape(.rect(cornerRadius: Theme.Radius.badge))
                .accessibilityHidden(true)
        } else if let a = row.avatar, a.url != nil || a.text?.isEmpty == false {
            Group {
                if let url = a.url.flatMap(Self.image) {
                    ExtensionRemoteImage(url: url, fallback: a.text)
                } else {
                    Monogram(name: a.text ?? "", size: Theme.Size.wellL, fill: Theme.Fill.chip, font: Theme.Font.labelStrong, letters: 2)
                }
            }
            .scaledFrame(width: Theme.Size.wellL, height: Theme.Size.wellL)
            .clipShape(.circle)
            .accessibilityHidden(true)
        } else if let i = row.icon, !i.isEmpty {
            // The icon and its column stop growing at the largest standard size, where the glyph still fits: past it the
            // glyph followed the text to 3 × while the column stopped at `sizeScale`'s 1.6 ×, and spilled into the
            // title and past the inset (#4841). The title keeps growing; the default sizes are unchanged.
            ThemedIcon(ExtensionUIStyle.icon(i))
                .font(Theme.Font.heading)
                .foregroundStyle(Theme.accent)
                .scaledFrame(width: Theme.Size.icon, height: Theme.Size.icon)
                .dynamicTypeSize(...DynamicTypeSize.xxxLarge)
                .accessibilityHidden(true)
        }
    }

    /// An image the extension points at: https only, loaded without any of the app's credentials.
    static func image(_ s: String) -> URL? {
        ExtensionImageCache.url(s)
    }
}

extension ExtensionComponentRowLabel where Trailing == EmptyView {
    init(row: ExtensionRowComponent, model: (any ExtensionViewControlling)?, chevron: Bool) {
        self.init(row: row, model: model, chevron: chevron) { EmptyView() }
    }
}

/// A picture an extension points at (a row's avatar or thumbnail): loaded with a session that sends no cookies or
/// credentials, a placeholder while it loads and when it cannot be loaded.
struct ExtensionRemoteImage: View {
    var url: URL
    /// An avatar's letters when the picture fails.
    var fallback: String?
    /// Catalog logos use the existing extension mark while loading or unavailable.
    var fallbackSymbol: String? = nil
    var points: CGFloat = Theme.Size.box80
    var contentMode: ContentMode = .fill
    @Environment(\.displayScale) private var displayScale
    @Environment(\.dynamicTypeSize) private var typeSize
    @State private var image: Image?
    @State private var failed = false

    private struct Request: Hashable { var url: URL; var pixels: Int }

    var body: some View {
        ZStack {
            if let image {
                image.resizable().aspectRatio(contentMode: contentMode)
            } else if let fallbackSymbol {
                ThemedIcon(fallbackSymbol).font(Theme.Font.heading).foregroundStyle(Theme.accent)
            } else if failed, let f = fallback, !f.isEmpty {
                Monogram(name: f, size: Theme.Size.wellL, fill: Theme.Fill.chip, font: Theme.Font.labelStrong, letters: 2)
            } else {
                Rectangle().fill(Theme.Fill.chip)
            }
        }
        .task(id: Request(url: url, pixels: Int(ceil(points * displayScale * Theme.sizeScale(typeSize))))) {
            image = nil
            failed = false
            #if DEBUG && os(macOS)
            let captureLoad = MacExtensionSnapshotBatch.beginImageLoad(url)
            defer { MacExtensionSnapshotBatch.finishImageLoad(captureLoad, loaded: image != nil) }
            #endif
            let pixels = Int(ceil(points * displayScale * Theme.sizeScale(typeSize)))
            guard let cg = await ExtensionImageCache.shared.image(url, maxPixel: pixels) else {
                if !Task.isCancelled { failed = true }
                return
            }
            guard !Task.isCancelled else { return }
            image = Image(decorative: cg, scale: displayScale)
        }
    }
}

/// Catalog artwork uses the same safe image component as extension rows, with a fitted logo and generic fallback.
struct ExtensionLogo: View {
    var url: String?
    var side: CGFloat = Theme.Size.wellM

    var body: some View {
        Group {
            if let url = ExtensionImageCache.url(url) {
                ExtensionRemoteImage(url: url, fallbackSymbol: Theme.Icon.plugins, points: side, contentMode: .fit)
            } else {
                ThemedIcon(Theme.Icon.plugins).font(Theme.Font.heading).foregroundStyle(Theme.accent)
            }
        }
        .scaledFrame(width: side, height: side)
        .clipShape(.rect(cornerRadius: Theme.Radius.badge))
        .accessibilityHidden(true)
    }
}

/// An extension's `markdown`: the safe subset (headings, paragraphs, lists, emphasis, code, quotes, links) in the chat's
/// theme. Pictures are never loaded; links open in the app, https only.
struct ExtensionMarkdown: View {
    var text: String
    @Binding var link: LinkTarget?

    var body: some View {
        Markdown(text)
            .markdownTheme(.persona)
            .markdownImageProvider(NoImages())
            .markdownInlineImageProvider(NoImages())
            .textSelection(.enabled)
            .frame(maxWidth: .infinity, alignment: .leading)
            .environment(\.openURL, OpenURLAction { url in
                if url.scheme?.lowercased() == "https" { link = LinkTarget(url: url) }
                return .handled
            })
            .accessibilityIdentifier("plugin-ui-markdown")
    }

    /// The subset has no pictures: whatever an image points at is not loaded.
    private struct NoImages: ImageProvider, InlineImageProvider {
        func makeImage(url: URL?) -> some View { EmptyView() }
        func image(with url: URL, label: String) async throws -> Image { throw URLError(.unsupportedURL) }
    }
}

/// An extension's `table`: its columns' titles, its rows, a cell on one line each; wider than the screen it scrolls
/// sideways. Its caption under it.
struct ExtensionTable: View {
    var table: ExtensionTableComponent

    var body: some View {
        let columns = Array(table.columns.prefix(8))
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            ScrollView(.horizontal) {
                Grid(alignment: .leading, horizontalSpacing: Theme.Space.l, verticalSpacing: Theme.Space.s) {
                    GridRow {
                        ForEach(Array(columns.enumerated()), id: \.offset) { _, c in
                            Text(c.title).font(Theme.Font.labelStrong).foregroundStyle(.secondary).lineLimit(1)
                                .gridColumnAlignment(c.align == ExtensionTableAlign.end ? .trailing : .leading)
                        }
                    }
                    Divider().gridCellUnsizedAxes(.horizontal)
                    ForEach(Array(table.rows.prefix(100).enumerated()), id: \.offset) { _, row in
                        GridRow {
                            ForEach(Array(columns.indices), id: \.self) { i in
                                Text(i < row.count ? row[i] : "")
                                    .font(columns[i].align == ExtensionTableAlign.end ? Theme.Font.secondary.monospacedDigit() : Theme.Font.secondary)
                                    .lineLimit(1)
                                    .fixedSize()
                            }
                        }
                    }
                }
                .padding(.vertical, Theme.Space.xs)
            }
            .textSelection(.enabled)
            if let c = table.caption, !c.isEmpty { Text(c).font(Theme.Font.label).foregroundStyle(.secondary) }
        }
        .accessibilityIdentifier("plugin-ui-table")
    }
}

/// An extension's `empty`: the app's empty state with its icon, words and at most one button.
struct ExtensionEmpty: View {
    @Environment(\.extensionDisplayedRevision) private var displayed
    @Environment(AppModel.self) private var app
    var empty: ExtensionEmptyComponent
    var model: (any ExtensionViewControlling)?
    @Binding var link: LinkTarget?

    @State private var asking = false

    var body: some View {
        EmptyStateView(symbol: ExtensionUIStyle.icon(empty.icon), title: empty.title, message: empty.detail ?? "",
                       action: button)
            .frame(maxWidth: .infinity)
            .listRowBackground(Color.clear)
            .accessibilityIdentifier("plugin-ui-empty")
            .disabled(model?.managesConfirmation == true && empty.button.map { model?.isEnabled($0.action) != true } == true)
            .confirmationDialog(empty.button?.action.confirm ?? "", isPresented: $asking, titleVisibility: .visible) {
                Button { run() } label: { Text(empty.button?.title ?? String(localized: "OK [Interactive Assistant View Card fac2a6]")).lineLimit(1) }
                    .accessibilityIdentifier("plugin-ui-confirm")
                Button("Cancel", role: .cancel) {}
            }
    }

    private var button: (title: String, run: () -> Void)? {
        guard let b = empty.button, let model else { return nil }
        return (b.title, {
            if b.action.confirm?.isEmpty == false, !model.managesConfirmation { asking = true } else { run() }
        })
    }

    private func run() {
        guard let b = empty.button, let model, model.isEnabled(b.action) else { return }
        let execute = model.prepared(b.action, with: [:], displayed: displayed)
        Task { ExtensionOutcome.show(await execute(), app: app, link: $link) }
    }
}

/// A section's `more`: a row at its end that loads the next part when it scrolls into view (once, until the extension
/// answers) or is tapped.
struct ExtensionMoreRow: View {
    @Environment(\.extensionDisplayedRevision) private var displayed
    @Environment(AppModel.self) private var app
    var more: ExtensionMore
    var model: any ExtensionViewControlling
    @Binding var link: LinkTarget?

    var body: some View {
        Button { load() } label: {
            HStack(spacing: Theme.Space.s) {
                Text(more.title?.isEmpty == false ? more.title! : String(localized: "Load More")).lineLimit(1)
                if model.loadingMore == more.action.id { ProgressView().controlSize(.small) }
            }
            .frame(maxWidth: .infinity)
        }
        .disabled(model.loadingMore != nil || (model.managesConfirmation && !model.isEnabled(more.action)))
        .onAppear { if !model.managesConfirmation { load() } }
        .accessibilityIdentifier("plugin-ui-more")
    }

    private func load() {
        guard model.loadingMore == nil else { return }
        if let page = model as? ExtensionViewModel, let displayed {
            let execute = page.preparedMore(more, displayed: displayed)
            Task { ExtensionOutcome.show(await execute(), app: app, link: $link) }
        } else { Task { ExtensionOutcome.show(await model.more(more), app: app, link: $link) } }
    }
}

/// The page's `filter`: its choices as chips under the search field, the picked one on; picking another asks the
/// extension for the view at once.
struct ExtensionFilterChips: View {
    @Environment(\.extensionDisplayedRevision) private var displayed
    @Environment(AppModel.self) private var app
    var filter: ExtensionFilter
    var model: any ExtensionViewControlling
    @Binding var link: LinkTarget?

    var body: some View {
        let picked = model.values[filter.name] ?? filter.value
        ScrollView(.horizontal) {
            HStack(spacing: Theme.Space.s) {
                ForEach(filter.choices.prefix(20)) { c in
                    FilterChip(title: c.label, on: c.value == picked) {
                        if let displayed {
                            let before = model.values[filter.name]
                            model.values[filter.name] = c.value
                            let execute = model.prepared(filter.action, with: [filter.name: c.value, "q": model.query], displayed: displayed)
                            Task {
                                let outcome = await execute()
                                if outcome.error != nil || outcome.cancelled { model.values[filter.name] = before }
                                ExtensionOutcome.show(outcome, app: app, link: $link)
                            }
                        } else { Task { ExtensionOutcome.show(await model.filter(c.value), app: app, link: $link) } }
                    }
                    .accessibilityIdentifier("plugin-ui-filter-\(c.value)")
                }
            }
            .padding(.horizontal, Theme.Space.page)
            .padding(.vertical, Theme.Space.xs)
        }
        .disabled(model.running != nil || (model.managesConfirmation && !model.isEnabled(filter.action)))
        .accessibilityLabel(filter.title ?? String(localized: "Filter"))
    }
}

/// A card's explicit search control. Unlike extension-page debounce, typing cannot send an assistant reply.
struct ExtensionSearchRow: View {
    var search: ExtensionSearch
    var model: any ExtensionViewControlling
    @Binding var link: LinkTarget?
    @Environment(AppModel.self) private var app

    var body: some View {
        HStack(spacing: Theme.Space.s) {
            TextField(search.placeholder ?? String(localized: "Search"), text: Binding(get: { model.query }, set: { model.query = $0 }))
                .font(Theme.Font.body).textInputAutocapitalization(.never).autocorrectionDisabled()
                .onSubmit { run() }
            Button { run() } label: { ThemedIcon(Theme.Icon.search) }
                .themedButton().accessibilityLabel("Search")
        }
        .padding(Theme.Space.s)
        .background(Theme.Fill.well, in: .rect(cornerRadius: Theme.Radius.control))
        .disabled(!model.isEnabled(search.action))
        .accessibilityIdentifier("assistant-view-search")
    }
    private func run() { Task { ExtensionOutcome.show(await model.search(), app: app, link: $link) } }
}

/// What using a control came to, on screen: an error or the extension's word as a toast, its link in the app.
enum ExtensionOutcome {
    @MainActor static func show(_ o: ExtensionViewModel.Outcome, app: AppModel, link: Binding<LinkTarget?>) {
        if let e = o.error {
            Haptics.error()
            app.toast = Toast(e, style: .error)
        } else if let t = o.toast {
            app.toast = Toast(t, style: .success)
        }
        if let url = o.url, !app.openIfExtensionSettings(url) { link.wrappedValue = LinkTarget(url: url) }
    }
}

/// An extension's `chart`: its title, the chart (bars or lines, stacked when it says so; a donut or pie a `RingChart` of
/// the first series), what it says under it. A
/// series' tone is a role color, without one it takes the next data hue; values the extension did not write are compact
/// numbers with its unit. Compact (a card) when the environment says so.
struct ExtensionChartItem: View {
    var item: ExtensionChartComponent
    @Environment(\.pluginUICompact) private var compact
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    private var series: [ExtensionChartSeries] { Array(item.series.prefix(4)) }
    /// The x axis: the first series' labels (the others line up with them).
    private var labels: [String] { series.first?.points.map(\.label) ?? [] }

    var body: some View {
        if !labels.isEmpty {
            VStack(alignment: .leading, spacing: Theme.Space.s) {
                if let t = item.title, !t.isEmpty {
                    Text(t).font(compact ? Theme.Font.labelStrong : Theme.Font.secondaryStrong).foregroundStyle(compact ? .secondary : .primary).lineLimit(1)
                }
                if item.kind == ExtensionChartKind.donut || item.kind == ExtensionChartKind.pie, let s = series.first {
                    RingChart(parts: s.points.map { RingChart.Part(label: $0.label, value: $0.value, display: $0.display ?? format($0.value)) },
                              center: item.center, pie: item.kind == ExtensionChartKind.pie)
                } else {
                DataChart(kind: item.kind == "line" ? .line : .bar, labels: labels,
                          series: series.enumerated().map { i, s in
                              DataChart.Series(name: s.name?.isEmpty == false ? s.name! : String(localized: "Series \(i + 1)"),
                                               color: ExtensionUIStyle.series(s.tone), values: s.points.map(\.value),
                                               displays: s.points.map(\.display))
                          },
                          stacked: item.stacked == true, format: format, compact: compact)
                // Drawn again with other values, the bars and lines move to them (#37).
                .animation(reduceMotion ? nil : Theme.Motion.layout, value: item.series)
                }
                if let d = item.detail, !d.isEmpty, !compact {
                    Text(d).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(3)
                }
            }
            .padding(.vertical, Theme.Space.xs)
        }
    }

    private func format(_ v: Double) -> String {
        let n = DataChart.compact(v)
        guard let u = item.unit, !u.isEmpty else { return n }
        return n + " " + u
    }
}

/// An extension's `stat_grid`: a few numbers side by side (two or three a row on an iPhone, all of them in a row when
/// there is room), each with what it is and a line under it in its tone.
struct ExtensionStatGrid: View {
    var stats: [ExtensionStat]

    var body: some View {
        let shown = Array(stats.prefix(6))
        ViewThatFits(in: .horizontal) {
            row(shown, columns: shown.count)
            grid(shown, columns: shown.count == 4 ? 2 : 3)
            grid(shown, columns: 2)
        }
        .padding(.vertical, Theme.Space.xs)
    }

    private func row(_ items: [ExtensionStat], columns: Int) -> some View {
        HStack(alignment: .top, spacing: Theme.Space.l) {
            ForEach(Array(items.enumerated()), id: \.offset) { _, s in cell(s).fixedSize() }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    private func grid(_ items: [ExtensionStat], columns: Int) -> some View {
        Grid(alignment: .topLeading, horizontalSpacing: Theme.Space.l, verticalSpacing: Theme.Space.m) {
            ForEach(Array(stride(from: 0, to: items.count, by: columns)), id: \.self) { start in
                GridRow {
                    ForEach(start..<min(start + columns, items.count), id: \.self) { i in
                        cell(items[i]).frame(maxWidth: .infinity, alignment: .leading)
                    }
                }
            }
        }
    }

    private func cell(_ s: ExtensionStat) -> some View {
        VStack(alignment: .leading, spacing: Theme.Space.xxs) {
            Text(s.label).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1)
            ExtensionChangingText(s.value).font(Theme.Font.heading.monospacedDigit()).lineLimit(1).minimumScaleFactor(0.6)
            if let d = s.detail, !d.isEmpty {
                Text(d).font(Theme.Font.label).foregroundStyle(ExtensionUIStyle.tone(s.tone)).lineLimit(1).minimumScaleFactor(0.8)
            }
        }
        .accessibilityElement(children: .combine)
    }
}

/// An extension's `segmented`: a few choices, one picked; picking another sends it to the extension at once (its
/// action, with `{name: value}`), whose answer is the view for it.
struct ExtensionSegmented: View {
    @Environment(\.extensionDisplayedRevision) private var displayed
    @Environment(AppModel.self) private var app
    var item: ExtensionSegmentedComponent
    var model: any ExtensionViewControlling
    @Binding var link: LinkTarget?

    var body: some View {
        let choices = Array(item.choices.prefix(5))
        if choices.count >= 2 {
            SegmentedChoice(item.name, choices.map { ($0.value, $0.label) }, selection: Binding(
                get: { model.values[item.name] ?? (item.value.isEmpty ? choices[0].value : item.value) },
                set: { v in
                    if let displayed {
                        let before = model.values[item.name]
                        model.values[item.name] = v
                        let execute = model.prepared(item.action, with: [item.name: v], displayed: displayed)
                        Task {
                            let outcome = await execute()
                            if outcome.error != nil || outcome.cancelled { model.values[item.name] = before }
                            ExtensionOutcome.show(outcome, app: app, link: $link)
                        }
                    } else { Task { ExtensionOutcome.show(await model.choose(item.name, v, action: item.action), app: app, link: $link) } }
                }))
            // It has no label of its own (the name is the extension's key): a Mac picker would write the key beside it.
            .labelsHidden()
            .disabled(!model.isEnabled(item.action))
            .accessibilityIdentifier("plugin-ui-segmented-\(item.name)")
            .padding(.vertical, Theme.Space.xxs)
        }
    }
}

/// A button or row that sends its action back to the extension, asking first when it says so (a `push` opens its page
/// without asking the extension); the answer's word is a toast, its link opens in the app.
struct ExtensionActionButton<Content: View>: View {
    @Environment(\.extensionDisplayedRevision) private var displayed
    @Environment(AppModel.self) private var app
    var action: ExtensionAction
    var title: String
    var destructive = false
    var model: any ExtensionViewControlling
    var values: [String: String] = [:]
    @Binding var link: LinkTarget?
    @ViewBuilder var label: Content
    @State private var asking = false

    var body: some View {
        Button(role: destructive ? .destructive : nil) {
            if action.confirm?.isEmpty == false, !model.managesConfirmation { asking = true } else { run() }
        } label: {
            label
        }
        // A Mac form draws a button in the accent whatever its role: red as on the phone (#1512).
        .tint(destructive ? Theme.danger : nil)
        .disabled(!model.isEnabled(action))
        .accessibilityIdentifier("plugin-ui-action-\(action.id)")
        .confirmationDialog(action.confirm ?? "", isPresented: $asking, titleVisibility: .visible) {
            Button(role: destructive ? .destructive : nil) { run() } label: { Text(title.isEmpty ? String(localized: "OK [Interactive Assistant View Card fac2a6]") : title).lineLimit(1) }
                .accessibilityIdentifier("plugin-ui-confirm")
            Button("Cancel", role: .cancel) {}
        }
    }

    private func run() {
        let execute = model.prepared(action, with: values, displayed: displayed)
        if model.managesConfirmation && displayed == nil { Keyboard.dismiss() }
        Task {
            // Let the conversation's simultaneous focus-dismiss gesture settle before presenting a native alert.
            if model.managesConfirmation && displayed == nil { await Task.yield() }
            ExtensionOutcome.show(await execute(), app: app, link: $link)
        }
    }
}

/// A field of an extension's View: text, a secret (typed in hidden, never filled in for you), a number, a switch, a
/// choice.
struct ExtensionFieldItem: View {
    @Environment(\.extensionFieldFocus) private var focus
    @Environment(\.extensionDisplayedRevision) private var displayed
    @Environment(\.scenePhase) private var phase
    @State private var secretDraft = ""
    var field: ExtensionFieldComponent
    var model: any ExtensionViewControlling

    private var name: String { field.name }
    private var label: String { field.label.isEmpty ? name : field.label }
    private var privateInput: Bool {
        guard let page = model as? ExtensionViewModel else { return false }
        return page.stream?.tree != nil
    }
    private var text: Binding<String> {
        if field.kind == "secret", privateInput {
            return Binding(get: { secretDraft }, set: { secretDraft = $0 })
        }
        return Binding(get: { model.values[name] ?? "" }, set: { model.values[name] = $0 })
    }

    var body: some View {
        Group {
            switch field.kind {
            case "toggle":
                Toggle(isOn: Binding(get: { model.values[name] == "true" }, set: { model.values[name] = $0 ? "true" : "false" })) {
                    Text(label).lineLimit(2)
                }
            case "choice":
                Picker(selection: text) {
                    if !(field.choices ?? []).contains(where: { $0.value == (model.values[name] ?? "") }) {
                        Text(field.placeholder ?? String(localized: "None [Extension Components 3005ea]")).tag(model.values[name] ?? "")
                    }
                    ForEach(field.choices ?? []) { c in Text(c.label).tag(c.value) }
                } label: {
                    Text(label).lineLimit(2)
                }
            case "secret":
                labeled {
                    SecretField(field.placeholder ?? "", filled: privateInput ? String(localized: "Hidden") : String(localized: "Filled in; sent when you submit"), text: text)
                        .modifier(ExtensionFieldFocus(binding: focus, key: field.key ?? name))
                }
            case "number":
                labeled {
                    TextField(field.placeholder ?? "", text: text).keyboardType(.decimalPad)
                        .modifier(ExtensionFieldFocus(binding: focus, key: field.key ?? name))
                }
            default:
                labeled {
                    TextField(field.placeholder ?? "", text: text)
                        .modifier(ExtensionFieldFocus(binding: focus, key: field.key ?? name))
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                }
            }
        }
        .disabled(!model.fieldsEnabled)
        .accessibilityIdentifier("plugin-ui-field-\(name)")
        .onDisappear { secretDraft = "" }
        .onChange(of: displayed) { _, _ in secretDraft = "" }
        .onChange(of: field.kind) { _, _ in secretDraft = "" }
        .onChange(of: phase) { _, phase in if phase != .active { secretDraft = "" } }
        .onTabVisibility(appear: {}, disappear: { secretDraft = "" })
    }

    /// The label above the field.
    private func labeled(@ViewBuilder _ content: () -> some View) -> some View {
        VStack(alignment: .leading, spacing: Theme.Space.xs) {
            Text(label).font(Theme.Font.labelStrong).foregroundStyle(.secondary).lineLimit(2)
            #if os(macOS)
            content().labelsHidden() // a Mac form would put the label beside an empty box (#1221)
            #else
            content()
            #endif
        }
        .padding(.vertical, Theme.Space.xxs)
    }
}
