import PersonaKit
import SwiftUI

/// Which All Chats the sidebar opens: filtered to one source of Auto Chats (`AutoConversationSource.id`), or all (nil).
struct AutoConversationsRoute: Identifiable, Hashable {
    var source: String?
    var id: String { source ?? "" }
}

/// All Chats (#3264, docs/protocol/protocol.md「Sidebar」): the assistant's conversations, newest first, filtered along the top by
/// who opened them: All, Mine (the user's own), then each source of Auto Chats that has any. The sidebar's Auto Chats opens
/// it on a source. A tap opens one; a swipe or a long press archives it, as in the sidebar.
struct AutoConversationsView: View {
    /// What the list shows: every conversation, the user's own, or one source's.
    enum Filter: Hashable {
        case all, mine
        case source(String)

        var id: String {
            switch self {
            case .all: "all"
            case .mine: "mine"
            case .source(let id): id
            }
        }
    }

    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    @State private var filter: Filter
    private let taskChats = AssistantTaskChatsStore.shared
    var open: (SessionKey) -> Void

    init(source: String?, open: @escaping (SessionKey) -> Void) {
        _filter = State(initialValue: source.map(Filter.source) ?? .all)
        self.open = open
    }

    var body: some View {
        let everyone = model.assistantConversations.filter { $0.key != model.assistantMain }
        let split = AutoConversations(everyone, tasks: taskChats.known(on: model.api), current: nil,
                                      hubSays: model.hubFeatures.contains(AutoConversationSource.feature))
        let groups = split.bySource(tasks: taskChats.known(on: model.api))
        let shown = AssistantSidebarGroups.newestFirst(Self.list(filter, all: everyone, mine: split.mine, groups: groups))
        NavigationStack {
            List {
                filters(groups, all: everyone.count, mine: split.mine.count)
                    .themedRow()
                    .listRowSeparator(.hidden)
                ForEach(shown) { s in row(s).themedRow() }
            }
            .listStyle(.plain)
            .themedPage()
            .overlay {
                if shown.isEmpty {
                    EmptyStateView(symbol: Theme.Icon.autoConversations, title: String(localized: "No Chats Here"),
                                   message: String(localized: "Chats started by the Inbox's AI twin, scheduled tasks and Lark /agent are collected here by source."))
                }
            }
            .navigationTitle("All Chats")
            #if os(iOS) || os(visionOS)
            .navigationBarTitleDisplayMode(.inline)
            #endif
            #if os(macOS)
            .macSheetDone { dismiss() }
            #else
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button("Done [Extension System Presentation c0b3fb]") { dismiss() }.lineLimit(1)
                }
            }
            #endif
        }
        #if os(macOS)
        .macSheet()
        #endif
    }

    private static func list(_ filter: Filter, all: [Session], mine: [Session], groups: [AutoConversations.Group]) -> [Session] {
        switch filter {
        case .all: all
        case .mine: mine
        case .source(let id): groups.first { $0.source.id == id }?.sessions ?? []
        }
    }

    /// All, Mine and each source, the chosen one tinted.
    private func filters(_ groups: [AutoConversations.Group], all: Int, mine: Int) -> some View {
        FlowLayoutRow {
            chip(String(localized: "All"), symbol: Theme.Icon.list, count: all, filter: .all)
            chip(String(localized: "Mine"), symbol: Theme.Icon.user, count: mine, filter: .mine)
            ForEach(groups, id: \.source) { g in chip(g.source.name, symbol: g.source.symbol, count: g.sessions.count, filter: .source(g.source.id)) }
        }
        .padding(.vertical, Theme.Space.xs)
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    private func chip(_ name: String, symbol: String, count: Int, filter f: Filter) -> some View {
        let chosen = filter == f
        return Button {
            withAnimation(Theme.Motion.state) { filter = f }
        } label: {
            Chip(text: "\(name) \(count)", symbol: symbol, look: chosen ? .tint(Theme.accent) : .neutral)
                .contentShape(.capsule)
        }
        .buttonStyle(.plain)
        .accessibilityLabel("\(name), \(count) [Auto Conversations View 9c72ba]")
        .accessibilityAddTraits(chosen ? .isSelected : [])
        .accessibilityIdentifier("auto-filter-" + f.id)
    }

    private func row(_ s: Session) -> some View {
        let title = s.title.isEmpty || s.title == "Claude Code" ? String(localized: "New Chat [Quick Actions ea4b29]") : s.title
        let from = AutoConversations.source(of: s, tasks: taskChats.known(on: model.api))
        let detail = [filter == .all ? from?.name : nil, [.working, .needsYou, .unread].contains(s.state) ? s.state.label : s.updatedAt.map { Fmt.relative($0) }]
            .compactMap { $0 }.joined(separator: " · ")
        let external = AutoConversationSource.external(s.origin)
        return Button { open(s.key) } label: {
            HStack(spacing: Theme.Space.m) {
                VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                    HStack(spacing: Theme.Space.xs) {
                        if let t = ConversationTitle(title) {
                            Text("\(Text(t.head).foregroundStyle(Color.secondary))\(t.topic)")
                                .font(Theme.Font.body).foregroundStyle(Color.primary).lineLimit(1)
                        } else {
                            Text(title).font(Theme.Font.body).foregroundStyle(Color.primary).lineLimit(1)
                        }
                        if let external { ExternalSourceMark(source: external) } // an extension opened it (#3758)
                    }
                    if !detail.isEmpty {
                        Text(detail).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1)
                    }
                }
                Spacer(minLength: Theme.Space.xs)
                RowStateMark(state: s.state)
            }
            .padding(.vertical, Theme.Space.xs)
            .frame(maxWidth: .infinity, alignment: .leading)
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .accessibilityLabel([title, filter == .all ? nil : external?.from, detail.isEmpty ? nil : detail].compactMap { $0 }
            .joined(separator: String(localized: ", ", comment: "Separator between the parts of a VoiceOver label")))
        .accessibilityIdentifier("auto-conversation")
        .swipeActions(edge: .trailing, allowsFullSwipe: true) {
            Button { Task { await model.archiveConversation(s.key, true) } } label: {
                Label("Archive", systemImage: Theme.Icon.archive)
            }
            .tint(Theme.accent)
        }
        .contextMenu {
            // A direction is the hub's, for every device (#4331): a long press makes a conversation one, or ends it.
            let lead = model.assistantLeaders.contains(s.sessionID)
            Button {
                Task { await model.setDirectionLead(s.sessionID, !lead) }
            } label: {
                lead ? Label("Unpin [Assistant Sidebar b7c40e]", systemImage: Theme.Icon.unpin)
                    : Label("Pin [Assistant Sidebar 6d1f2a]", systemImage: Theme.Icon.pin)
            }
            Button { Task { await model.archiveConversation(s.key, true) } } label: {
                Label("Archive", systemImage: Theme.Icon.archive)
            }
        }
    }
}
