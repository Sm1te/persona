import Foundation

/// One "thing" in the unified inbox (docs/assistant/inbox.md): the new messages in a Feishu chat or a mail thread, with the
/// hub's AI triage and whether it has been dealt with. Mirrors hub/inbox.go `InboxItem`.
nonisolated struct InboxItem: Codable, Identifiable, Hashable, Sendable {
    var id: String
    var source: String // lark | mail | a connector's id (github…)
    var kind: String   // dm | mention | group | bot | mail
    var account: String
    var accountName: String
    var title: String
    var sender: String?
    var avatar: String?
    var preview: String
    var count: Int
    var startedAt: Date?
    var updatedAt: Date?
    var tier: String?
    var summary: String?
    var reason: String?
    var read: Bool?
    /// Navigation counts only unread Needs you items; reading never files them.
    var isUnreadAction: Bool { read != true && !isDone && triage == .action }
    var doneAt: Date?
    var doneBy: String?
    var lark: Lark?
    var mail: Mail?
    /// A Feishu document's @me / comment: the rows about the user with their status columns (docs/assistant/inbox.md「Docs: mentions in sheets, bases, and documents」).
    var doc: Doc?
    /// New foreign mail translated by the hub as it came in, by target language (subject and preview here; the body
    /// comes with the mail).
    var translations: [String: Translated]?
    /// A Moments post where someone commented on, replied to or @ed the user (docs/assistant/inbox.md「Moments」).
    var moment: Moment?
    /// Only in GET /api/v1/inbox/{id}: the conversation as text.
    var context: String?
    /// The AI twin's work on it: judged, handled by an agent, a reply drafted (docs/assistant/inbox.md).
    var agent: Agent?
    /// A thread of one of the hub's connectors (GitHub…): drawn the same for every source (docs/plans/plan-connectors.md).
    var connector: Connector?
    /// What it can be muted by (the row's menu: Mute This Repository / Session / From); older hubs leave it out.
    var mutes: [MuteOption]?
    /// What it holds now, as the hub listed it: a bulk Done sends it back so an item that took new messages since
    /// is left open (older hubs leave it out).
    var version: String?
    /// What it grew out of (docs/assistant/inbox.md「Relationships: parents and children」, #2128): another item, a todo or an agent session; older hubs leave it out.
    var parent: InboxParent?
    /// The manager it belongs to, as the hub stamps a merged list (docs/protocol/protocol.md "Per-manager routing"); nil
    /// from a hub before managers. What is done to it goes to that manager (`APIClient.manager(_:)`).
    var manager: String?
    /// Its place in the list: each manager numbers its own items, so two managers may both have an `id`.
    var key: String { Self.key(id: id, manager: manager) }
    static func key(id: String, manager: String?) -> String { manager.map { "\($0)\u{0}\(id)" } ?? id }
    /// Inbox phase 1 (docs/assistant/inbox.md): the section the hub put an open item in (action | working | fyi | noise;
    /// nil from an older hub: the list goes by `tier`), urgent (someone waits), the manager's recap, the processing
    /// conversation, the current suggestion, the chain and related items. Older hubs leave them all out.
    var status: String?
    var urgent: Bool?
    var recap: String?
    var handling: Handling?
    var suggestion: Suggestion?
    var chain: [ChainEntry]?
    var related: [Related]?

    /// One thing an item can be muted by: a repository, a chat or a sender of its source.
    nonisolated struct MuteOption: Codable, Hashable, Sendable {
        var tag: String // repo | chat | sender
        var value: String
        var name: String

        /// The name under the menu's words, on one line: a repository without its owner (example-repo).
        var shortName: String { tag == "repo" ? String(name.split(separator: "/").last ?? Substring(name)) : name }

        /// The menu's words for it.
        var title: String {
            switch tag {
            case "repo": String(localized: "Mute This Repository")
            case "chat": String(localized: "Mute This Chat")
            default: String(localized: "Mute This Sender")
            }
        }

        /// The menu's words for setting a rule on it (docs/assistant/inbox.md "Policies").
        var policyTitle: String {
            switch tag {
            case "repo": String(localized: "Rule for This Repository…")
            case "chat": String(localized: "Rule for This Chat…")
            default: String(localized: "Rule for This Sender…")
            }
        }
    }

    nonisolated struct Connector: Codable, Hashable, Sendable {
        var thread: String
        var label: String?
        /// The thread on the web, opened in the app; `openURL` is handed to the system instead (sms:…).
        var url: String?
        var openURL: String?
        /// Where a reply goes and who sees it (「评论到 example-org/example-repo#12（仓库可见的人都能看到）」).
        var replyTo: String?
        var canReply: Bool?

        enum CodingKeys: String, CodingKey {
            case thread, label, url
            case openURL = "open_url", replyTo = "reply_to", canReply = "can_reply"
        }

        /// The thread as people write it (example-org/example-repo#12), nil when it is only an id.
        var reference: String? { thread.contains("#") ? thread : nil }
    }

    nonisolated struct Moment: Codable, Hashable, Sendable {
        var postID: String
        var notice: String?  // comment | reply | atInPost | atInComment (the newest)
        var replyTo: String? // the comment Send answers; nil: on the post
        var myPost: Bool?
        var postText: String?

        enum CodingKeys: String, CodingKey {
            case notice
            case postID = "post_id", replyTo = "reply_to", myPost = "my_post", postText = "post_text"
        }
    }

    nonisolated struct Agent: Codable, Hashable, Sendable {
        var state: String // skipped | queued | running | drafted | failed
        var task: String?
        var reason: String?
        var machineID: String?
        var sessionID: String?
        var draft: String?
        var note: String?
        var error: String?
        /// Status moves the agent proposes with its draft (document items); Send makes the ones kept.
        var changes: [DocChange]?

        enum CodingKeys: String, CodingKey {
            case state, task, reason, draft, note, error, changes
            case machineID = "machine_id", sessionID = "session_id"
        }

        /// The agent's conversation (in Chat), to see how it got to the draft.
        var sessionKey: SessionKey? {
            guard let m = machineID, let s = sessionID, !m.isEmpty, !s.isEmpty else { return nil }
            return SessionKey(machineID: m, sessionID: s)
        }
    }

    /// The agent's reply, ready to send.
    var agentDraft: String? {
        guard agent?.state == "drafted", let d = agent?.draft, !d.isEmpty else { return nil }
        return d
    }

    /// An agent is on it (queued or working).
    var agentWorking: Bool { agent?.state == "queued" || agent?.state == "running" }

    nonisolated struct Lark: Codable, Hashable, Sendable {
        var feedID: String
        var isGroup: Bool
        var isTopic: Bool?
        var startPosition: String?
        /// The chat's read position in Feishu when collected; older hubs leave it out.
        var readPosition: String?
        var mentionPosition: String?
        var atAll: Bool?
        /// The Feishu account the chat is in, to pass as `ol_account`; nil: the hub's own (older hubs: always).
        var olAccount: Int?

        enum CodingKeys: String, CodingKey {
            case feedID = "feed_id", isGroup = "is_group", isTopic = "is_topic", startPosition = "start_position"
            case readPosition = "read_position", mentionPosition = "mention_position", atAll = "at_all"
            case olAccount = "ol_account"
        }
    }

    nonisolated struct Translated: Codable, Hashable, Sendable {
        var subject: String
        var preview: String
    }

    /// The hub's translation into `target` (mail only); whether to show it is the app's setting.
    func translated(_ target: String) -> Translated? { mail != nil ? translations?[target] : nil }

    nonisolated struct Doc: Codable, Hashable, Sendable {
        var url: String
        var link: String?
        var type: String   // docx | doc | sheet | base | other
        var notice: String // mention | comment | reply
        var rows: [DocRow]?
        var reply: Reply?
        var error: String?

        nonisolated struct Reply: Codable, Hashable, Sendable {
            var kind: String // thread | record | cell | doc
            var label: String
        }

        /// Sheet / Base / Document.
        var typeName: String {
            switch type {
            case "sheet": String(localized: "Sheet [Feishu Docs Models 22d9e7]")
            case "base": String(localized: "Base")
            case "docx", "doc": String(localized: "Document")
            default: String(localized: "Cloud doc")
            }
        }

        var symbol: String {
            switch type {
            case "sheet": Theme.Icon.sheet
            case "base": Theme.Icon.base
            default: Theme.Icon.document
            }
        }
    }

    nonisolated struct DocRow: Codable, Hashable, Sendable, Identifiable {
        var ref: String
        var table: String?
        var title: String
        var fields: [DocField]?
        var status: [DocStatus]?
        var id: String { ref }
    }

    nonisolated struct DocField: Codable, Hashable, Sendable {
        var name: String
        var value: String
    }

    nonisolated struct DocStatus: Codable, Hashable, Sendable, Identifiable {
        var field: String
        var name: String
        var value: String
        var options: [DocOption]
        var id: String { field }
    }

    nonisolated struct DocOption: Codable, Hashable, Sendable {
        var name: String
        var color: String?
    }

    nonisolated struct DocChange: Codable, Hashable, Sendable {
        var ref: String
        var field: String
        var value: String
    }

    nonisolated struct Mail: Codable, Hashable, Sendable {
        var accountID: String
        var mailbox: String
        var uids: [UInt32]
        var messageID: String?
        var from: MailAddress?
        var direct: Bool?
        var thread: String?

        enum CodingKeys: String, CodingKey {
            case mailbox, uids, from, direct, thread
            case accountID = "account_id", messageID = "message_id"
        }
    }

    enum CodingKeys: String, CodingKey {
        case id, source, kind, account, title, sender, avatar, preview, count, tier, summary, reason, read, lark, mail, doc, moment, context, agent, translations, connector, mutes, version, parent
        case status, urgent, recap, handling, suggestion, chain, related, manager
        case accountName = "account_name", startedAt = "started_at", updatedAt = "updated_at", doneAt = "done_at", doneBy = "done_by"
    }

    var isDone: Bool { doneAt != nil }
    var isLark: Bool { source == "lark" }

    /// The list's sections of open items, in order: Needs You, In Progress (the AI or an Agent on it, or waiting for the
    /// other side; inbox phase 1), FYI, Collapsed. Items not triaged yet count as Needs You until the AI says otherwise.
    enum Tier: String, CaseIterable, Sendable {
        case action, working, fyi, noise
        var title: String {
            switch self {
            case .action: StateName.needsYou.text
            case .working: String(localized: "Active")
            case .fyi: String(localized: "FYI")
            case .noise: String(localized: "Collapsed [Readiness Card ee6046]")
            }
        }

        /// The section opens and closes from its title; In Progress is always open (the AI is on those, nothing to put away).
        var folds: Bool { self != .working }
    }

    /// The section: the hub's `status` when it sends one, else the triage tier as before (an older hub never says
    /// In Progress, so the list is today's).
    var triage: Tier {
        if let s = status.flatMap(Tier.init(rawValue:)) { return s }
        return tier.flatMap(Tier.init(rawValue:)).flatMap { $0 == .working ? nil : $0 } ?? .action
    }
    var isTriaged: Bool { tier?.isEmpty == false }

    /// The short tag next to the title.
    var kindLabel: String? {
        if let c = connector { return c.label }
        return switch kind {
        case "dm": String(localized: "DMs")
        // Read elsewhere: the @ is no longer unread, so the row stops flagging it (it stays for its tier).
        case "mention": read == true ? nil : lark?.atAll == true ? String(localized: "@Everyone") : String(localized: "@Me")
        case "bot": String(localized: "Bot")
        case "doc": doc?.notice == "comment" || doc?.notice == "reply" ? String(localized: "Comments") : String(localized: "@Me")
        case "moment": moment?.notice?.hasPrefix("atIn") == true ? String(localized: "@Me") : String(localized: "Moments")
        default: nil
        }
    }
}

