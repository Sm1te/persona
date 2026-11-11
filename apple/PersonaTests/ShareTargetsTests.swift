import Foundation
import Testing
@testable import Persona

/// Share › Persona's targets (#1460, `ShareTargets`): what the sheet lists, which come first, which one it starts on,
/// search, and sending to an agent and to a new chat, against a hub in memory.
@MainActor struct ShareTargetsTests {
    private static let t0 = Date(timeIntervalSince1970: 1_800_000_000)

    private static func stamp(_ t: TimeInterval) -> String { ISO8601DateFormatter().string(from: t0.addingTimeInterval(t)) }

    private static func session(_ id: String, machine: String = "m1", title: String? = nil, state: String = "idle",
                                updated: TimeInterval = 0, extra: String = "") -> String {
        #"{"machine_id":"\#(machine)","session_id":"\#(id)","title":"\#(title ?? id)","cwd":"/work/\#(id)-repo","status":"idle","state":"\#(state)","updated_at":"\#(stamp(updated))"\#(extra)}"#
    }

    private static let assistantJSON = """
    {"machine_id":"hub","launcher":"claude","cwd":"/assistant","main":"main",
     "conversations":[\(session("main", machine: "hub", title: "Main", updated: 50, extra: #","assistant":true"#)),
                      \(session("c-old", machine: "hub", title: "Trip plans", updated: 10, extra: #","assistant":true,"origin":"user""#)),
                      \(session("c-new", machine: "hub", title: "Claude Code", updated: 40, extra: #","assistant":true"#)),
                      \(session("c-archived", machine: "hub", updated: 60, extra: #","assistant":true,"archived":true"#)),
                      \(session("c-inbox", machine: "hub", updated: 60, extra: #","assistant":true,"origin":"inbox""#)),
                      \(session("c-health", machine: "hub", updated: 60, extra: #","assistant":true,"space":"health""#))]}
    """

    private static let sessionsJSON = """
    {"sessions":[\(session("a-idle", title: "Fix the login bug", updated: 20)),
                 \(session("a-work", machine: "m2", title: "Release notes", state: "working", updated: 5)),
                 \(session("a-off", state: "offline", updated: 90)),
                 \(session("a-failed", updated: 90, extra: #","start_error":"no launcher""#)),
                 \(session("a-feishu", updated: 90, extra: #","source_kind":"botmux""#)),
                 \(session("c-new", machine: "hub", updated: 40, extra: #","assistant":true"#))]}
    """

    private static let machinesJSON = #"{"machines":[{"id":"m1","name":"Studio","kind":"ssh","online":true},{"id":"m2","name":"Laptop","kind":"ssh","online":true}]}"#

    private func listed() throws -> ShareTargets.Listed {
        let a = try JSON.decoder.decode(APIClient.AssistantState.self, from: Data(Self.assistantJSON.utf8))
        let s = try JSON.decoder.decode(APIClient.SessionsResponse.self, from: Data(Self.sessionsJSON.utf8)).sessions
        let m = try JSON.decoder.decode(APIClient.MachinesResponse.self, from: Data(Self.machinesJSON.utf8)).machines
        return ShareTargets.listed(assistant: a, sessions: s, machines: m)
    }

    @Test func listsTheMainConversationTheUsersOwnConversationsAndTheAgentsThatCanTakeAMessage() throws {
        let l = try listed()
        #expect(l.main?.kind == .main)
        #expect(l.main?.key == SessionKey(machineID: "hub", sessionID: "main"))
        // Newest first; archived, the Inbox's, Health's and the main one are not listed; an untitled one is a New Chat.
        #expect(l.conversations.map(\.id) == ["hub/c-new", "hub/c-old"])
        #expect(l.conversations.first?.title == ShareTarget.newChat.title)
        // Agents newest first (a working one by when it started working); not offline, not failed to start, not one
        // that takes no messages, not an assistant conversation.
        #expect(l.agents.map(\.id) == ["m1/a-idle", "m2/a-work"])
        #expect(l.agents.map(\.machine) == ["Studio", "Laptop"])
        #expect(l.agents.first?.folder == "a-idle-repo")
    }

    @Test func aHubWithoutAMainConversationStartsOnANewChat() throws {
        var l = try listed()
        l.main = nil
        #expect(ShareTargets.preselected(l, recent: [], now: Self.t0).kind == .newChat)
    }

