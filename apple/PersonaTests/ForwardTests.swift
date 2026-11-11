import Foundation
import Testing
@testable import Persona

/// Forwarding a message (#3807, docs/protocol/protocol.md "Forwarded messages"): the block as spec/fixtures/forward
/// writes and reads it, what of a message it carries, where it may go, and the targets remembered.
struct ForwardTests {
    private static let repo = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()

    private struct Fixtures: Decodable {
        struct Block: Decodable {
            var title: String
            var url: String
            var msg: String
            var attributes: [String: String]
            var text: String
        }
        struct Expected: Decodable {
            struct Origin: Decodable { var machine: String; var session: String; var item: String? }
            var kind: String
            var title: String
            var url: String
            var msg: String
            var attributes: [String: String]
            var text: String
            var rest: String
            var origin: Origin
        }
        struct Case: Decodable {
            var name: String
            var block: Block?
            var xml: String?
            var message: String?
            var expected: Expected?
        }
        var version: Int
        var cases: [Case]
    }

    @Test func writesAndReadsTheSharedCases() throws {
        let data = try Data(contentsOf: Self.repo.appending(path: "spec/fixtures/forward/cases.json"))
        let fixtures = try JSONDecoder().decode(Fixtures.self, from: data)
        #expect(fixtures.version == 1)
        #expect(fixtures.cases.count >= 3)
        for c in fixtures.cases {
            if let b = c.block {
                let ref = ContextRef(kind: ForwardMessage.kind, title: b.title, url: b.url, text: b.text, msg: b.msg, extra: b.attributes)
                #expect(ref.xml == c.xml, "\(c.name)")
                // What it writes reads back as the same block.
                let back = ContextRef.parse(ref.xml).refs.first
                #expect(back?.extra == b.attributes && back?.url == b.url && back?.msg == b.msg, "\(c.name)")
                continue
            }
            let parsed = ContextRef.parse(c.message ?? "")
            let r = try #require(parsed.refs.first, "\(c.name)")
            let e = try #require(c.expected)
            #expect(r.kind == e.kind && r.title == e.title && r.url == e.url && r.msg == e.msg, "\(c.name)")
            #expect(r.extra == e.attributes, "\(c.name)")
            #expect(r.text == e.text && parsed.rest == e.rest, "\(c.name)")
            let o = try #require(ForwardMessage.origin(r))
            #expect(o.machine == e.origin.machine && o.session == e.origin.session && o.item == e.origin.item, "\(c.name)")
            #expect(r.isForward && r.isQuote && !r.isSession, "\(c.name): a forward is a quote card, not an @-session")
        }
    }

    private static let key = SessionKey(group: "", machineID: "macbook", sessionID: "a1f3c2d4-0000-4000-8000-000000000006")

    private static func source(_ role: ForwardMessage.Role = .agent, time: Date? = nil) -> ForwardMessage.Source {
        ForwardMessage.Source(key: key, item: "e5f0#12", role: role, author: "Claude", chat: "Network tests",
                              project: "weather › tests", machine: "MacBook Pro", time: time)
    }

    @Test func aForwardCarriesItsSourceAndALinkBack() throws {
        let t = Date(timeIntervalSince1970: 1_762_827_840)
        let ref = ForwardMessage.ref("The answer", from: Self.source(time: t))
        #expect(ref.kind == "forward" && ref.text == "The answer" && ref.msg == "e5f0#12")
        #expect(ref.extra?["role"] == "agent" && ref.extra?["author"] == "Claude" && ref.extra?["chat"] == "Network tests")
        #expect(ref.extra?["project"] == "weather › tests" && ref.extra?["machine"] == "MacBook Pro")
        #expect(ref.extra?["time"].flatMap { ForwardMessage.timeFormat.date(from: $0) } == t)
        #expect(ref.title == ForwardMessage.heading(ref) && ref.title.contains("Claude") && ref.title.contains("Network tests"))
        // The link names the session and the item, `#` escaped, and reads back.
        #expect(ref.url == "persona://session/macbook/a1f3c2d4-0000-4000-8000-000000000006?at=e5f0%2312")
        let o = try #require(ForwardMessage.origin(ref))
        #expect(o.machine == "macbook" && o.session == Self.key.sessionID && o.item == "e5f0#12")
        // The user's own message is "Me", never the chat's Agent; Persona's chats have no project or computer.
        let mine = ForwardMessage.ref("Mine", from: Self.source(.user))
        #expect(mine.extra?["role"] == "user" && mine.extra?["author"] == String(localized: "Me"))
        var persona = Self.source(.persona)
        persona.project = nil
        persona.machine = nil
        let p = ForwardMessage.ref("Hi", from: persona)
        #expect(p.extra?["project"] == nil && p.extra?["machine"] == nil)
    }