/// A node of an item's Thread (#2128): an inbox item, a todo or an agent session (id `<machine>/<session>`). `by` says
/// who hung it there: continues | connector | user. Mirrors hub/assistant/inbox_parent.go `InboxParent`.
nonisolated struct InboxParent: Codable, Hashable, Sendable {
    var kind: String // inbox | todo | session
    var id: String
    var by: String?

    /// The session it names, nil unless a session id with both its halves.
    var sessionKey: SessionKey? {
        guard kind == "session", let slash = id.firstIndex(of: "/") else { return nil }
        let m = String(id[..<slash]), s = String(id[id.index(after: slash)...])
        return m.isEmpty || s.isEmpty ? nil : SessionKey(machineID: m, sessionID: s)
    }
}

/// GET /api/v1/inbox/tree/{kind}/{id}: the node, its ancestors (nearest first; the last may be a todo or a session,
/// with no item) and the items right under it (oldest first).
nonisolated struct InboxTree: Decodable, Sendable, Equatable {
    nonisolated struct Up: Decodable, Sendable, Equatable {
        var parent: InboxParent
        /// The item, when the ancestor is one still in the inbox.
        var item: InboxItem?

        init(parent: InboxParent, item: InboxItem? = nil) { self.parent = parent; self.item = item }

        init(from decoder: Decoder) throws {
            parent = try InboxParent(from: decoder)
            item = try decoder.container(keyedBy: Keys.self).decodeIfPresent(InboxItem.self, forKey: .item)
        }
        private enum Keys: String, CodingKey { case item }
    }

    var node: InboxParent
    var item: InboxItem?
    var ancestors: [Up]
    var children: [InboxItem]

    init(node: InboxParent, item: InboxItem? = nil, ancestors: [Up] = [], children: [InboxItem] = []) {
        self.node = node; self.item = item; self.ancestors = ancestors; self.children = children
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        node = try c.decode(InboxParent.self, forKey: .node)
        item = try c.decodeIfPresent(InboxItem.self, forKey: .item)
        ancestors = try c.decodeIfPresent([Up].self, forKey: .ancestors) ?? []
        children = try c.decodeIfPresent([InboxItem].self, forKey: .children) ?? []
    }
    private enum CodingKeys: String, CodingKey { case node, item, ancestors, children }
}

