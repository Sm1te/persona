import Foundation
import SwiftUI

// Forwarding a message (#3807; docs/protocol/protocol.md "Forwarded messages", docs/apple/ios.md "Forwarding a message"):
// long-press › Forward › a target, and that chat opens with the message over its box as a card, as Telegram does. What
// goes out is the user's own message: a `<persona-context kind="forward">` block (the original and where it came from)
// before the note typed under it, sent through the chat's usual send. Nothing is written back to the original chat.

/// The forward block: what it carries and how it is written and read.
nonisolated enum ForwardMessage {
    static let kind = "forward"
    /// The most of the original a forward carries; a longer one is cut with a note saying so.
    static let limit = 20_000

    /// Whose message it was: the user's own, Persona's, an Agent's, or another source's (a relay, a patrol).
    enum Role: String, Sendable {
        case user, persona, agent, other
    }

    /// Where the forwarded message is and who wrote it, as the chat it was pressed in knows them.
    struct Source: Sendable, Equatable {
        var key: SessionKey
        var item: String
        var role: Role
        /// Who wrote it, by name: "Persona", the Agent's ("Claude"), a source's ("@Patrol"); unused for the user's own.
        var author: String
        /// The chat's title.
        var chat: String
        /// The Agent's project ("weather › tests"); nil for Persona's chats.
        var project: String?
        /// The computer's name; nil for Persona's chats.
        var machine: String?
        var time: Date?
    }

    /// The block for a message whose text, as the transcript has it, is `raw`.
    static func ref(_ raw: String, from s: Source) -> ContextRef {
        var extra = ["role": s.role.rawValue, "author": author(s), "chat": s.chat]
        if let p = s.project, !p.isEmpty { extra["project"] = p }
        if let m = s.machine, !m.isEmpty { extra["machine"] = m }
        if let t = s.time { extra["time"] = timeFormat.string(from: t) }
        var ref = ContextRef(kind: kind, title: "", url: url(s.key, item: s.item), text: body(raw, machine: s.machine),
                             msg: s.item, extra: extra)
        ref.title = heading(ref)
        return ref
    }

    /// The name a block carries for its author: the user's own message is "Me" in the sender's language, as quotes say.
    private static func author(_ s: Source) -> String {
        s.role == .user ? String(localized: "Me") : s.author
    }

    /// What of `raw` a forward carries: the words as the chat shows them, without the hub's metadata and transport blocks;
    /// the quotes and forwards it opened with as `> ` lines led by their heading; files on its computer by name; cut at
    /// `limit`.
    static func body(_ raw: String, machine: String? = nil) -> String {
        let parsed = ContextRef.parse(MessageSource.strippingMetadata(raw))
        var parts: [String] = []
        for r in parsed.refs where r.isQuote {
            let lines = [r.quoteHeading] + r.text.components(separatedBy: "\n")
            parts.append(lines.map { "> " + $0 }.joined(separator: "\n"))
        }
        let attached = AttachedFile.parse(parsed.rest)
        let words = UserBubble.display(attached.rest)
        if !words.isEmpty { parts.append(words) }
        for f in attached.files {
            parts.append(machine.map { String(localized: "(File on \($0): \(f.name))") } ?? String(localized: "(File: \(f.name))"))
        }
        let text = parts.joined(separator: "\n\n")
        guard text.count > limit else { return text }
        return String(text.prefix(limit)) + "\n\n" + String(localized: "(The original is longer and was cut here.)")
    }

    /// The original message's address: the session's link (as an @-quoted session's) with the item.
    static func url(_ key: SessionKey, item: String) -> String {
        var c = URLComponents()
        c.scheme = "persona"
        c.host = "session"
        c.path = "/\(key.machineID)/\(key.sessionID)"
        if !item.isEmpty { c.queryItems = [URLQueryItem(name: "at", value: item)] }
        // `#` in a Claude item id is a fragment to URLComponents' query encoder: write it escaped.
        return (c.string ?? "").replacingOccurrences(of: "#", with: "%23")
    }

    /// The session and item a forward's `url` names; nil when it names none.
    static func origin(_ ref: ContextRef) -> (machine: String, session: String, item: String?)? {
        guard let c = URLComponents(string: ref.url), HubNaming.accepts(scheme: c.scheme), c.host == "session" else { return nil }
        let parts = c.path.split(separator: "/").map(String.init)
        guard parts.count == 2 else { return nil }
        let item = c.queryItems?.first { $0.name == "at" }?.value.flatMap { $0.isEmpty ? nil : $0 }
        return (parts[0], parts[1], item ?? (ref.msg.isEmpty ? nil : ref.msg))
    }

    /// `time` as written: ISO 8601 with the sender's offset. ISO8601DateFormatter is safe to share between threads.
    nonisolated(unsafe) static let timeFormat: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime]
        f.timeZone = .current
        return f
    }()

    /// The card's first line: "Forwarded from Claude · Network tests".
    static func heading(_ r: ContextRef) -> String {
        let who = shownAuthor(r)
        let chat = r.extra?["chat"] ?? ""
        return chat.isEmpty ? String(localized: "Forwarded from \(who)") : String(localized: "Forwarded from \(who) · \(chat)")
    }

    /// The author as this device names it: the user's own message is "Me" in this device's language.
    static func shownAuthor(_ r: ContextRef) -> String {
        if r.extra?["role"] == Role.user.rawValue { return String(localized: "Me") }
        let a = r.extra?["author"] ?? ""
        return a.isEmpty ? String(localized: "Messages [Quote Views 4da199]") : a
    }

    /// The card's second line: the computer and when ("MacBook Pro · 10:24"); nil when it knows neither.
    static func meta(_ r: ContextRef, now: Date = .now) -> String? {
        let when = r.extra?["time"].flatMap { timeFormat.date(from: $0) }.map { shortTime($0, now: now) }
        let parts = [r.extra?["machine"], when].compactMap { $0 }.filter { !$0.isEmpty }
        return parts.isEmpty ? nil : parts.joined(separator: " · ")
    }

    /// A time of today as the time alone, another day's with its date.
    static func shortTime(_ d: Date, now: Date = .now) -> String {
        Calendar.current.isDate(d, inSameDayAs: now)
            ? d.formatted(date: .omitted, time: .shortened)
            : d.formatted(.dateTime.month(.abbreviated).day().hour().minute())
    }

    /// The composer's line under the card: which computer it comes from and where it goes. `to`: the target's Agent
    /// ("Codex") and computer, or nil for Persona.
    static func route(from: String?, to: (agent: String, machine: String?)?) -> String {
        let from = from.flatMap { $0.isEmpty ? nil : $0 }
        guard let to else {
            return from.map { String(localized: "From \($0) · to Persona") } ?? String(localized: "To Persona")
        }
        guard let machine = to.machine, !machine.isEmpty else { return String(localized: "To \(to.agent) [Forward Route]") }
        if let from, from != machine { return String(localized: "From \(from) · to \(to.agent) on \(machine)") }
        return String(localized: "To \(to.agent) on \(machine)")
    }
}

