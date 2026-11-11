import Foundation
import Testing
@testable import Persona

/// Source subscriptions (#2041): the pages under their reserved id, the entry rows' counts and the event that redraws them.
struct SubscriptionsUITests {
    @Test func thePagesFirstStateNamesTheScope() throws {
        #expect(SubscriptionsUI.state() == nil)
        #expect(SubscriptionsUI.state(extension: "apps") == #"{"ext":"apps"}"#)
        #expect(SubscriptionsUI.state(connector: "github") == #"{"c":"github"}"#)
        #expect(SubscriptionsUI.id.hasPrefix("-"), "no extension can have the id")
    }

    @Test func countsShowOnlyWhenSomethingCanBeSubscribed() throws {
        let lines = try JSONDecoder().decode([SubscriptionSummaryLine].self, from: Data(#"""
            [{"connector":"apps","extension":"apps","subscribable":true,"on":2,"failing":1},
             {"connector":"github","extension":"","on":0,"failing":0},
             {"connector":"notes","subscribable":false,"on":0,"failing":0},
             {"connector":"old","subscribable":false,"on":1,"failing":0}]
            """#.utf8))
        #expect(SubscriptionCounts.of(lines) { $0.extensionID == "apps" } == SubscriptionCounts(on: 2, failing: 1))
        #expect(SubscriptionCounts.of(lines) { $0.connector == "github" } == nil, "not read yet: hidden")
        #expect(SubscriptionCounts.of(lines) { $0.connector == "notes" } == nil, "nothing to subscribe: hidden")
        #expect(SubscriptionCounts.of(lines) { $0.connector == "old" } == SubscriptionCounts(on: 1, failing: 0), "tools gone, subscriptions kept")
        #expect(SubscriptionCounts(on: 0, failing: 0).text != SubscriptionCounts(on: 2, failing: 1).text)
    }

    @Test func aSubscriptionEventRedrawsThePages() {
        let event = ServerEvent.decode(Data(#"{"type":"subscription","id":"sub_1","connector":"apps","revision":3,"state":"ok","removed":false}"#.utf8))
        guard case .extensionInvalidate(let ext, let id)? = event else { Issue.record("not decoded: \(String(describing: event))"); return }
        #expect(ext == SubscriptionsUI.id)
        #expect(id == nil)
        // Other events still decode as before.
        guard case .extensionInvalidate("demo", "home")? = ServerEvent.decode(Data(#"{"type":"extension_ui","extension":"demo","id":"home","invalidate":true}"#.utf8)) else {
            Issue.record("extension_ui no longer decodes"); return
        }
    }
}