/// How triage stands (GET /api/v1/inbox `triage`, #1644): new items the hub holds back because the model could not
/// sort them yet, and why. `ok` when none wait; older hubs leave it out. Mirrors assistant/inbox_admit.go `InboxTriage`.
nonisolated struct InboxTriageStatus: Codable, Hashable, Sendable {
    /// ok | unconfigured | paused | error | timeout | behind (the most telling of the sources').
    var state: String
    /// The state for people, the hub's words (「模型没有额度」); may be empty.
    var reason: String?
    var waiting: Int
    var sources: [Source]?

    nonisolated struct Source: Codable, Hashable, Sendable {
        var source: String
        var name: String?
        var state: String
        var reason: String?
        var waiting: Int
        /// timeout | rate_limited | login_refused | failed: this round could not read the source, so its items may be old.
        var readError: String? = nil
        /// The failed read in the hub's words; older hubs may omit it.
        var readReason: String? = nil

        private enum CodingKeys: String, CodingKey {
            case source, name, state, reason, waiting
            case readError = "read_error"
            case readReason = "read_reason"
        }
    }
}

/// One answer of GET /api/v1/inbox (and of a refresh): the items, and how triage stands (nil from an older hub).
nonisolated struct InboxListing: Decodable, Sendable {
    var items: [InboxItem]
    var triage: InboxTriageStatus?
    /// Asked with `since` (docs/protocol/protocol.md "Inbox delta", #4740): what names this list for the next one; nil
    /// from a hub without deltas.
    var cursor: String?
    /// A delta: `items` are only the ones added or changed since the cursor asked with, `removed` the ones gone.
    var delta: Bool?
    var removed: [Removed]?
    /// What `since` says before the app holds a cursor: one no list has, so the hub answers the whole list and its cursor
    /// (an empty value would not be sent at all).
    static let noCursor = "0"

    nonisolated struct Removed: Decodable, Sendable, Hashable {
        var id: String
        var manager: String?
        var key: String { InboxItem.key(id: id, manager: manager) }
    }

    /// `held` with this delta applied: changed items in place, new ones in, removed ones out, then in the hub's order
    /// (each manager's items together, the managers as they came; newest first, by id among items of the same moment).
    func applied(to held: [InboxItem]) -> [InboxItem] {
        let gone = Set((removed ?? []).map(\.key))
        var changed: [String: InboxItem] = [:]
        for it in items where changed[it.key] == nil { changed[it.key] = it }
        var out = held.compactMap { it -> InboxItem? in
            guard !gone.contains(it.key) else { return nil }
            return changed.removeValue(forKey: it.key) ?? it
        }
        out += items.filter { changed.removeValue(forKey: $0.key) != nil }
        var managers: [String: Int] = [:]
        for it in out where managers[it.manager ?? ""] == nil { managers[it.manager ?? ""] = managers.count }
        return out.sorted { a, b in
            let ma = managers[a.manager ?? ""] ?? 0, mb = managers[b.manager ?? ""] ?? 0
            if ma != mb { return ma < mb }
            let ta = a.updatedAt ?? .distantPast, tb = b.updatedAt ?? .distantPast
            return ta != tb ? ta > tb : a.id < b.id
        }
    }
}

