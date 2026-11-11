import BeautifulUI
import Observation
import SwiftUI

// The UI slots (docs/extensions/extensions.md「UI slots」): where plugins put their declarative UI beyond their own settings.
//
// | placement        | iPhone / iPad                                             | Mac                                   |
// | ---------------- | --------------------------------------------------------- | ------------------------------------- |
// | nav_page         | the sidebar / the shell's drawer after the places to go, and | the sidebar, before Settings          |
// |                  | Settings home's Extensions section at the top             |                                       |
// | overview_card    | Settings home, above those entries                        | Settings › Extensions, above the list |
// | machine_section  | a machine's page, after its own sections                  | the same page                         |
//
// Only with a hub that says `plugin_ui_slots`; an older hub is never asked about them.

/// The plugins' main-navigation entries and overview cards of the hub, with their badges (`plugin_ui` events change
/// them in place).
@MainActor @Observable final class ExtensionSlots {
    static let shared = ExtensionSlots()

    /// nav_page entries, in the hub's order.
    private(set) var pages: [ExtensionUIPlace] = []
    /// overview_card entries, in the hub's order.
    private(set) var cards: [ExtensionUIPlace] = []
    /// The cards' models, made when the cards are listed (the page pushes what they ask for, `extensionPushes`).
    private var cardModels: [String: ExtensionViewModel] = [:]
    @ObservationIgnored private var lastHub: String?
    @ObservationIgnored private var reloadGeneration = 0
    @ObservationIgnored private let deps: ExtensionViewModel.Deps
    @ObservationIgnored private let features: () -> Set<String>

    init(deps: ExtensionViewModel.Deps = .live, features: @escaping () -> Set<String> = { AppModel.shared.hubFeatures }) {
        self.deps = deps
        self.features = features
    }

    /// The hub has the slots (and so the context, charts and badges).
    var available: Bool { features().hasExtensionFeature("ui_slots") }

    /// Asks the hub again for the entries and the cards (a list that cannot be read keeps what there was).
    func reload(for hub: String? = nil) async {
        reloadGeneration += 1
        let generation = reloadGeneration
        // Another hub: nothing of the last one's stays on screen while this one's are read.
        if let hub, hub != lastHub {
            lastHub = hub
            pages = []
            cards = []
            cardModels = [:]
        }
        guard available, let api = deps.api() else {
            pages = []
            cards = []
            cardModels = [:]
            return
        }
        // No placement: every slot's at once (not launchers' sections), in the hub's order.
        guard let all = try? await api.pluginUI(placement: nil), generation == reloadGeneration else { return }
        // A slower reply cannot restore another hub's slots, or slots whose feature was removed.
        do {
            let next = all.filter { $0.placement == "nav_page" }
            if next != pages { pages = next }
        }
        do {
            let next = all.filter { $0.placement == "overview_card" }
            if next != cards { cards = next }
            var models = cardModels.filter { k, _ in next.contains { Self.key($0) == k } }
            for c in next where models[Self.key(c)] == nil {
                models[Self.key(c)] = ExtensionViewModel(plugin: c.owner, ui: c.id, context: .here(size: "compact"), deps: deps)
            }
            cardModels = models
        }
    }

    /// A plugin changed an entry's badge (nil: none).
    func received(plugin: String, id: String, badge: ExtensionBadge?) {
        guard let i = pages.firstIndex(where: { $0.owner == plugin && $0.id == id }), pages[i].badge != badge else { return }
        pages[i].badge = badge
    }

    /// A card's model, kept while the card is listed (a card drawn again keeps its state).
    func model(for card: ExtensionUIPlace) -> ExtensionViewModel {
        cardModels[Self.key(card)]
            ?? ExtensionViewModel(plugin: card.owner, ui: card.id, context: .here(size: "compact"), deps: deps)
    }

    /// The cards' models, for the page that pushes what they ask for.
    var cardPages: [ExtensionViewModel] { cards.compactMap { cardModels[Self.key($0)] } }

    /// The page a card opens (`opens`: the plugin's nav_page or plugin_page), with its title.
    func opened(by card: ExtensionUIPlace) -> (ui: String, title: String)? {
        guard let id = card.opens, !id.isEmpty else { return nil }
        let title = pages.first { $0.owner == card.owner && $0.id == id }?.title ?? card.title
        return (id, title)
    }