/// Where a forward can go.
nonisolated enum ForwardTarget: Hashable, Sendable {
    /// Persona's main conversation (a new one when there is none yet).
    case persona
    /// A new Agent: New Chat with the forward as its first message's context.
    case newAgent
    /// An Agent's session or another Persona conversation.
    case session(SessionKey)

    /// How the recent targets remember it.
    var stored: String? {
        switch self {
        case .persona: "persona"
        case .newAgent: nil
        case .session(let key): "session:" + key.path
        }
    }

    init?(stored: String) {
        if stored == "persona" {
            self = .persona
        } else if stored.hasPrefix("session:"), let key = SessionKey(path: String(stored.dropFirst("session:".count))) {
            self = .session(key)
        } else {
            return nil
        }
    }
}

/// The picker's lists (`ForwardSheet`): which sessions a forward may go to, the recent ones first, the rest by computer,
/// and what a search finds. Built from the sessions the app already has; nothing is fetched.
nonisolated enum ForwardTargets {
    /// Recent targets shown at most.
    static let recentLimit = 5

    struct Section: Identifiable, Equatable {
        enum Kind: Equatable { case recent, machine, results }
        var id: String
        var kind: Kind
        var title: String
        var sessions: [Session]
    }

    /// Whether `s` can take a forward from the chat `from`: another chat on the same hub that takes messages; an Agent
    /// on an online computer, live or (`ended`) one the send resumes; never Health's conversations.
    static func takes(_ s: Session, from: SessionKey, ended: Bool, online: (MachineRef) -> Bool) -> Bool {
        guard s.key != from, s.key.group == from.group, s.canSend, !s.isHealth, s.archived != true else { return false }
        if s.assistant { return true }
        return online(s.machineRef) && (ended || s.state.isLive)
    }

    /// The second line of a session's row: who answers there and its project, and its computer where rows mix them.
    static func detail(_ s: Session, machine: String?) -> String {
        if s.assistant { return "Persona" }
        let project = s.spaceName.flatMap { $0.isEmpty ? nil : $0 } ?? s.folderName
        return [s.agentLabel, project, machine].compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: " · ")
    }

    /// The sections for `query`: with none, the recent targets, then each computer's live Agents (latest first);
    /// with one, every match, ended Agents and Persona conversations included.
    static func sections(_ all: [Session], from: SessionKey, recents: [SessionKey], main: SessionKey?,
                         machines: [(ref: MachineRef, name: String)], query: String,
                         online: (MachineRef) -> Bool) -> [Section] {
        let latest: (Session, Session) -> Bool = { ($0.updatedAt ?? .distantPast) > ($1.updatedAt ?? .distantPast) }
        let name: (MachineRef) -> String = { ref in machines.first { $0.ref == ref }?.name ?? ref.id }
        let terms = query.lowercased().split(whereSeparator: \.isWhitespace).map(String.init)
        if !terms.isEmpty {
            let found = all.filter { s in
                guard s.key != main, takes(s, from: from, ended: true, online: online) else { return false }
                let hay = [s.displayTitle, detail(s, machine: s.assistant ? nil : name(s.machineRef))].joined(separator: " ").lowercased()
                return terms.allSatisfy { hay.contains($0) }
            }
            return found.isEmpty ? [] : [Section(id: "results", kind: .results, title: "", sessions: found.sorted(by: latest))]
        }
        var out: [Section] = []
        let byKey = Dictionary(all.map { ($0.key, $0) }, uniquingKeysWith: { a, _ in a })
        let recent = recents.compactMap { byKey[$0] }.filter { $0.key != main && takes($0, from: from, ended: false, online: online) }
            .prefix(recentLimit)
        if !recent.isEmpty { out.append(Section(id: "recent", kind: .recent, title: "", sessions: Array(recent))) }
        let shown = Set(recent.map(\.key))
        let agents = all.filter { !$0.assistant && !shown.contains($0.key) && takes($0, from: from, ended: false, online: online) }
        for m in machines {
            let rows = agents.filter { $0.machineRef == m.ref }.sorted(by: latest)
            if !rows.isEmpty { out.append(Section(id: "machine:" + m.ref.path, kind: .machine, title: m.name, sessions: rows)) }
        }
        return out
    }
}