/// A page of the hub's done history (GET /api/v1/inbox/done, #2200): done items older than the list's few days, newest
/// done first; `next` is where the following page starts, present when this one is full.
nonisolated struct InboxDonePage: Decodable, Sendable {
    nonisolated struct Cursor: Codable, Sendable, Equatable {
        var before: String
        var beforeID: String
        enum CodingKeys: String, CodingKey {
            case before
            case beforeID = "before_id"
        }
    }
    var items: [InboxItem]
    var next: Cursor?
}

/// GET /api/v1/inbox/done/days' answer (#2967): the done items per calendar day in the zone asked for.
nonisolated struct InboxDoneDays: Decodable, Sendable {
    nonisolated struct Day: Decodable, Sendable {
        var day: String
        var count: Int
    }
    var days: [Day]
}

extension APIClient {
    private static func decodeInbox(_ data: Data) async throws -> InboxListing {
        try await Task.detached(priority: .userInitiated) {
            try JSON.decoder.decode(InboxListing.self, from: data)
        }.value
    }

    /// Open items and the ones done in the last few days, newest first. `connectors`: the connectors' items too
    /// (GitHub…), for a client that draws their `connector` block; without it the hub lists Feishu and mail only.
    func inbox(connectors: Bool = false) async throws -> [InboxItem] {
        try await inboxListing(connectors: connectors).items
    }

    /// The same with how triage stands.
    func inboxListing(connectors: Bool = false) async throws -> InboxListing {
        try await Self.decodeInbox(request("GET", "/api/v1/inbox", query: connectors ? ["sources": "all"] : [:]))
    }

    /// The list again, sending the tag of the one held as `If-None-Match`: nil when it is still the hub's (304), and
    /// nothing is downloaded or decoded. Without a tag it sends one no answer has, so the hub tags its answer
    /// (docs/protocol/protocol.md "Inbox validator", #4739). Old hubs answer the whole list every time, untagged.
    func inboxListing(connectors: Bool, etag: String?) async throws -> (listing: InboxListing?, etag: String?) {
        let answer = try await conditionalGet("/api/v1/inbox", query: connectors ? ["sources": "all"] : [:],
                                              etag: etag ?? Self.noInboxTag)
        guard let data = answer.data else { return (nil, etag) }
        return (try await Self.decodeInbox(data), answer.etag)
    }

    /// The list asked with `since` (docs/protocol/protocol.md "Inbox delta", #4740): a cursor of a list this app holds, or
    /// `InboxListing.noCursor` for the whole list and its cursor. A hub without deltas answers the whole list, no cursor.
    func inboxListing(connectors: Bool, since: String) async throws -> InboxListing {
        var query = ["since": since]
        if connectors { query["sources"] = "all" }
        return try await Self.decodeInbox(request("GET", "/api/v1/inbox", query: query))
    }

