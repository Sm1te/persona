import Foundation

/// What opened an assistant conversation the user did not start (#3264, docs/protocol/protocol.md「Origin of assistant chats」): the hub's
/// `origin`, with its name and symbol for the sidebar's Auto Chats and the list of conversations. Origins this app does
/// not know share Other.
nonisolated struct AutoConversationSource: Hashable, Identifiable, Sendable {
    var id: String
    var name: String
    var symbol: String
    /// Its brand's mark (`Theme.Brand`), drawn instead of `symbol` where there is one.
    var brand: String? = nil

    static let inbox = AutoConversationSource(id: "inbox", name: String(localized: "Inbox"), symbol: Theme.Icon.inbox)
    static let task = AutoConversationSource(id: "task", name: String(localized: "Scheduled Tasks"), symbol: Theme.Icon.schedule)
    static let larkAgent = AutoConversationSource(id: "lark_agent", name: String(localized: "Lark /agent"), symbol: Theme.Icon.feishuChats,
                                                  brand: Theme.Brand.lark)
    static let spawned = AutoConversationSource(id: "spawned", name: String(localized: "Spawned from Chats"), symbol: Theme.Icon.conversation)
    static let extensionSource = AutoConversationSource(id: "extension", name: String(localized: "Extensions"), symbol: Theme.Icon.plugins)
    static let other = AutoConversationSource(id: "other", name: String(localized: "Other"), symbol: Theme.Icon.ai)

    /// All of them, in the order the sidebar lists them.
    static let all: [AutoConversationSource] = [inbox, task, larkAgent, spawned, extensionSource, other]

    /// The source an origin is listed under; one this app does not know is Other.
    static func of(_ origin: String) -> AutoConversationSource {
        all.first { $0.id == origin && $0 != other } ?? other
    }

    /// The origins of the user's own conversations: made in the app or the web, or with /new in a channel.
    static let usersOwn: Set<String> = ["", "user", "channel"]

    /// Where a conversation an extension opened came from (#3758): Lark /agent or another extension; nil for any other
    /// (the user's, the inbox's, a task's, one another conversation started, an origin this app does not know).
    static func external(_ origin: String?) -> AutoConversationSource? {
        switch origin?.trimmingCharacters(in: .whitespaces) {
        case larkAgent.id: larkAgent
        case extensionSource.id: extensionSource
        default: nil
        }
    }

    /// 「来自 飞书 /agent」, 「来自扩展」: where an external conversation came from, under its first message and for
    /// VoiceOver on its row.
    var from: String {
        id == Self.extensionSource.id ? String(localized: "From an extension") : String(localized: "From \(name)")
    }

    /// The hub's feature for `origin` (docs/protocol/protocol.md「Origin of assistant chats」).
    static let feature = "conversation_origin"
}

/// The sidebar's conversations split into the user's own (Recent) and the ones Persona opened by itself (Auto Chats,
/// #3264), counted by source and by state so a closed Auto Chats still says one waits or works.
///
/// With a hub that says who opened its conversations (`conversation_origin`, or an `origin` on any of them), the
/// others are in Auto Chats; a scheduled task's conversation the hub calls the user's is still known the way the sidebar
/// knew it before (`AssistantTaskChats`, #2838). A hub that never says lists them as before: Recent has all but the
/// scheduled tasks' ones, and there is no Auto Chats.
nonisolated struct AutoConversations: Equatable, Sendable {
    /// Recent: the user's own, and the open one whatever opened it (it keeps its row).
    var mine: [Session] = []
    /// Auto Chats, in the order they came.
    var auto: [Session] = []
    /// Whether the hub says who opened its conversations.
    var supported = false

    init(_ sessions: [Session], tasks: AssistantTaskChats, current: SessionKey?, hubSays: Bool = false) {
        supported = hubSays || sessions.contains { $0.origin != nil }
        guard supported else {
            mine = tasks.recent(sessions, current: current)
            return
        }
        for s in sessions {
            if Self.source(of: s, tasks: tasks) == nil {
                mine.append(s)
            } else {
                auto.append(s)
                if s.key == current { mine.append(s) }
            }
        }
    }

    /// Who opened `s`: nil for the user.
    static func source(of s: Session, tasks: AssistantTaskChats) -> AutoConversationSource? {
        let o = s.origin?.trimmingCharacters(in: .whitespaces) ?? ""
        if !AutoConversationSource.usersOwn.contains(o) { return .of(o) }
        return tasks.contains(s) ? .task : nil
    }

    /// One source with its conversations.
    struct Group: Equatable, Sendable {
        var source: AutoConversationSource
        var sessions: [Session]
        var needsYou: Int { sessions.count { $0.state == .needsYou } }
        var working: Bool { sessions.contains { $0.state == .working } }
    }

    /// The sources that have conversations, in `AutoConversationSource.all`'s order.
    func bySource(tasks: AssistantTaskChats) -> [Group] {
        var groups: [AutoConversationSource: [Session]] = [:]
        for s in auto { if let src = Self.source(of: s, tasks: tasks) { groups[src, default: []].append(s) } }
        return AutoConversationSource.all.compactMap { src in groups[src].map { Group(source: src, sessions: $0) } }
    }

    /// How many of them wait for the user.
    var needsYou: Int { auto.count { $0.state == .needsYou } }
    var working: Bool { auto.contains { $0.state == .working } }
    var unread: Int { auto.count { $0.state == .unread } }
}