    @Test func recentTargetsComeFirstAtMostThreeLatestFirstAndOnlyWhileListed() throws {
        let l = try listed()
        var recent: [ShareTargets.Recent] = []
        let agent = try #require(l.agents.first { $0.id == "m2/a-work" })
        let conversation = try #require(l.conversations.first { $0.id == "hub/c-old" })
        let other = try #require(l.agents.first { $0.id == "m1/a-idle" })
        recent = ShareTargets.remember(recent, other, now: Self.t0)
        recent = ShareTargets.remember(recent, ShareTarget(kind: .agent, key: SessionKey(machineID: "m1", sessionID: "gone"), title: "gone"), now: Self.t0.addingTimeInterval(1))
        recent = ShareTargets.remember(recent, try #require(l.main), now: Self.t0.addingTimeInterval(2))
        recent = ShareTargets.remember(recent, conversation, now: Self.t0.addingTimeInterval(3))
        recent = ShareTargets.remember(recent, agent, now: Self.t0.addingTimeInterval(4))
        // The main conversation is always on the sheet and a gone one takes no place.
        #expect(ShareTargets.recentFirst(l, recent: recent).map(\.id) == ["m2/a-work", "hub/c-old", "m1/a-idle"])
        // Used again: it moves to the front, once.
        recent = ShareTargets.remember(recent, other, now: Self.t0.addingTimeInterval(5))
        #expect(recent.filter { $0.session == "a-idle" }.count == 1)
        #expect(ShareTargets.recentFirst(l, recent: recent).map(\.id) == ["m1/a-idle", "m2/a-work", "hub/c-old"])
    }

    @Test func keepsAtMostEightAndANewChatAsTheConversationItStarted() {
        var recent: [ShareTargets.Recent] = []
        for i in 0..<10 {
            recent = ShareTargets.remember(recent, ShareTarget(kind: .agent, key: SessionKey(machineID: "m", sessionID: "s\(i)"), title: ""),
                                           now: Self.t0.addingTimeInterval(TimeInterval(i)))
        }
        #expect(recent.count == ShareTargets.kept)
        #expect(recent.first?.session == "s9")
        let started = SessionKey(machineID: "hub", sessionID: "fresh")
        recent = ShareTargets.remember(recent, .newChat, started: started, now: Self.t0.addingTimeInterval(20))
        #expect(recent.first == ShareTargets.Recent(machine: "hub", session: "fresh", at: Self.t0.addingTimeInterval(20)))
        #expect(ShareTargets.remember(recent, .newChat, now: Self.t0) == recent, "a new chat that did not start is not remembered")
    }

    @Test func startsOnTheLastTargetWithinADayWhileItIsListedElseTheMainConversation() throws {
        let l = try listed()
        let agent = try #require(l.agents.first)
        let recent = ShareTargets.remember([], agent, now: Self.t0)
        #expect(ShareTargets.preselected(l, recent: recent, now: Self.t0.addingTimeInterval(23 * 3600)).id == agent.id)
        #expect(ShareTargets.preselected(l, recent: recent, now: Self.t0.addingTimeInterval(25 * 3600)).kind == .main)
        let gone = ShareTargets.remember([], ShareTarget(kind: .agent, key: SessionKey(machineID: "m1", sessionID: "a-off"), title: ""), now: Self.t0)
        #expect(ShareTargets.preselected(l, recent: gone, now: Self.t0).kind == .main, "an agent no longer running is not picked")
        #expect(ShareTargets.preselected(l, recent: [], now: Self.t0).kind == .main)
    }

    @Test func searchMatchesEveryWordInTheTitleMachineOrFolder() throws {
        let l = try listed()
        #expect(ShareTargets.matching(l.agents, "login").map(\.id) == ["m1/a-idle"])
        #expect(ShareTargets.matching(l.agents, "laptop").map(\.id) == ["m2/a-work"])
        #expect(ShareTargets.matching(l.agents, "studio REPO").map(\.id) == ["m1/a-idle"])
        #expect(ShareTargets.matching(l.agents, "studio notes").isEmpty)
        #expect(ShareTargets.matching(l.agents, "  ").count == l.agents.count)
    }

    @Test func sendsToAnAgentAsTheComposerDoes() async throws {
        let hub = FakeHub()
        var body: [String: Any] = [:]
        hub.on("POST", "/api/v1/sessions/m2/a-work/messages") { req in
            body = try JSONSerialization.jsonObject(with: req.body ?? Data()) as? [String: Any] ?? [:]
            return Data("{}".utf8)
        }
        let l = try listed()
        let agent = try #require(l.agents.first { $0.id == "m2/a-work" })
        let id = UUID().uuidString
        let went = try await ShareTargets.send(hub.client, to: agent, text: "look at this", images: [ImagePayload(mime: "image/jpeg", data: "AAAA")], clientMsgID: id)
        #expect(went == SessionKey(machineID: "m2", sessionID: "a-work"))
        #expect(body["text"] as? String == "look at this")
        #expect(body["client_msg_id"] as? String == id)
        #expect((body["images"] as? [[String: Any]])?.first?["mime"] as? String == "image/jpeg")
    }

    @Test func aNewChatStartsWithTheSharedItemsAsItsFirstMessage() async throws {
        let hub = FakeHub()
        var body: [String: Any] = [:]
        hub.on("POST", "/api/v1/assistant/conversations") { req in
            body = try JSONSerialization.jsonObject(with: req.body ?? Data()) as? [String: Any] ?? [:]
            return Data(#"{"session":\#(Self.session("fresh", machine: "hub", extra: #","assistant":true"#))}"#.utf8)
        }
        let went = try await ShareTargets.send(hub.client, to: .newChat, text: "https://example.com", images: [], clientMsgID: UUID().uuidString)
        #expect(went == SessionKey(machineID: "hub", sessionID: "fresh"))
        #expect(body["prompt"] as? String == "https://example.com")
        #expect(body["main"] == nil, "a new chat does not replace the main conversation")
        #expect(hub.sent("POST", "/api/v1/assistant/conversations").count == 1)
    }

    @Test func readsThePairingWithoutEverWritingIt() async throws {
        let config = ServerConfig(url: URL(string: "https://hub.invalid")!, token: "t")
        let registry = HubRegistry(hubs: [PairedHub(config: config, group: "")])
        let data = try JSONEncoder().encode(registry)
        let writes = Counter()
        let kc = KeychainAccess(read: { $0 == "hubs" ? .found(data) : .missing }, write: { _, _ in writes.add(); return false }, delete: { _ in writes.add() })
        let api = try await ShareTargets.client(kc) { $0.first }
        #expect(api.config.url == config.url)
        #expect(api.group == "")
        await #expect(throws: HubStore.NotPaired.self) {
            _ = try await ShareTargets.client(KeychainAccess(read: { _ in .missing }, write: { _, _ in writes.add(); return false }, delete: { _ in writes.add() })) { $0.first }
        }
        #expect(writes.count == 0)
        // The extension's own reads never write: the real one is read-only.
        #expect(ShareTargets.readOnlyKeychain.write(Data(), "hubs") == false)
    }

    @Test func failuresSayWhatToDo() {
        #expect(ShareTargets.sentence(HubStore.NotPaired()) == String(localized: "No hub paired yet: pair one in Persona first"))
        #expect(ShareTargets.sentence(APIError(status: 401, message: "no")) == String(localized: "The pairing no longer works: pair again in Persona"))
        #expect(ShareTargets.sentence(APIError(status: 409, message: "A dialog is open")) == "A dialog is open")
        #expect(ShareTargets.sentence(URLError(.timedOut)) == String(localized: "Can't reach the hub. Try again later."))
        // Cloudflare Access in front of the hub: the app's wording, not "can't reach" (#3504).
        for sent in [false, true] {
            let access = CloudflareAccessError(tokenSent: sent)
            #expect(ShareTargets.sentence(access) == access.errorDescription)
            #expect(ShareTargets.fileSentence(access, name: "a.pdf") == access.errorDescription)
        }
    }

    @Test func filesGoToTheTargetsComputerAndTheirPathsFollowTheText() throws {
        let l = try listed()
        #expect(ShareTargets.machine(for: try #require(l.agents.first { $0.id == "m2/a-work" }), assistantMachine: "hub") == "m2")
        #expect(ShareTargets.machine(for: try #require(l.main), assistantMachine: "hub") == "hub")
        #expect(ShareTargets.machine(for: .newChat, assistantMachine: "hub") == "hub")
        #expect(ShareTargets.machine(for: .newChat, assistantMachine: nil) == nil)
        #expect(ShareTargets.message("Read these", paths: ["/tmp/a.pdf", "/tmp/My notes.txt", "/tmp/it's.zip"])
                == "Read these\n\n/tmp/a.pdf '/tmp/My notes.txt' '/tmp/it'\\''s.zip'")
        #expect(ShareTargets.message("", paths: ["/tmp/a.pdf"]) == "/tmp/a.pdf")
        #expect(ShareTargets.message("Only words", paths: []) == "Only words")
    }