    /// The tag a client sends before it has one (as the web app does): no answer has it.
    nonisolated static let noInboxTag = #"W/"i1-""#

    /// A page of done items older than the list's (GET /api/v1/inbox/done, #2200), from `after` (nil: the newest).
    /// `before` without a cursor is a moment: the items done before it (a Done group's end, #2969).
    func inboxDone(after cursor: InboxDonePage.Cursor?, before moment: Date? = nil, limit: Int = 100, connectors: Bool = false) async throws -> InboxDonePage {
        var query = ["limit": String(limit)]
        if connectors { query["sources"] = "all" }
        if let cursor {
            query["before"] = cursor.before
            query["before_id"] = cursor.beforeID
        } else if let moment {
            query["before"] = moment.formatted(.iso8601)
        }
        return try await JSON.decoder.decode(InboxDonePage.self, from: request("GET", "/api/v1/inbox/done", query: query))
    }

    /// The done items per calendar day in `zone` (GET /api/v1/inbox/done/days, #2967): "YYYY-MM-DD" → count. 404 on a
    /// hub from before them.
    func inboxDoneDays(zone: String, connectors: Bool = false) async throws -> [String: Int] {
        var query = ["tz": zone]
        if connectors { query["sources"] = "all" }
        let page = try JSON.decoder.decode(InboxDoneDays.self, from: try await request("GET", "/api/v1/inbox/done/days", query: query))
        return Dictionary(page.days.map { ($0.day, $0.count) }, uniquingKeysWith: +)
    }

    /// Collects Feishu and mail now, then lists.
    func refreshInbox(connectors: Bool = false) async throws -> InboxListing {
        try await Self.decodeInbox(request("POST", "/api/v1/inbox/refresh", query: connectors ? ["sources": "all"] : [:],
                                           body: [String: String](), timeout: 60))
    }

    // Item ids are `lark:<feed>:<position>` / `mail:<account>:<thread>:<uid>`: path-safe as they are
    // (`URL.appending(path:)` would encode an escaped id a second time).

    /// One item with its conversation text.
    func inboxItem(_ id: String) async throws -> InboxItem { try await get("/api/v1/inbox/" + id) }

    /// An item's Thread (#2128): what it hangs under and what hangs under it. Older hubs answer 404.
    func inboxTree(_ kind: String, _ id: String) async throws -> InboxTree {
        try await get(Self.inboxTreePath(kind, id))
    }

    /// `/api/v1/inbox/tree/<kind>/<id>`: each segment escaped (an id may carry '#', a space…), a session's slash kept
    /// as the hub's `{id...}` takes it; ':' stays readable as in the item routes.
    nonisolated static func inboxTreePath(_ kind: String, _ id: String) -> String {
        var allowed = CharacterSet.urlPathAllowed
        allowed.remove("/")
        let segments = kind == "session" ? id.split(separator: "/", omittingEmptySubsequences: false).map(String.init) : [id]
        let escaped = segments.map { $0.addingPercentEncoding(withAllowedCharacters: allowed) ?? $0 }
        return "/api/v1/inbox/tree/" + kind + "/" + escaped.joined(separator: "/")
    }

    func setInboxDone(_ id: String, _ done: Bool) async throws {
        try await post("/api/v1/inbox/" + id + "/done", body: ["done": done])
    }

    /// Files (or brings back) many items in one request (Mark All Seen / Clear All, Mac's multi-select, Undo, and Clear All on a hub without `/bulk`): each with the version it
    /// was listed at, so the hub leaves one that took new messages since open.
    func setInboxDone(_ items: [InboxItem], _ done: Bool) async throws -> InboxBulkResult {
        try await post("/api/v1/inbox/done", body: InboxBulkBody(done: done, items: items.map { .init(id: $0.id, version: $0.version) }),
                       as: InboxBulkResult.self)
    }

    /// Clear All (`POST /api/v1/inbox/bulk`, docs/assistant/inbox.md「Clean up all」): `action` is `dismiss` (the hub takes `done` too),
    /// each item with the version it was listed at (the hub leaves one that took new messages since: `skipped`, changed).
    func bulkInbox(_ action: String, _ items: [InboxItem]) async throws -> InboxBulkReply {
        try await post("/api/v1/inbox/bulk",
                       body: InboxBulkActionBody(action: action, items: items.map { .init(id: $0.id, version: $0.version) }),
                       as: InboxBulkReply.self)
    }

    /// The reply's `undo` sent back as it came (`{action: "reopen", ids}`): brings back only what that request changed.
    func undoInboxBulk(_ undo: InboxBulkReply.Undo) async throws -> InboxBulkReply {
        try await post("/api/v1/inbox/bulk", body: undo, as: InboxBulkReply.self)
    }

    private nonisolated struct InboxMutes: Decodable, Sendable { var mutes: [InboxMute] }
    private nonisolated struct MuteBody: Encodable, Sendable { var itemID: String; var tag: String
        enum CodingKeys: String, CodingKey { case tag, itemID = "item_id" }
    }
    private nonisolated struct MuteResult: Decodable, Sendable { var mute: InboxMute; var settled: Int? }

