import Foundation

/// Where Share › Persona sends (#1460): Persona's main conversation, another of its conversations, an agent, or a new
/// chat. The rules are here, not in the extension, so PersonaTests can hold them; the extension (`PersonaShare/`) draws
/// them and the app never uses them.
nonisolated struct ShareTarget: Identifiable, Hashable, Sendable {
    enum Kind: Hashable, Sendable { case main, conversation, agent, newChat }

    var kind: Kind
    /// The session it sends to; nil for a new chat.
    var key: SessionKey?
    var title: String
    /// The machine's name (an agent's), else empty.
    var machine: String = ""
    /// The folder an agent works in, for search.
    var folder: String = ""
    var state: AgentState = .unknown
    var date: Date? = nil

    var id: String {
        switch kind {
        case .newChat: "new"
        case .main: "main"
        case .conversation, .agent: key.map { "\($0.machineID)/\($0.sessionID)" } ?? ""
        }
    }

    static let newChat = ShareTarget(kind: .newChat, title: String(localized: "New Chat [Quick Actions ea4b29]"))
}

nonisolated enum ShareTargets {
    /// How many sent-to targets are kept, and how many of them the sheet puts first (Android's `RECENT_SHARE_*`).
    static let kept = 8
    static let first = 3
    /// The last target is where the next share starts only this long after it was used.
    static let preselectWithin: TimeInterval = 24 * 60 * 60
    /// The most of Persona's other conversations listed (newest first); search looks through these too.
    static let maxConversations = 30

    /// Where a share went and when; a nil session is the main conversation (whichever that is by then).
    struct Recent: Codable, Hashable, Sendable {
        var machine: String?
        var session: String?
        var at: Date

        var isMain: Bool { session == nil }

        func names(_ t: ShareTarget) -> Bool {
            switch t.kind {
            case .main: isMain
            case .newChat: false
            case .conversation, .agent: !isMain && t.key?.machineID == machine && t.key?.sessionID == session
            }
        }
    }

    /// What the sheet can send to, from the hub's lists: the main conversation, the others newest first, the agents
    /// that can take a message newest first.
    struct Listed: Equatable, Sendable {
        var main: ShareTarget?
        var conversations: [ShareTarget] = []
        var agents: [ShareTarget] = []

        var all: [ShareTarget] { (main.map { [$0] } ?? []) + conversations + agents }
    }

    static func listed(assistant: APIClient.AssistantState?, sessions: [Session], machines: [Machine]) -> Listed {
        let names = Dictionary(machines.map { ($0.id, $0.name) }, uniquingKeysWith: { a, _ in a })
        var out = Listed()
        if let a = assistant {
            let mainID = a.main.flatMap { $0.isEmpty ? nil : $0 }
            if let mainID {
                let s = a.conversations.first { $0.sessionID == mainID }
                out.main = ShareTarget(kind: .main, key: SessionKey(machineID: a.machineID, sessionID: mainID), title: "Persona",
                                       state: s?.state ?? .idle, date: s?.updatedAt)
            }
            out.conversations = a.conversations
                .filter { $0.sessionID != mainID && $0.archived != true && !$0.isHealth && ($0.origin ?? "user") == "user" && $0.canSend }
                .sorted { ($0.updatedAt ?? .distantPast) > ($1.updatedAt ?? .distantPast) }
                .prefix(maxConversations)
                .map { s in
                    ShareTarget(kind: .conversation, key: SessionKey(machineID: s.machineID, sessionID: s.sessionID),
                                title: s.title.isEmpty || s.title == "Claude Code" ? ShareTarget.newChat.title : s.title,
                                state: s.state, date: s.updatedAt)
                }
        }
        out.agents = sessions
            .filter { !$0.assistant && $0.canSend && $0.state != .offline && $0.startError == nil }
            .sorted { $0.sortDate > $1.sortDate }
            .map { s in
                ShareTarget(kind: .agent, key: SessionKey(machineID: s.machineID, sessionID: s.sessionID), title: s.displayTitle,
                            machine: names[s.machineID] ?? s.machineID, folder: s.folderName, state: s.state, date: s.updatedAt)
            }
        return out
    }

    /// `recent` with `target` in front, used at `now`; each target once, at most `kept`. A new chat is remembered as
    /// the conversation it started (`started`).
    static func remember(_ recent: [Recent], _ target: ShareTarget, started: SessionKey? = nil, now: Date) -> [Recent] {
        let entry: Recent
        switch target.kind {
        case .main: entry = Recent(at: now)
        case .newChat:
            guard let started else { return recent }
            entry = Recent(machine: started.machineID, session: started.sessionID, at: now)
        case .conversation, .agent:
            guard let k = target.key else { return recent }
            entry = Recent(machine: k.machineID, session: k.sessionID, at: now)
        }
        return Array(([entry] + recent.filter { $0.machine != entry.machine || $0.session != entry.session }).prefix(kept))
    }

    /// The targets shared to most recently that are still listed, latest first, at most `first`; never the main
    /// conversation (it is always on the sheet).
    static func recentFirst(_ listed: Listed, recent: [Recent]) -> [ShareTarget] {
        let others = listed.conversations + listed.agents
        var out: [ShareTarget] = []
        for r in recent where !r.isMain {
            if let t = others.first(where: r.names), !out.contains(t) { out.append(t) }
            if out.count == first { break }
        }
        return out
    }

    /// The target the sheet starts on: the last one, when it was used within `preselectWithin` and is still listed;
    /// otherwise the main conversation, or a new chat when there is none yet. Sending stays the user's tap.
    static func preselected(_ listed: Listed, recent: [Recent], now: Date) -> ShareTarget {
        let fallback = listed.main ?? .newChat
        guard let last = recent.first, (0...preselectWithin).contains(now.timeIntervalSince(last.at)) else { return fallback }
        return listed.all.first(where: last.names) ?? fallback
    }

    /// The targets whose title, machine or folder holds every word of `query` (case and width insensitive).
    static func matching(_ targets: [ShareTarget], _ query: String) -> [ShareTarget] {
        let words = query.split(whereSeparator: \.isWhitespace).map(String.init)
        guard !words.isEmpty else { return targets }
        return targets.filter { t in
            let hay = [t.title, t.machine, t.folder].joined(separator: " ")
            return words.allSatisfy { hay.range(of: $0, options: [.caseInsensitive, .diacriticInsensitive, .widthInsensitive]) != nil }
        }
    }

    /// Sends `text` and `images` to `target` as the composer would, the same `clientMsgID` on a retry; a new chat starts
    /// with them as its first message. Returns the session it went to.
    static func send(_ api: APIClient, to target: ShareTarget, text: String, images: [ImagePayload], clientMsgID: String) async throws -> SessionKey {
        switch target.kind {
        case .newChat:
            return try await api.startConversation(prompt: text, images: images, opID: UUID(uuidString: clientMsgID)).key
        case .main, .conversation, .agent:
            guard let k = target.key else { throw APIError(status: 404, message: "no session") }
            try await api.send(k, text: text, images: images, clientMsgID: clientMsgID, opID: UUID(uuidString: clientMsgID))
            return k
        }
    }
}