    @Test func theBodyIsWhatTheChatShows() {
        // The hub's provenance metadata and the photo note go; a quote it opened with stays, as `> ` lines.
        let quote = ContextRef.quote("Earlier words", from: "Claude", msg: "q1")
        let raw = "<persona-context kind=\"message-source\" title=\"\">\n{\"author\":\"user\"}\n</persona-context>\n"
            + quote.xml + "\n\nMy words\n<attached_images>\n/Users/me/.persona/inbox/a.jpg\n</attached_images>"
        let body = ForwardMessage.body(raw)
        #expect(body == "> \(quote.quoteHeading)\n> Earlier words\n\nMy words")
        #expect(!body.contains("message-source") && !body.contains("attached_images"))
        // A very long one is cut, and says so.
        let long = String(repeating: "x", count: ForwardMessage.limit + 500)
        let cut = ForwardMessage.body(long)
        #expect(cut.hasPrefix(String(repeating: "x", count: ForwardMessage.limit)) && cut.count < long.count + 100)
        #expect(cut != long && !cut.hasSuffix("x"))
    }

    @Test func theCardAndTheRouteNameTheComputers() {
        let now = Date(timeIntervalSince1970: 1_762_827_840)
        let ref = ForwardMessage.ref("x", from: Self.source(time: now.addingTimeInterval(-60)))
        let meta = ForwardMessage.meta(ref, now: now)
        #expect(meta?.hasPrefix("MacBook Pro · ") == true)
        // Today's time alone; another day's carries its date.
        let old = ForwardMessage.ref("x", from: Self.source(time: now.addingTimeInterval(-3 * 86_400)))
        #expect((ForwardMessage.meta(old, now: now)?.count ?? 0) > (meta?.count ?? 0))
        let across = ForwardMessage.route(from: "MacBook Pro", to: ("Codex", "Mac Studio"))
        #expect(across.contains("MacBook Pro") && across.contains("Mac Studio") && across.contains("Codex"))
        let same = ForwardMessage.route(from: "Mac Studio", to: ("Codex", "Mac Studio"))
        #expect(same.contains("Mac Studio") && !same.contains("MacBook Pro") && same.count < across.count)
        #expect(ForwardMessage.route(from: "MacBook Pro", to: nil).contains("Persona"))
    }

    @Test func aMessageIsForwardedOnceToTheSameBox() {
        let ref = ForwardMessage.ref("x", from: Self.source())
        let once = ContextRef.adding(ref, to: [])
        #expect(once?.count == 1)
        #expect(ContextRef.adding(ForwardMessage.ref("x", from: Self.source()), to: once ?? []) == nil)
        // A quote of the same message is another thing over the box.
        #expect(ContextRef.adding(.quote("x", from: "Claude", msg: "e5f0#12"), to: once ?? [])?.count == 2)
    }

    // MARK: - Targets

    private static func session(_ machine: String, _ id: String, state: String = "idle", updated: TimeInterval,
                                _ fields: String = "") -> Session {
        let json = """
        {"machine_id":"\(machine)","session_id":"\(id)","title":"\(id)","cwd":"/w/\(id)","status":"\(state)","state":"\(state)",
         "updated_at":"\(ISO8601DateFormatter().string(from: Date(timeIntervalSince1970: updated)))"\(fields)}
        """
        do { return try JSON.decoder.decode(Session.self, from: Data(json.utf8)) } catch { fatalError("bad fixture: \(error)") }
    }