/// The targets forwards went to lately, latest first (`ForwardSheet`'s Recent): local to the device, ids only, per hub
/// group, never synced, gone with the group (`forget`), as Hand to AI's memory (InboxHandOffMemory).
struct ForwardMemory {
    static let limit = 8
    static let base = "forward.recentTargets"

    var group: String
    var defaults: UserDefaults = .standard

    private var name: String { PinsStore.name(Self.base, group) }

    var targets: [ForwardTarget] {
        (defaults.stringArray(forKey: name) ?? []).compactMap(ForwardTarget.init(stored:))
    }

    var sessions: [SessionKey] {
        targets.compactMap { target -> SessionKey? in
            if case .session(let k) = target { return k }
            return nil
        }
    }

    func remember(_ target: ForwardTarget) {
        guard let stored = target.stored else { return }
        let list = [stored] + (defaults.stringArray(forKey: name) ?? []).filter { $0 != stored }
        defaults.set(Array(list.prefix(Self.limit)), forKey: name)
    }

    static func forget(group: String, defaults: UserDefaults = .standard) {
        defaults.removeObject(forKey: PinsStore.name(base, group))
    }
}

extension AppModel {
    /// Puts the forward over `target`'s box (with the pictures it carries) and opens that chat, focused, ready for a
    /// note. Persona with no main conversation yet: the new conversation's box. A new Agent is New Chat's, opened by
    /// the chat (`ForwardSheet`).
    func forward(_ ref: ContextRef, images: [UIImage], to target: ForwardTarget, group: String) {
        ForwardMemory(group: group).remember(target)
        switch target {
        case .newAgent:
            return
        case .persona:
            if let main = assistantMain, main.group == group {
                place(ref, images: images, on: main, route: ForwardMessage.route(from: ref.extra?["machine"], to: nil))
                openAssistantConversation(main) // `open` takes a conversation it has not listed for an Agent's
            } else {
                var r = ref
                r.route = ForwardMessage.route(from: ref.extra?["machine"], to: nil)
                let draft = starterDraft("")
                draft.quotes = (ContextRef.adding(r, to: draft.quotes) ?? draft.quotes)
                for img in images.prefix(MessagePhotos.limit) {
                    if let p = MessagePhotos.payload(img) { draft.held.images.append(p) }
                }
                selectedTab = .assistant
                #if os(iOS)
                if let shell = tabRouter as? ShellState {
                    shell.show(.newConversation)
                    shell.drawer.close()
                } else {
                    assistantCurrent = nil
                }
                #else
                assistantCurrent = nil
                #endif
            }
        case .session(let key):
            let s = session(key)
            let to: (agent: String, machine: String?)? = s?.assistant == true ? nil
                : (s?.agentLabel ?? "Claude", machine(key.machineRef)?.name)
            place(ref, images: images, on: key, route: ForwardMessage.route(from: ref.extra?["machine"], to: to))
            if s?.assistant == true { openAssistantConversation(key) } else { open(key) }
        }
    }

