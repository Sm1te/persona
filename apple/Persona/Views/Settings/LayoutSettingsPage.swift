import SwiftUI

#if os(iOS)
/// Settings › Appearance › Layout (#2530): how the Agents list draws its agents, each choice showing two agents as the
/// list would draw them. Every grouping draws them so, By Repository too (#3753).
struct LayoutSettingsPage: View {
    private let choice = AgentListDisplayChoice()

    var body: some View {
        SettingsForm {
            Section {
                ForEach(AgentListDisplay.allCases) { display in
                    Button { choice.display = display } label: {
                        LayoutChoice(display: display, selected: choice.display == display)
                    }
                    .buttonStyle(.plain)
                    .accessibilityIdentifier("layout-agents-" + display.rawValue)
                    .accessibilityAddTraits(choice.display == display ? .isSelected : [])
                }
            } header: {
                Text("Agents List [Layout]")
            } footer: {
                if AppStyle.current.usesBeautifulUI {
                    Text("The BeautifulUI style draws its agents as its own capsules; this choice shows in the other styles and in Compact List.")
                }
            }
        }
        .themedGroupedPage()
        .detailTitle(String(localized: "Layout"))
        .navigationBarTitleDisplayMode(.inline)
    }
}

/// One way to draw the Agents list: its name, two agents drawn that way, and what it shows.
private struct LayoutChoice: View {
    private func card<Content: View>(@ViewBuilder _ content: () -> Content) -> some View {
        VStack(alignment: .leading, spacing: Theme.Space.s) { content() }
            .padding(Theme.Space.m)
            .background(Theme.card, in: .rect(cornerRadius: Theme.Radius.card))
    }

    var display: AgentListDisplay
    var selected: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            HStack(spacing: Theme.Space.s) {
                Text(display.title).font(Theme.Font.bodyStrong).lineLimit(1)
                Spacer(minLength: Theme.Space.s)
                if selected {
                    ThemedIcon(Theme.Icon.done).font(Theme.Font.bodyStrong).foregroundStyle(Theme.accent)
                }
            }
            preview
                .allowsHitTesting(false)
                .accessibilityHidden(true)
            Text(display.summary).font(Theme.Font.caption).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
        .padding(.vertical, Theme.Space.xs)
        .contentShape(.rect)
        .accessibilityElement(children: .combine)
    }

    @ViewBuilder private var preview: some View {
        let rows = LayoutSamples.sessions
        // On the page's color as the list is: the rows on the list's card, State Cards each a card of its own.
        Group {
            switch display.style {
            case .cards?:
                VStack(spacing: Theme.Space.s) {
                    ForEach(rows) { SlotRow(session: $0).styled(.cards, dense: false) }
                }
            case let style?:
                card {
                    ForEach(rows) { SlotRow(session: $0).styled(style, dense: false) }
                }
            case nil:
                card { ForEach(rows) { CompactSlotRow(session: $0) } }
            }
        }
        .padding(Theme.Space.s)
        .background(Theme.groupedPage, in: .rect(cornerRadius: Theme.Radius.card))
    }
}

/// The two agents the Layout page draws: one waiting for you, one at work. Made up, in the app's language.
private enum LayoutSamples {
    @MainActor static let sessions: [Session] = [
        sample(id: "layout-1", title: String(localized: "Add tests for the network layer [Layout sample]"), state: "needs_you",
               words: String(localized: "Which test framework should it use? I'd go with Swift Testing. [Layout sample]"),
               slot: "tests", minutesAgo: 2),
        sample(id: "layout-2", title: String(localized: "Add the weather widget to the home screen [Layout sample]"), state: "working",
               words: String(localized: "Running the tests: 128 of 160 pass so far. [Layout sample]"), slot: "widget", minutesAgo: 0),
    ].compactMap { $0 }

    private static func sample(id: String, title: String, state: String, words: String, slot: String, minutesAgo: Int) -> Session? {
        let fields: [String: Any] = [
            "machine_id": "layout", "session_id": id, "title": title, "cwd": "/", "status": state == "working" ? "working" : "blocked",
            "state": state, "space_name": "weather", "slot_name": slot, "pane_id": "p-" + id, "last_text": words,
            "attention_reason": state == "needs_you" ? words : "",
            "updated_at": ISO8601DateFormatter().string(from: Date.now.addingTimeInterval(TimeInterval(-60 * minutesAgo))),
        ]
        guard let data = try? JSONSerialization.data(withJSONObject: fields) else { return nil }
        return try? JSON.decoder.decode(Session.self, from: data)
    }
}
#endif
