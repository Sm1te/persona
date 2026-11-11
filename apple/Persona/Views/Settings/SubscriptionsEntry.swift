import SwiftUI

// Source subscriptions' entry rows (#2041, docs/extensions/subscriptions.md "Pages"): on an extension's details and a connector's
// page, shown once the hub reports `source_subscriptions` and only for a connector that can be subscribed (or already is).
// The pages themselves are the manager's, drawn by ExtensionPageView under `SubscriptionsUI.id`.

/// One connector's line of `GET /api/v1/subscriptions/summary`: `subscribable` is nil until its catalog was read.
nonisolated struct SubscriptionSummaryLine: Decodable, Equatable, Sendable {
    var connector: String
    var extensionID: String
    var subscribable: Bool?
    var on: Int
    var failing: Int

    init(connector: String, extensionID: String = "", subscribable: Bool? = nil, on: Int = 0, failing: Int = 0) {
        self.connector = connector
        self.extensionID = extensionID
        self.subscribable = subscribable
        self.on = on
        self.failing = failing
    }

    private enum CodingKeys: String, CodingKey { case connector, extensionID = "extension", subscribable, on, failing }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        connector = try c.decode(String.self, forKey: .connector)
        extensionID = try c.decodeIfPresent(String.self, forKey: .extensionID) ?? ""
        subscribable = try c.decodeIfPresent(Bool.self, forKey: .subscribable)
        on = try c.decodeIfPresent(Int.self, forKey: .on) ?? 0
        failing = try c.decodeIfPresent(Int.self, forKey: .failing) ?? 0
    }
}

/// What an entry row says: how many subscriptions are on and how many need attention.
nonisolated struct SubscriptionCounts: Equatable, Sendable {
    var on: Int
    var failing: Int

    /// The counts for the lines one row stands for; nil when none of them can be subscribed (the row is hidden).
    static func of(_ lines: [SubscriptionSummaryLine], where pick: (SubscriptionSummaryLine) -> Bool) -> SubscriptionCounts? {
        let mine = lines.filter(pick)
        guard mine.contains(where: { $0.subscribable == true || $0.on > 0 }) else { return nil }
        return SubscriptionCounts(on: mine.reduce(0) { $0 + $1.on }, failing: mine.reduce(0) { $0 + $1.failing })
    }

    var text: String {
        if failing > 0 { return String(localized: "\(on) on · \(failing) failing [Source Subscriptions]") }
        return on > 0 ? String(localized: "\(on) on [Source Subscriptions]") : String(localized: "Available [Source Subscriptions]")
    }
}

extension APIClient {
    /// `GET /api/v1/subscriptions/summary`: the entry rows' counts, from what the manager has cached.
    func subscriptionSummary() async throws -> [SubscriptionSummaryLine] {
        struct Summary: Decodable { var connectors: [SubscriptionSummaryLine]? }
        let summary: Summary = try await get("/api/v1/subscriptions/summary")
        return summary.connectors ?? []
    }
}

/// Bumped by each `subscription` event (AppModel), so the entry rows read the summary again.
@MainActor @Observable final class SubscriptionsRefresh {
    static let shared = SubscriptionsRefresh()
    private(set) var revision = 0
    func changed() { revision += 1 }
}

/// A Subscriptions row in its own section; nothing while `counts` is nil.
struct SubscriptionsEntrySection: View {
    var counts: SubscriptionCounts?
    /// The pages' first state (`SubscriptionsUI.state`).
    var state: String?

    var body: some View {
        if let counts {
            Section {
                NavigationLink(value: SettingsPage.subscriptions(state: state)) {
                    Label {
                        VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                            Text("Subscriptions [Source Subscriptions Entry]").lineLimit(1)
                            Text(counts.text).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1)
                        }
                    } icon: {
                        ThemedIcon(Theme.Icon.subscriptions)
                    }
                }
                .accessibilityIdentifier("subscriptions-entry")
            }
        }
    }
}

extension View {
    /// Reads the counts for an entry row on this page: once the hub shows subscriptions, and again (a second after the last of a
    /// burst) whenever one changes. Another manager's page passes `enabled: false`: subscriptions are the default manager's.
    func subscriptionCounts(_ counts: Binding<SubscriptionCounts?>, enabled: Bool = true,
                            where pick: @escaping (SubscriptionSummaryLine) -> Bool) -> some View {
        modifier(SubscriptionCountsLoader(counts: counts, enabled: enabled, pick: pick))
    }
}

private struct SubscriptionCountsLoader: ViewModifier {
    @Environment(AppModel.self) private var app
    @Binding var counts: SubscriptionCounts?
    var enabled: Bool
    var pick: (SubscriptionSummaryLine) -> Bool

    private struct Key: Equatable { var shown: Bool; var revision: Int }

    func body(content: Content) -> some View {
        let shown = enabled && app.hubFeatures.contains(SubscriptionsUI.feature)
        content.task(id: Key(shown: shown, revision: SubscriptionsRefresh.shared.revision)) {
            guard shown else { counts = nil; return }
            if SubscriptionsRefresh.shared.revision > 0 { try? await Task.sleep(for: .seconds(1)) }
            guard !Task.isCancelled, let api = app.api, let lines = try? await api.subscriptionSummary() else { return }
            counts = SubscriptionCounts.of(lines, where: pick) // a failed read keeps what the row had
        }
    }
}

/// The pages from the Inbox's menu, every subscription: a sheet, as the Inbox's Muted and Rules are.
struct SubscriptionsSheet: View {
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            ExtensionPageView(plugin: SubscriptionsUI.id, ui: SubscriptionsUI.page, title: String(localized: "Subscriptions [Source Subscriptions]"))
                .toolbar {
                    ToolbarItem(placement: .confirmationAction) {
                        Button { dismiss() } label: { Text("Done [Extension System Presentation c0b3fb]").lineLimit(1) }
                    }
                }
        }
    }
}