    /// What the user muted, in every source.
    func inboxMutes() async throws -> [InboxMute] { try await get("/api/v1/inbox/mutes", as: InboxMutes.self).mutes }

    /// Mutes what the item has under `tag`: its source's open items that match are done, later ones do not come in.
    func muteInbox(_ itemID: String, tag: String) async throws -> InboxMute {
        try await post("/api/v1/inbox/mutes", body: MuteBody(itemID: itemID, tag: tag), as: MuteResult.self).mute
    }

    func unmuteInbox(_ id: String) async throws { try await delete("/api/v1/inbox/mutes/" + id) }

    private nonisolated struct InboxPolicies: Decodable, Sendable { var policies: [InboxPolicy]? }
    private nonisolated struct PolicyBody: Encodable, Sendable {
        var itemID: String?
        var tag: String?
        var tier: InboxPolicy.Tier
        var twin: InboxPolicy.Twin
        enum CodingKeys: String, CodingKey { case tag, tier, twin, itemID = "item_id" }
    }
    /// Only the count is read: a rule that setting neither part removed still comes back as an object.
    private nonisolated struct PolicyApplied: Decodable, Sendable { var applied: Int? }

    /// The user's rules, in every source (docs/assistant/inbox.md "Policies"); an older hub answers 404.
    func inboxPolicies() async throws -> [InboxPolicy] {
        try await get("/api/v1/inbox/policies", as: InboxPolicies.self).policies ?? []
    }

    /// Sets the rule of what the item has under `tag` (the hub reads the value off the item): a rule for the same target is
    /// changed, one left with neither part is removed. Answers how many open items took its tier at once.
    @discardableResult
    func setInboxPolicy(itemID: String, tag: String, tier: InboxPolicy.Tier, twin: InboxPolicy.Twin) async throws -> Int {
        try await post("/api/v1/inbox/policies", body: PolicyBody(itemID: itemID, tag: tag, tier: tier, twin: twin),
                       as: PolicyApplied.self).applied ?? 0
    }

    /// Changes a rule by id, both parts as they should be; neither removes it.
    @discardableResult
    func updateInboxPolicy(_ id: String, tier: InboxPolicy.Tier, twin: InboxPolicy.Twin) async throws -> Int {
        let data = try await request("PATCH", "/api/v1/inbox/policies/" + id, body: PolicyBody(tier: tier, twin: twin))
        return try JSON.decoder.decode(PolicyApplied.self, from: data).applied ?? 0
    }

    /// Removes a rule: items keep the tier it gave until new content is triaged again.
    func deleteInboxPolicy(_ id: String) async throws { try await delete("/api/v1/inbox/policies/" + id) }

    private struct InboxText: Codable { var text: String }
    private struct InboxReplyBody: Encodable { var text: String; var changes: [InboxItem.DocChange]?; var card: String? }
    private struct InboxHandoffBody: Encodable { var text: String; var clientID: String
        enum CodingKeys: String, CodingKey { case text, clientID = "client_id" }
    }
    private nonisolated struct InboxHandoffResponse: Decodable, Sendable {
        struct Ref: Decodable, Sendable { var machineID: String; var sessionID: String
            enum CodingKeys: String, CodingKey { case machineID = "machine_id", sessionID = "session_id" }
        }
        var item: InboxItem?
        var session: Ref

        enum CodingKeys: String, CodingKey { case item, session }

        /// An item this app can't read never turns a hand-off that went into a failure (its retry would send it again).
        init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            item = try? c.decodeIfPresent(InboxItem.self, forKey: .item)
            session = try c.decode(Ref.self, forKey: .session)
        }
    }
    private struct InboxDraftBody: Encodable { var instruction: String }

    /// A reply drafted by the hub's model; `instruction` steers it ("Agree, but later next week").
    func inboxDraft(_ id: String, instruction: String = "") async throws -> String {
        try await post("/api/v1/inbox/" + id + "/draft", body: InboxDraftBody(instruction: instruction), timeout: 90, as: InboxText.self).text
    }

    /// Sends the user's reply (in the Feishu chat, as a mail reply, or as a comment in the document, moving the rows'
    /// status in `changes` first) and files the item as done. `card`: Send on that message card, which the hub sends once
    /// and refuses for an item handled since (409 `inbox_item_done`, #4797).
    func inboxReply(_ id: String, text: String, changes: [InboxItem.DocChange] = [], card: String? = nil) async throws {
        try await post("/api/v1/inbox/" + id + "/reply", body: InboxReplyBody(text: text, changes: changes.isEmpty ? nil : changes, card: card),
                       timeout: 60)
    }

    /// Hand to AI's main chat (#1194, docs/assistant/inbox.md "Item actions"): the item's manager sends `text` after a
    /// block naming the item to its main conversation (starting it with it when there is none), once per `clientID`, and
    /// links the item to it, which may then write back to the item. Answers the item as it is now and the conversation.
    /// Older hubs and managers answer 404 / 405.
    func inboxHandoff(_ id: String, text: String, clientID: UUID) async throws -> InboxHandoffAnswer {
        let data = try await request("POST", "/api/v1/inbox/" + id + "/handoff",
                                     body: InboxHandoffBody(text: HubNaming.outbound(text, canonical: personaNaming), clientID: clientID.uuidString.lowercased()),
                                     timeout: 120)
        // The receipt is keyed on client_id, not the operation header: a repeat the hub can't answer yet comes back as
        // the bare receipt, which no answer looks like.
        try Self.checkReceipt(data, status: nil)
        let r = try JSON.decoder.decode(InboxHandoffResponse.self, from: data)
        return InboxHandoffAnswer(item: r.item, session: SessionKey(group: group, machineID: r.session.machineID, sessionID: r.session.sessionID))
    }

    /// Moves one row's status in the document; the item comes back read again.
    func inboxDocStatus(_ id: String, _ change: InboxItem.DocChange) async throws -> InboxItem {
        try await post("/api/v1/inbox/" + id + "/doc/status", body: change, timeout: 60, as: InboxItem.self)
    }
}

