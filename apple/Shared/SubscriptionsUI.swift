import Foundation

/// Source subscriptions (#2041, docs/extensions/subscriptions.md "Pages"): the manager draws their pages as declarative UI at
/// `POST /api/v1/subscriptions/-/ui`, and the app opens them as an extension page under a reserved id.
nonisolated enum SubscriptionsUI {
    /// The id the pages are drawn under: no extension can have it (extension ids never start with "-").
    static let id = "-subscriptions"
    /// The pages' one interface.
    static let page = "pages"
    /// In the hub's features once subscriptions deliver to the Inbox and the knowledge base: the entry rows show only then.
    static let feature = "source_subscriptions"
    /// The manager's route for drawing and pressing the pages.
    static let path = "/api/v1/subscriptions/-/ui"

    /// The pages' first state: every subscription, one extension's or one connector's.
    static func state(extension ext: String? = nil, connector: String? = nil) -> String? {
        let scope: [String: String] = ext.map { ["ext": $0] } ?? connector.map { ["c": $0] } ?? [:]
        guard !scope.isEmpty, let data = try? JSONSerialization.data(withJSONObject: scope, options: [.sortedKeys]) else { return nil }
        return String(data: data, encoding: .utf8)
    }
}