    /// What the entries' badges add up to on Settings (and its tab): their numbers summed; only words, a dot.
    var total: (count: Int, dot: Bool) {
        let badges = pages.compactMap(\.badge)
        let count = badges.compactMap { Int($0.text) }.filter { $0 > 0 }.reduce(0, +)
        return (count, count == 0 && badges.contains { Int($0.text) == nil })
    }

    static func key(_ p: ExtensionUIPlace) -> String { p.owner + "/" + p.id }

    /// What the lists depend on: which hub (paired again with another, or another group picked) and whether it has slots.
    nonisolated struct Source: Hashable, Sendable {
        var hub: String?
        var slots: Bool
    }

    static func source(_ app: AppModel) -> Source {
        Source(hub: app.isPaired ? app.config?.url.absoluteString : nil, slots: app.hubFeatures.hasExtensionFeature("ui_slots"))
    }

    /// An entry's icon: one of the few a plugin may ask for, anything else the plugin's.
    static func icon(_ name: String?) -> String { ExtensionUIStyle.icon(name) }
}

/// An extension's main-navigation page opened from the sidebar or the shell's drawer (#2961).
nonisolated struct ExtensionNavRoute: Hashable, Sendable, Identifiable {
    var owner: String
    var ui: String
    var title: String
    var id: String { owner + "/" + ui }

    init(owner: String, ui: String, title: String) {
        self.owner = owner
        self.ui = ui
        self.title = title
    }

    init(_ p: ExtensionUIPlace) { self.init(owner: p.owner, ui: p.id, title: p.title) }
}

/// The page for it: the extension's View, drawn again from the start for another entry.
struct ExtensionNavPage: View {
    var route: ExtensionNavRoute

    var body: some View {
        ExtensionPageView(plugin: route.owner, ui: route.ui, title: route.title).id(route.id)
    }
}

// MARK: Badges

/// A plugin's badge: a number as the count badges are drawn, a word as a chip; in its tone (neutral: the usual red of a
/// count, a quiet chip).
struct ExtensionBadgeView: View {
    var badge: ExtensionBadge

    var body: some View {
        if let n = Int(badge.text), n > 0 {
            CountBadge(count: n, look: .solid(badge.tone == nil || badge.tone == "neutral" ? Theme.countBadge : ExtensionUIStyle.tone(badge.tone)))
        } else {
            Chip(text: badge.text, look: badge.tone == nil || badge.tone == "neutral" ? .neutral : .tint(ExtensionUIStyle.tone(badge.tone)))
                .lineLimit(1)
        }
    }
}

/// What the plugins' entries' badges add up to, on a way to Settings that is not its tab (the shell drawer's Settings button):
/// the sum as a count badge, only words a dot, nothing without badges.
struct ExtensionSettingsBadge: View {
    var body: some View {
        let total = ExtensionSlots.shared.total
        if total.count > 0 {
            CountBadge(count: total.count, look: .neutral)
                .offset(x: Theme.Space.xs, y: -Theme.Space.xs)
        } else if total.dot {
            Circle().fill(Theme.countBadge).scaledFrame(width: Theme.Size.dot, height: Theme.Size.dot)
        }
    }
}

// MARK: Entries and cards on Settings (iPhone, iPad)

/// Settings home's Extensions section, at the top: the plugins' cards, then their pages, one row each (icon, title, badge).
/// Nothing when no plugin puts anything there.
struct ExtensionSlotsSettingsSection: View {
    @Environment(AppModel.self) private var model
    /// Where the cards' links open: the page's (`pluginLinks` on the page, not on a section of its list).
    @Binding var link: LinkTarget?
    private var slots: ExtensionSlots { .shared }

    var body: some View {
        if slots.available {
            ExtensionCardsSection(link: $link)
            if !slots.pages.isEmpty {
                Section {
                    Group {
                        ForEach(slots.pages) { page in
                            NavigationLink(value: SettingsPage.pluginPage(plugin: page.owner, ui: page.id, title: page.title)) {
                                ExtensionNavRow(place: page)
                            }
                            .settingsListRow(SettingsPage.pluginPage(plugin: page.owner, ui: page.id, title: page.title))
                            .accessibilityIdentifier("plugin-nav-\(page.owner)-\(page.id)")
                        }
                    }
                    .bareListRow()
                } header: {
                    SettingsHeading(String(localized: "Extensions"))
                }
            }
        }
    }
}