    private static let all = [
        session("macbook", "here", updated: 50),
        session("macbook", "tests", updated: 100, #","agent":"codex","space_name":"weather""#),
        session("studio", "photos", state: "working", updated: 200),
        session("studio", "ended", state: "offline", updated: 300),
        session("server", "notes", updated: 400, #","assistant":true"#),
        session("server", "main", updated: 500, #","assistant":true"#),
        session("server", "health", updated: 600, #","assistant":true,"space":"health""#),
        session("nas", "away", updated: 700),
    ]
    private static let machines: [(ref: MachineRef, name: String)] = [
        (MachineRef(group: "", id: "macbook"), "MacBook Pro"), (MachineRef(group: "", id: "studio"), "Mac Studio"),
        (MachineRef(group: "", id: "nas"), "NAS"),
    ]
    private static let here = SessionKey(group: "", machineID: "macbook", sessionID: "here")
    private static let main = SessionKey(group: "", machineID: "server", sessionID: "main")

    private static func sections(recents: [String] = [], query: String = "") -> [ForwardTargets.Section] {
        let keys = recents.compactMap { id in all.first { $0.sessionID == id }?.key }
        return ForwardTargets.sections(all, from: here, recents: keys, main: main, machines: machines, query: query) { $0.id != "nas" }
    }

    @Test func theListIsRecentThenEachComputersLiveAgents() {
        let s = Self.sections()
        #expect(s.map(\.title) == ["MacBook Pro", "Mac Studio"])
        // Not this chat, not an ended Agent, not a computer that is offline, no Persona conversation (Persona has its row).
        #expect(s.flatMap(\.sessions).map(\.sessionID) == ["tests", "photos"])
        let r = Self.sections(recents: ["photos", "health", "here", "gone"])
        #expect(r.first?.kind == .recent && r.first?.sessions.map(\.sessionID) == ["photos"])
        #expect(r.dropFirst().flatMap(\.sessions).map(\.sessionID) == ["tests"], "a recent one is listed once")
    }

    @Test func searchFindsEndedAgentsAndPersonasConversations() {
        let byMachine = Self.sections(query: "mac studio").flatMap(\.sessions).map(\.sessionID)
        #expect(byMachine == ["ended", "photos"], "latest first, the ended one too: the send resumes it")
        #expect(Self.sections(query: "notes").flatMap(\.sessions).map(\.sessionID) == ["notes"])
        #expect(Self.sections(query: "codex weather").flatMap(\.sessions).map(\.sessionID) == ["tests"])
        #expect(Self.sections(query: "health").isEmpty && Self.sections(query: "main").isEmpty && Self.sections(query: "away").isEmpty)
    }

    @Test @MainActor func recentTargetsAreRememberedPerHub() throws {
        let defaults = try #require(UserDefaults(suiteName: "ForwardTests-\(UUID().uuidString)"))
        let work = ForwardMemory(group: "work", defaults: defaults)
        let a = SessionKey(group: "work", machineID: "m", sessionID: "a")
        let b = SessionKey(group: "work", machineID: "m", sessionID: "b")
        work.remember(.session(a))
        work.remember(.persona)
        work.remember(.session(b))
        work.remember(.session(a))
        work.remember(.newAgent) // not a place to go back to
        #expect(work.targets == [.session(a), .session(b), .persona])
        #expect(work.sessions == [a, b])
        #expect(ForwardMemory(group: "home", defaults: defaults).targets.isEmpty)
        for i in 0..<20 { work.remember(.session(SessionKey(group: "work", machineID: "m", sessionID: "s\(i)"))) }
        #expect(work.targets.count == ForwardMemory.limit)
        ForwardMemory.forget(group: "work", defaults: defaults)
        #expect(work.targets.isEmpty)
    }

    @Test @MainActor func forwardTakesSharesPlaceInTheBar() {
        let menu = MessageMenu()
        #expect(!MessageActionBar.forwards(menu: menu, pending: false), "a chat that does not forward keeps Share")
        menu.forward = { _ in }
        #expect(MessageActionBar.forwards(menu: menu, pending: false))
        #expect(!MessageActionBar.forwards(menu: menu, pending: true), "a message not sent yet is not forwarded")
        // Same number of actions: the bar is as wide as before.
        #expect(MessageActionBar.count(id: "u1", isUser: true, menu: menu) == 5)
        #expect(MessageActionBar.count(id: "a1", isUser: false, menu: menu) == 4)
    }
}