    @Test func aFileThatDidNotUploadSaysWhyAsTheComposerDoes() {
        let tooLarge = APIError(status: 413, message: "", fields: ["code": "too_large"])
        #expect(ShareTargets.fileSentence(tooLarge, name: "big.mov") == FileUploads.message(tooLarge, name: "big.mov"))
        #expect(ShareTargets.fileSentence(tooLarge, name: "big.mov").contains("big.mov"))
        let oldHub = APIError(status: 404, message: "not found")
        #expect(ShareTargets.fileSentence(oldHub, name: "a.pdf") == FileUploads.message(oldHub, name: "a.pdf"))
        let outdated = APIError(status: 409, message: "The node is too old", fields: ["code": "node_outdated"])
        #expect(ShareTargets.fileSentence(outdated, name: "a.pdf") == "The node is too old")
        #expect(ShareTargets.fileSentence(URLError(.timedOut), name: "a.pdf") == String(localized: "Can't reach the hub. Try again later."))
    }

    @Test func theStoreKeepsWhatWasSent() throws {
        let defaults = try #require(UserDefaults(suiteName: "ShareTargetsTests-\(UUID().uuidString)"))
        let store = ShareTargetStore(defaults: defaults)
        #expect(store.read().isEmpty)
        store.remember(ShareTarget(kind: .agent, key: SessionKey(machineID: "m", sessionID: "s"), title: "x"), now: Self.t0)
        #expect(store.read() == [ShareTargets.Recent(machine: "m", session: "s", at: Self.t0)])
        defaults.set(Data("junk".utf8), forKey: ShareTargetStore.key)
        #expect(store.read().isEmpty, "an unreadable list is only a lost order")
    }

    private final class Counter: @unchecked Sendable {
        private let lock = NSLock()
        private var n = 0
        var count: Int { lock.withLock { n } }
        func add() { lock.withLock { n += 1 } }
    }
}