/// One plugin page on the navigation: its icon in the plugins' tile, its title on one line, its badge.
struct ExtensionNavRow: View {
    var place: ExtensionUIPlace

    var body: some View {
        let symbol = ExtensionSlots.icon(place.icon)
        if AppStyle.current.usesBeautifulUI {
            BUIListRow(place.title) {
                IconTile(symbol, tint: Theme.Hue.purple)
            } trailing: {
                if let b = place.badge { ExtensionBadgeView(badge: b) }
            }
        } else {
            LabeledContent {
                if let b = place.badge { ExtensionBadgeView(badge: b) }
            } label: {
                Label { Text(place.title).lineLimit(1) } icon: { ThemedIcon(symbol) }
            }
        }
    }
}

/// The plugins' overview cards as sections of a settings list: the card's title (with a chevron when it opens a page),
/// its View drawn compact. Tapping a card that opens a page pushes that page on Settings.
struct ExtensionCardsSection: View {
    @Environment(AppModel.self) private var model
    /// Where the cards' links open: the page's. On a Mac the link panel lays the page out beside it, which a list's
    /// section cannot hold (the card came out as its title alone).
    @Binding var link: LinkTarget?
    private var slots: ExtensionSlots { .shared }

    var body: some View {
        ForEach(slots.cards) { card in
            Section {
                ExtensionCard(card: card, model: slots.model(for: card), link: $link) {
                    if let (ui, title) = slots.opened(by: card) {
                        model.settingsPath.append(SettingsPage.pluginPage(plugin: card.owner, ui: ui, title: title))
                    }
                }
                .accessibilityIdentifier("plugin-card-\(card.owner)-\(card.id)")
            }
        }
    }
}

/// One overview card: its title line, then the plugin's View, compact. While loading a line of progress, a failure
/// one line with Retry; a plugin that says it has nothing here draws nothing.
struct ExtensionCard: View {
    var card: ExtensionUIPlace
    var model: ExtensionViewModel
    @Binding var link: LinkTarget?
    var open: () -> Void

    private var opens: Bool { card.opens?.isEmpty == false }

    var body: some View {
        if !model.unsupported {
            content
        }
    }

    private var content: some View {
            VStack(alignment: .leading, spacing: Theme.Space.m) {
                HStack(spacing: Theme.Space.s) {
                    Text(model.view?.title ?? card.title).font(Theme.Font.labelStrong).lineLimit(1)
                    Spacer(minLength: Theme.Space.s)
                    if opens {
                        ThemedIcon(Theme.Icon.disclosure).font(Theme.Font.labelStrong).foregroundStyle(.tertiary)
                    }
                }
                if let v = model.view {
                    ExtensionViewStack(view: v, model: model, cards: false, link: $link)
                        .environment(\.pluginUICompact, true)
                        .buttonStyle(.borderless)
                } else if let e = model.error {
                    HStack(spacing: Theme.Space.s) {
                        Label(e, systemImage: Theme.Icon.error).font(Theme.Font.label).foregroundStyle(Theme.danger).lineLimit(2)
                        Spacer(minLength: Theme.Space.s)
                        Button { Task { await model.load() } } label: { Text("Retry [Components b8784c]").lineLimit(1) }
                            .buttonStyle(.borderless)
                    }
                } else {
                    HStack(spacing: Theme.Space.s) {
                        ProgressView().controlSize(.small)
                        Text("Loading… [File Viewer 86b6d0]").font(Theme.Font.label).foregroundStyle(.secondary)
                    }
                }
            }
            .padding(.vertical, Theme.Space.xs)
            .contentShape(.rect)
            .onTapGesture { if opens { open() } }
            .accessibilityElement(children: .contain)
            .accessibilityAddTraits(opens ? .isButton : [])
            .task { if model.view == nil { await model.load() } }
            .pluginRefresh(model)
    }
}

// MARK: A machine's page

/// What plugins draw on a machine's page (placement machine_section), for that machine: its hub group's API, the
/// machine's id as context. The page owns it and loads it (so an empty list adds nothing to the page, not even spacing).
@MainActor @Observable final class ExtensionMachineModel {
    private(set) var places: [ExtensionUIPlace] = []
    private(set) var models: [String: ExtensionViewModel] = [:]