    private func place(_ ref: ContextRef, images: [UIImage], on key: SessionKey, route: String) {
        var r = ref
        r.route = route
        let now = composerContexts[key] ?? []
        composerContexts[key] = ContextRef.adding(r, to: now) ?? now
        if !images.isEmpty { composerInbox[key, default: []].append(contentsOf: images.prefix(MessagePhotos.limit)) }
        composerFocus = key
    }
}

/// A pressed message on its way to the picker (`MessageMenu.forwarding`): where it is and who wrote it, its text as the
/// chat holds it, how to read the whole of it when the chat holds it cut, its pictures, and the same message as a share
/// card (Share as Image, at the top of the picker).
struct ForwardDraft: Identifiable {
    let id = UUID()
    var source: ForwardMessage.Source
    var raw: String
    /// Reads the whole text when the chat holds it cut (`Item.fullSize`); nil: `raw` is all of it.
    var full: (() async throws -> String)?
    /// The pictures sent with it (your own message's), as paths on its computer.
    var images: [String] = []
    var loadImage: (String) async -> UIImage? = { _ in nil }
    var share: ShareMessage

    /// The forward as the chat has it now: the picker's preview (the whole text is read when it goes).
    var preview: ContextRef { ForwardMessage.ref(raw, from: source) }

    /// The forward with the whole text and its pictures, as it goes.
    func complete() async throws -> (ref: ContextRef, images: [UIImage]) {
        let text = try await full?() ?? raw
        var pictures: [UIImage] = []
        for path in images.prefix(MessagePhotos.limit) {
            if let img = await loadImage(path) { pictures.append(img) }
        }
        return (ForwardMessage.ref(text, from: source), pictures)
    }
}

/// What the picker leaves for the chat to do once it has gone: the chat opens the next sheet or the target.
enum ForwardOutcome {
    case share(ShareMessage)
    case newAgent(ContextRef, [UIImage])
    case target(ContextRef, [UIImage], ForwardTarget)
}

/// A forward on its way to a new Agent (New Chat with it over the first message).
struct ForwardNewAgent: Identifiable {
    let id = UUID()
    var ref: ContextRef
    var images: [UIImage]
}