/// What `POST /api/v1/inbox/{id}/handoff` answers: the item (nil from a hub that leaves it out) and the conversation it
/// was handed to.
nonisolated struct InboxHandoffAnswer: Sendable {
    var item: InboxItem?
    var session: SessionKey
}

/// A mute the user set (GET /api/v1/inbox/mutes): nothing of `value` under `tag` in `source` comes into the inbox.
nonisolated struct InboxMute: Codable, Identifiable, Hashable, Sendable {
    var id: String
    var source: String
    var sourceName: String?
    var tag: String // repo | chat | sender
    var value: String
    var name: String
    var createdAt: Date?
    /// Whose mute it is (several managers: each keeps its own, and the Muted list stamps it); nil: the default's.
    var manager: String? = nil

    enum CodingKeys: String, CodingKey {
        case id, source, tag, value, name
        case sourceName = "source_name", createdAt = "created_at"
    }

    /// What kind of thing it is, for people: Repository / Session / From.
    var tagName: String {
        switch tag {
        case "repo": String(localized: "Repository")
        case "chat": String(localized: "Session")
        default: String(localized: "From")
        }
    }
}

/// A rule the user set (GET /api/v1/inbox/policies, docs/assistant/inbox.md "Policies"): items of `source` with `value`
/// under `tag` get its tier after triage, and the AI twin takes them first or never. The hub decides in code.
nonisolated struct InboxPolicy: Decodable, Identifiable, Hashable, Sendable {
    /// Where its items go; `unset` leaves it to triage.
    enum Tier: String, Codable, CaseIterable, Sendable {
        case unset = "", action, fyi, noise

        var title: String {
            switch self {
            case .unset: String(localized: "Leave to Triage [Inbox Policy]")
            case .action: String(localized: "Always Needs You [Inbox Policy]")
            case .fyi: String(localized: "FYI [Inbox Policy]")
            case .noise: String(localized: "Collapsed [Inbox Policy]")
            }
        }
    }

    /// What the AI twin does with them; `unset` as configured.
    enum Twin: String, Codable, CaseIterable, Sendable {
        case unset = "", first, never

        var title: String {
            switch self {
            case .unset: String(localized: "As Set [Inbox Policy]")
            case .first: String(localized: "AI Twin First [Inbox Policy]")
            case .never: String(localized: "Never AI Twin [Inbox Policy]")
            }
        }
    }

    var id: String
    var source: String
    var sourceName: String?
    var tag: String // sender | chat | repo
    var value: String
    var name: String
    var tier: Tier
    var twin: Twin
    var createdAt: Date?
    var updatedAt: Date?
    /// Whose rule it is (several managers: each keeps its own, and the Rules list stamps it); nil: the default's.
    var manager: String?

    enum CodingKeys: String, CodingKey {
        case id, source, tag, value, name, tier, twin
        case sourceName = "source_name", createdAt = "created_at", updatedAt = "updated_at"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        source = try c.decode(String.self, forKey: .source)
        sourceName = try c.decodeIfPresent(String.self, forKey: .sourceName).flatMap { $0.isEmpty ? nil : $0 }
        tag = try c.decode(String.self, forKey: .tag)
        value = try c.decode(String.self, forKey: .value)
        name = try c.decodeIfPresent(String.self, forKey: .name).flatMap { $0.isEmpty ? nil : $0 } ?? value
        // A part a newer hub added reads as unset rather than failing the whole list.
        tier = (try? c.decodeIfPresent(String.self, forKey: .tier)).flatMap(Tier.init(rawValue:)) ?? .unset
        twin = (try? c.decodeIfPresent(String.self, forKey: .twin)).flatMap(Twin.init(rawValue:)) ?? .unset
        createdAt = try? c.decodeIfPresent(Date.self, forKey: .createdAt)
        updatedAt = try? c.decodeIfPresent(Date.self, forKey: .updatedAt)
    }

    /// What it targets, as an item's mute option.
    var option: InboxItem.MuteOption { .init(tag: tag, value: value, name: name) }

    /// The tags a rule can name, the most specific first: a sender's rule beats its chat's (the hub's order).
    static let precedence = ["sender", "chat", "repo"]

    /// The rule that applies to an item, as the hub picks it: the same source, one of its mute options, sender before
    /// chat before repository.
    static func applying(to item: InboxItem, in policies: [InboxPolicy]) -> InboxPolicy? {
        var best: (rank: Int, policy: InboxPolicy)?
        for o in item.mutes ?? [] {
            guard let r = precedence.firstIndex(of: o.tag), r < (best?.rank ?? precedence.count) else { continue }
            if let p = policies.first(where: { $0.source == item.source && $0.tag == o.tag && $0.value == o.value }) { best = (r, p) }
        }
        return best?.policy
    }

    /// The newest first, by time (`created_at`); one without a time last.
    static func newestFirst(_ list: [InboxPolicy]) -> [InboxPolicy] {
        list.sorted { ($0.createdAt ?? .distantPast) > ($1.createdAt ?? .distantPast) }
    }

    /// What it does, for people: "Always Needs You · AI Twin First", one part when it sets one.
    var summary: String {
        [tier == .unset ? nil : tier.title, twin == .unset ? nil : twin.title].compactMap { $0 }.joined(separator: " · ")
    }

    /// What kind of thing a tag names, for people, in the web's words: Repository / Chat / Sender.
    static func kindName(_ tag: String) -> String {
        switch tag {
        case "repo": String(localized: "Repository [Inbox Rule Kind]")
        case "chat": String(localized: "Chat [Inbox Rule Kind]")
        default: String(localized: "Sender [Inbox Rule Kind]")
        }
    }

    /// The Rules list's second line: where it is from, what kind of thing, what it does.
    var detail: String {
        [sourceName ?? source, Self.kindName(tag), summary].filter { !$0.isEmpty }.joined(separator: " · ")
    }
}