    /// Asks the hub what plugins draw for `ref`; `redraw` (pull to refresh) draws the ones already there again.
    func load(_ ref: MachineRef, app: AppModel, redraw: Bool) async {
        guard app.features(ref.group).hasExtensionFeature("ui_slots"), let api = app.api(ref.group) else {
            places = []
            return
        }
        guard let list = try? await api.pluginUI(placement: "machine_section", machine: ref.id) else { return }
        let shown = list.filter { $0.placement.isEmpty || $0.placement == "machine_section" }
        let deps = ExtensionViewModel.Deps(api: { [weak app] in app?.api(ref.group) })
        var next: [String: ExtensionViewModel] = [:]
        for p in shown {
            next[ExtensionSlots.key(p)] = models[ExtensionSlots.key(p)]
                ?? ExtensionViewModel(plugin: p.owner, ui: p.id, context: ExtensionUIContext.here(machine: ref.id), deps: deps)
        }
        let kept = Set(models.keys)
        models = next
        if shown != places { places = shown }
        guard redraw else { return }
        let again = next.filter { kept.contains($0.key) }.map(\.value)
        await withTaskGroup(of: Void.self) { group in
            for m in again { group.addTask { await m.load() } }
        }
    }
}

/// The plugins' sections on a machine's page, after its own: each a heading (the contribution's title) and its View's
/// sections as the page's other cards. A plugin that says it has nothing for this machine draws nothing.
struct ExtensionMachineSections: View {
    var page: ExtensionMachineModel
    /// Where links open: the machine page's.
    @Binding var link: LinkTarget?

    var body: some View {
        ForEach(page.places) { place in
            if let m = page.models[ExtensionSlots.key(place)], !m.unsupported {
                ExtensionMachineSection(place: place, model: m, link: $link)
            }
        }
    }
}

private struct ExtensionMachineSection: View {
    var place: ExtensionUIPlace
    var model: ExtensionViewModel
    @Binding var link: LinkTarget?

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.m) {
            Text(model.view?.title ?? place.title).font(Theme.Font.heading).lineLimit(1)
                .padding(.horizontal, Theme.Space.xs)
            if let v = model.view {
                ExtensionViewStack(view: v, model: model, link: $link)
            } else if let e = model.error {
                VStack(alignment: .leading, spacing: Theme.Space.s) {
                    Label(e, systemImage: Theme.Icon.error).font(Theme.Font.label).foregroundStyle(Theme.danger)
                    Button { Task { await model.load() } } label: { Text("Retry [Components b8784c]").lineLimit(1) }
                        .accessibilityIdentifier("plugin-ui-retry")
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .card()
            } else {
                HStack(spacing: Theme.Space.s) {
                    ProgressView().controlSize(.small)
                    Text("Loading… [File Viewer 86b6d0]").font(Theme.Font.label).foregroundStyle(.secondary)
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .card()
            }
        }
        .accessibilityIdentifier("plugin-machine-\(place.owner)-\(place.id)")
        .task { if model.view == nil { await model.load() } }
        .pluginRefresh(model)
    }
}

// MARK: Refresh

nonisolated private struct ExtensionRefreshKey: Hashable, Sendable {
    var due: Date?
    var active: Bool
    var invalidation: Int
    var completion: Int
}

/// Draws an extension's View again when its `refresh_after` is up or `ui.invalidate` came for it, only while it is on
/// screen with the app in front; coming back after the time has passed, or after an invalidate, draws it at once.
private struct ExtensionRefresh: ViewModifier {
    var model: ExtensionViewModel
    @Environment(\.scenePhase) private var phase

    func body(content: Content) -> some View {
        content.tabActiveTask(id: ExtensionRefreshKey(due: model.refreshDue, active: phase == .active,
                                                      invalidation: model.invalidationVersion,
                                                      completion: model.stale ? model.completedOperations : 0)) {
            guard phase == .active else { return }
            if model.stale {
                await model.refresh()
                return
            }
            guard let due = model.refreshDue else { return }
            let wait = due.timeIntervalSinceNow
            if wait > 0 { try? await Task.sleep(for: .seconds(wait)) }
            guard !Task.isCancelled else { return }
            await model.refresh()
        }
    }
}

extension View {
    /// Draws `model`'s View again after its `refresh_after` while on screen (docs/extensions/extensions.md「UI slots」, refreshing).
    func pluginRefresh(_ model: ExtensionViewModel) -> some View { modifier(ExtensionRefresh(model: model)) }
}