/// The sent-to targets, in the extension's own defaults (the app never reads them): the keys are the default group's
/// sessions, and ones no longer listed take no place.
nonisolated struct ShareTargetStore {
    static let key = "share.recentTargets"
    var defaults: UserDefaults = .standard

    func read() -> [ShareTargets.Recent] {
        guard let data = defaults.data(forKey: Self.key) else { return [] }
        return (try? JSONDecoder().decode([ShareTargets.Recent].self, from: data)) ?? []
    }

    func remember(_ target: ShareTarget, started: SessionKey? = nil, now: Date = .now) {
        let next = ShareTargets.remember(read(), target, started: started, now: now)
        if let data = try? JSONEncoder().encode(next) { defaults.set(data, forKey: Self.key) }
    }
}

nonisolated extension ShareTargets {
    /// The keychain as Share › Persona reads it: the app's items, never written (the app owns the pairing).
    static let readOnlyKeychain = KeychainAccess(read: { Keychain.read(account: $0) }, write: { _, _ in false }, delete: { _ in })

    /// A client for the default group (the app opens that one first), else the first group, on the hub of it that
    /// answers first (`HubRoute.pick`, the center when it does).
    static func client(_ kc: KeychainAccess = readOnlyKeychain, pick: ([HubRoute.Candidate]) async -> HubRoute.Candidate? = { await HubRoute.pick($0) }) async throws -> APIClient {
        guard case .registry(let registry) = HubStore.read(kc) else { throw HubStore.NotPaired() }
        let group = registry.primary("") != nil ? "" : (registry.groups.first ?? "")
        guard let c = await pick(HubRoute.candidates(registry, group: group)) else { throw HubStore.NotPaired() }
        return APIClient(config: c.config, group: group)
    }

    /// What the sheet says when loading or sending failed.
    static func sentence(_ error: Error) -> String {
        if error is HubStore.NotPaired { return String(localized: "No hub paired yet: pair one in Persona first") }
        // Cloudflare Access in front of the hub turned the request away: what the app says (needs / rejected the token).
        if let e = error as? CloudflareAccessError, let s = e.errorDescription { return s }
        if let e = error as? APIError {
            if e.status == 401 || e.status == 403 { return String(localized: "The pairing no longer works: pair again in Persona") }
            if (400..<500).contains(e.status), !e.message.isEmpty { return e.message }
        }
        return String(localized: "Can't reach the hub. Try again later.")
    }
}

/// Files in a share (#1460): uploaded as the composer's + › Files does (docs/protocol/protocol.md「Sending files」) to the
/// computer the target runs on, their paths after the text; pictures stay pictures.
nonisolated extension ShareTargets {
    /// The most files one share takes, and the largest each may be (the composer's `FileUploads.limit`).
    static let maxFiles = 10
    static let fileLimit: Int64 = 200 << 20

    /// The computer a target's files go to: the agent's own, the assistant's for Persona's conversations and a new chat.
    static func machine(for target: ShareTarget, assistantMachine: String?) -> String? {
        target.kind == .newChat ? assistantMachine : target.key?.machineID
    }

    /// `text` with the files' paths after it, space-separated, each quoted as the composer writes it
    /// (`PathInsert.quoted`) so the agent reads it as one path.
    static func message(_ text: String, paths: [String]) -> String {
        let list = paths.map { path in
            path.contains(where: { $0.isWhitespace || $0 == "'" || $0 == "\"" }) ? "'" + path.replacingOccurrences(of: "'", with: "'\\''") + "'" : path
        }.joined(separator: " ")
        return [text, list].filter { !$0.isEmpty }.joined(separator: "\n\n")
    }

    /// What the sheet says when a file did not upload, as the composer says it (`FileUploads.message`).
    static func fileSentence(_ error: Error, name: String) -> String {
        if let e = error as? APIError {
            if e.code == "too_large" { return String(localized: "“\(name)” is over 200 MB and can't be sent") }
            if e.code == "node_outdated" { return e.message }
            if e.status == 404 { return String(localized: "The hub is too old to receive files. Update the hub and try again.") }
        }
        if error is URLError || error is CloudflareAccessError { return sentence(error) }
        return String(localized: "Couldn't send “\(name)”: \(error.localizedDescription)")
    }
}