nonisolated struct InboxBulkBody: Encodable, Sendable {
    struct Target: Encodable, Sendable { var id: String; var version: String? }
    var done: Bool
    var items: [Target]
}

private nonisolated struct InboxBulkActionBody: Encodable, Sendable {
    var action: String
    var items: [InboxBulkBody.Target]
}

/// What `POST /api/v1/inbox/bulk` answers: the items it changed (as the hub has them now) and their ids, the ones
/// already so, the ones it left, and the `undo` to send back for Undo (none for a reopen).
nonisolated struct InboxBulkReply: Decodable, Sendable, Equatable {
    nonisolated struct Undo: Codable, Sendable, Equatable {
        var action: String
        var ids: [String]
    }
    var action: String?
    var changed: [InboxItem]
    var ids: [String]
    var unchanged: [String]
    var skipped: [InboxBulkResult.Skip]
    var undo: Undo?

    init(action: String? = nil, changed: [InboxItem] = [], ids: [String]? = nil, unchanged: [String] = [],
         skipped: [InboxBulkResult.Skip] = [], undo: Undo? = nil) {
        self.action = action; self.changed = changed; self.ids = ids ?? changed.map(\.id)
        self.unchanged = unchanged; self.skipped = skipped; self.undo = undo
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        action = try c.decodeIfPresent(String.self, forKey: .action)
        changed = try c.decodeIfPresent([InboxItem].self, forKey: .changed) ?? []
        ids = try c.decodeIfPresent([String].self, forKey: .ids) ?? changed.map(\.id)
        unchanged = try c.decodeIfPresent([String].self, forKey: .unchanged) ?? []
        skipped = try c.decodeIfPresent([InboxBulkResult.Skip].self, forKey: .skipped) ?? []
        undo = try c.decodeIfPresent(Undo.self, forKey: .undo)
    }

    private enum CodingKeys: String, CodingKey { case action, changed, ids, unchanged, skipped, undo }
}

/// What a bulk Done / Undo did: the items it moved (as the hub has them now), the ones already so, and the ones it
/// left (`changed`: new messages since the list; `missing`: gone from the hub).
nonisolated struct InboxBulkResult: Decodable, Sendable, Equatable {
    nonisolated struct Skip: Decodable, Sendable, Equatable {
        var id: String
        var reason: String
        /// The manager that was asked (`stamped`); its answer names none.
        var manager: String? = nil
        var key: String { InboxItem.key(id: id, manager: manager) }

        enum CodingKeys: String, CodingKey { case id, reason }
    }
    var changed: [InboxItem]
    var unchanged: [String]
    var skipped: [Skip]

    static let none = InboxBulkResult(changed: [], unchanged: [], skipped: [])

    /// The answer of the manager asked, its items stamped with it as its list stamped them (`InboxItem.key`).
    func stamped(_ manager: String?) -> InboxBulkResult {
        guard let manager else { return self }
        return InboxBulkResult(changed: changed.map { var it = $0; it.manager = it.manager ?? manager; return it },
                               unchanged: unchanged,
                               skipped: skipped.map { var s = $0; s.manager = manager; return s })
    }

    mutating func add(_ other: InboxBulkResult) {
        changed += other.changed
        unchanged += other.unchanged
        skipped += other.skipped
    }
}
