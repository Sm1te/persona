import Foundation
import Testing
@testable import Persona

/// The shared chat sync scenarios (spec/fixtures/sync, #1073), replayed against the real AppModel (#1112): the case's
/// frames reach it through the demo config's socket, its requests through the demo config's transport, and after every
/// step its chat page is projected onto the README's view and checked against the step's `expect` and the three rules
/// (never missing, never twice, no revived cards); at the end, against `end`.
@MainActor
@Suite(.serialized)
struct SyncScenarioTests {
    nonisolated static let dir = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()
        .deletingLastPathComponent().deletingLastPathComponent().appending(path: "spec/fixtures/sync")

    nonisolated static let cases: [String: JSONValue] = {
        var out: [String: JSONValue] = [:]
        let files = (try? FileManager.default.contentsOfDirectory(at: dir, includingPropertiesForKeys: nil)) ?? []
        for f in files where f.pathExtension == "json" {
            guard let data = try? Data(contentsOf: f), let doc = try? JSONDecoder().decode(JSONValue.self, from: data) else { continue }
            for c in doc["cases"]?.array ?? [] { if let n = c["name"]?.string { out[n] = c } }
        }
        return out
    }()

    nonisolated static let names = cases.keys.sorted()

    /// Cases this harness cannot play yet, and cases that fail on a bug filed for them: the name and why. A case leaves
    /// the list when it passes (a known issue that no longer happens fails the test).
    nonisolated static let known: [String: String] = [
        "start_answer_cut_then_receipt": "#1112: a new chat starts from the new-conversation page, which the harness does not drive",
        "start_refused_then_resent_new_op": "#1112: a new chat starts from the new-conversation page, which the harness does not drive",
    ]

    @Test func everyFileHasCases() {
        #expect(Self.cases.count >= 24, "spec/fixtures/sync: \(Self.dir.path)")
    }

    @Test(arguments: [false, true])
    func cardAnswerWaitsForInitialSnapshot(cached: Bool) async throws {
        let c = try #require(Self.cases["card_answered_on_other_device"])
        try await SyncRun(c, initialInteractionsDelay: .seconds(1), cachedInitialThrough: cached ? 3 : nil).play()
    }

    @Test(arguments: ["restart_not_resumable_rereads_cards", "sent_on_other_device_once"], [1, 3])
    func initialSubscriptionCatchesUpFromCache(_ name: String, cursor: Int) async throws {
        let c = try #require(Self.cases[name])
        let hub = SyncHub(chat: c["chat"] ?? .object([:]), start: c["start"]?["hub"] ?? .object([:]))
        let backlog = hub.backlogFrame(since: cursor)
        #expect(backlog["backlog"]?.bool == true)
        #expect(backlog["from_seq"]?.int == cursor)
        #expect(backlog["to_seq"]?.int == 3)
        #expect(backlog["items"]?.array?.compactMap { $0["seq"]?.int } == (cursor == 3 ? [] : [2]))
        #expect(backlog["has_more"] == nil)
        #expect(backlog["more_after"]?.bool == false)
        try await SyncRun(c, cachedInitialThrough: cursor).play()
    }

    @Test(arguments: names)
    func scenario(_ name: String) async throws {
        let c = try #require(Self.cases[name])
        if let why = Self.known[name] {
            await withKnownIssue(Comment(rawValue: why)) { try await SyncRun(c).play() }
        } else {
            try await SyncRun(c).play()
        }
    }
}

/// The case can not be played here (yet): said as a failure, so the known list names it.
struct SyncUnplayable: Error, CustomStringConvertible {
    var description: String
}

@MainActor
final class SyncRun {
    static var current: SyncRun?
    /// Each step's view in the log (SYNC_TRACE=1 in the test's environment).
    static let traces = ProcessInfo.processInfo.environment["SYNC_TRACE"] == "1"

    let name: String
    let c: JSONValue
    let hub: SyncHub
    let key: SessionKey
    var devices: [String: SyncDevice] = [:]
    /// The device whose model `AgentSync` (one for the process) works for: the first one.
    private var first = "a"
    /// What every device's timed waits sleep on (`AppModel.clock`); a case's `wait` moves it.
    let clock = SyncClock()
    var held: [HeldWrite] = []
    /// Op id (upper case) → what a retry with it gets at once.
    var receipts: [String: SyncReply] = [:]
    /// Reads to hold: device → paths (`/items`, `/interactions`, `/sessions`).
    var holds: [String: [String]] = [:]
    var heldReads: [(device: String, path: String, data: Data, cont: CheckedContinuation<Data, Error>)] = []
    /// Requests a relaunched app had in flight: nobody hears their answers. Ended when the case is over.
    private var dead: [CheckedContinuation<Data, Error>] = []
    /// The case's devices' disks, a folder each (`SyncDevice.folder`); removed when the case is over.
    private let disks = FileManager.default.temporaryDirectory.appending(path: "sync-scenario-\(UUID().uuidString)")
    var failures: [String] = []
    private let initialInteractionsDelay: Duration
    private let cachedInitialThrough: Int?

    init(_ c: JSONValue, initialInteractionsDelay: Duration = .zero, cachedInitialThrough: Int? = nil) {
        self.initialInteractionsDelay = initialInteractionsDelay
        self.cachedInitialThrough = cachedInitialThrough
        self.c = c
        name = c["name"]?.string ?? "?"
        let chat = c["chat"] ?? .object([:])
        hub = SyncHub(chat: chat, start: c["start"]?["hub"] ?? .object([:]))
        key = SessionKey(machineID: hub.machine, sessionID: hub.sessionID)
    }

    func play() async throws {
        Self.current = self
        SyncHub.stamps = [:]
        defer { Self.current = nil; stop() }
        let names = c["devices"]?.array?.compactMap(\.string) ?? ["a"]
        first = names.first ?? "a"
        // A new chat's first message starts it from the new-conversation page (AssistantStart, #1349), not through
        // AppModel's send, so there is no chat page to project until it has started.
        if c["chat"]?["new"]?.bool == true { throw SyncUnplayable(description: "a new chat: its start is the new-conversation page's, not AppModel's") }
        AgentSync.shared.forgetChatsForTests()
        for n in names { try await start(n) }
        for (i, step) in (c["steps"]?.array ?? []).enumerated() {
            try await run(step)
            await settle()
            if Self.traces { for d in devices.values { print("SYNC \(name) step \(i + 1) \(d.name): \(d.view)") } }
            check(step["expect"], at: "step \(i + 1)")
        }
        try await finish()
        check(c["end"], at: "end", whole: true)
        if !failures.isEmpty { Issue.record(Comment(rawValue: "\(name):\n" + failures.joined(separator: "\n"))) }
    }

    private func stop() {
        for w in held { w.continuation.resume(throwing: URLError(.cancelled)) }
        held = []
        for r in heldReads { r.cont.resume(throwing: URLError(.cancelled)) }
        heldReads = []
        for c in dead { c.resume(throwing: URLError(.cancelled)) }
        dead = []
        for d in devices.values { for link in d.model.links { link.live.disconnect() } }
        try? FileManager.default.removeItem(at: disks)
        APIClient.local = nil
        LiveConnection.makeLocal = nil
        AgentSync.shared.model = { AppModel.shared }
    }

    /// The device already shows the start: connected, its lists in, the chat open and loaded.
    private func start(_ name: String) async throws {
        let folder = disks.appending(path: name)
        if let cursor = cachedInitialThrough {
            let lines = hub.lines.filter { ($0["seq"]?.int ?? 0) <= cursor }
            let items = try JSON.decoder.decode([Item].self, from: JSONEncoder().encode(JSONValue.array(lines)))
            ChatCache.save(key, items: items, hasMore: false, history: hub.history, covered: cursor, in: folder)
        }
        let d = SyncDevice(name: name, key: key, folder: folder)
        d.model.clock = clock
        if let cursor = cachedInitialThrough {
            // Load the seeded store before background prefetch can advance the on-disk copy.
            #expect(d.model.chat(key).covered == cursor)
            #expect(!d.model.chat(key).backlogBuilt)
        }
        devices[name] = d
        d.answersSubscribe = { [hub] since in hub.backlogFrame(since: since) }
        d.connect()
        #expect(d.model.machines.isEmpty)
        #expect(d.model.allSessions.isEmpty, "scenario setup must not retain a paired app's snapshot")
        if name == first { AgentSync.shared.model = { [weak d] in d?.model ?? AppModel.shared } }
        let socket = try await nextSocket(d, after: 0)
        socket.send(hub.hello(resumed: false))
        greeted[name] = d.made
        socket.send(hub.machinesFrame)
        socket.send(hub.sessionsFrame)
        await settle()
        d.model.subscribe(key, by: d.owner)
        d.model.show(key, by: d.owner)
        let cards = Set(hub.interactions.compactMap { $0["id"]?.string })
        try await waitUntil("\(name): initial session, transcript and cards were not loaded; "
                            + "session=\(d.model.session(self.key) != nil), backlogBuilt=\(d.model.chat(self.key).backlogBuilt), "
                            + "loadedOnce=\(d.model.chat(self.key).loadedOnce), covered=\(d.model.chat(self.key).covered), "
                            + "expectedCards=\(cards.sorted()), cards=\(d.model.interactions(for: self.key).map(\.id).sorted()), "
                            + "requests=\(d.requests)") {
            d.model.session(self.key) != nil && d.model.chat(self.key).backlogBuilt
                && Set(d.model.interactions(for: self.key).map(\.id)) == cards
        }
        if let cursor = cachedInitialThrough {
            #expect(d.requests.contains { $0["ws"]?.string == "subscribe" && $0["since_seq"]?.int == cursor })
        }
        d.answersSubscribe = nil
        d.requests = []
        d.noteCards()
    }

    /// `relaunch`: the app is killed and starts again from what it stored (`ChatCache`, the snapshot, the outbox), back
    /// on the chat it showed, as iOS brings it back. What it had in flight dies with it: those requests are never answered
    /// (a resumed write would let the dead app save its outbox over the new one's). The case's next `socket.open` greets it.
    private func relaunch(_ d: SyncDevice) async {
        await settle() // what the app saves on its own (the outbox, the chat's cache) is on disk by now
        dead += held.filter { $0.device == d.name }.map(\.continuation) + heldReads.filter { $0.device == d.name }.map(\.cont)
        held.removeAll { $0.device == d.name }
        heldReads.removeAll { $0.device == d.name }
        d.relaunch()
        d.model.clock = clock // before the outbox's sent messages start waiting on it
        d.model.restoreSaved() // as a paired app's launch does (AppModel.init), on a simulator paired or not
        d.connect()
        if d.name == first { AgentSync.shared.model = { [weak d] in d?.model ?? AppModel.shared } }
        d.model.subscribe(key, by: d.owner)
        d.model.show(key, by: d.owner)
        await settle()
    }

    private func nextSocket(_ d: SyncDevice, after made: Int) async throws -> SyncSocket {
        for _ in 0..<400 {
            if d.made > made, let s = d.socket, s.deliver != nil { return s }
            try await Task.sleep(for: .milliseconds(50))
        }
        throw SyncUnplayable(description: "\(d.name): the app made no new socket within 20 s")
    }

    /// Readiness is observable state, not the amount of time the runner happened to get on the main actor.
    private func waitUntil(_ failure: @autoclosure () -> String, ready: () -> Bool) async throws {
        let deadline = ContinuousClock.now + .seconds(5)
        while !ready() {
            guard ContinuousClock.now < deadline else { throw SyncUnplayable(description: failure()) }
            try await Task.sleep(for: .milliseconds(10))
        }
    }

    private func settle(ms: Int = 150) async {
        for _ in 0..<10 { await Task.yield() }
        try? await Task.sleep(for: .milliseconds(ms))
        for _ in 0..<10 { await Task.yield() }
    }

    // MARK: Steps

    private func run(_ step: JSONValue) async throws {
        if let h = step["hub"] { hub.apply(h) }
        if let socket = step["socket"] {
            let d = try device(socket["device"])
            if socket["drop"]?.bool == true {
                d.socket?.drop?()
            } else if let open = socket["open"] {
                let s = try await nextSocket(d, after: greeted[d.name] ?? 0)
                greeted[d.name] = d.made
                s.send(hub.hello(resumed: open["resumed"]?.bool ?? false))
            }
        }
        if let frames = step["frames"]?.object {
            for (n, list) in frames {
                let d = try device(.string(n))
                for f in list.array ?? [] {
                    if f["type"]?.string == "interaction_resolved", let id = f["id"]?.string { d.settledCards.insert(id) }
                    if f["type"]?.string == "interaction", let id = f["interaction"]?["id"]?.string, hub.raised.contains(id) {
                        d.settledCards.remove(id)
                    }
                    d.socket?.send(d.translated(SyncHub.stamped(f)))
                    for _ in 0..<3 { await Task.yield() }
                }
            }
        }
        if let user = step["user"] {
            let d = try device(user["device"])
            if let send = user["send"] {
                // Typed after what came before, not in the same second: the app orders a failed message among the
                // later ones by the second it was written.
                if let last = lastSend, Date.now.timeIntervalSince(last) < 1.1 { try await Task.sleep(for: .seconds(1.1 - Date.now.timeIntervalSince(last))) }
                lastSend = .now
                d.send(id: send["id"]?.string ?? "", text: send["text"]?.string ?? "")
            }
            if let answer = user["answer"] { try d.answer(answer["id"]?.string ?? "", answer["resolution"] ?? .object([:])) }
            if let id = user["retry"]?.string { d.retry(id) }
            if let id = user["discard"]?.string { d.discard(id) }
            if user["relaunch"] != nil { await relaunch(d) }
            for what in ["open", "close", "background", "foreground"] where user[what] != nil {
                throw SyncUnplayable(description: "user \(what) is not played yet")
            }
        }
        if let reply = step["reply"] {
            let d = try device(reply["device"])
            try await answer(d, reply)
        }
        if let ms = step["wait"]?.int {
            // The app's timed waits (`AppModel.clock`) see the whole of it, a second at a time, so a wait that ends and
            // starts the next one (a sent message's 60 s, then 10 s at a time) runs on as it would. Everything else
            // paced (gap fills, streamed text) is on the wall clock and gets up to 6 s of it.
            for _ in 0..<(ms / 1000) {
                clock.advance(by: .seconds(1))
                for _ in 0..<5 { await Task.yield() }
            }
            clock.advance(by: .milliseconds(ms % 1000))
            try await Task.sleep(for: .milliseconds(min(ms, 6000)))
        }
        if let hold = step["hold"] {
            let d = try device(hold["device"])
            holds[d.name, default: []].append(hold["get"]?.string ?? "")
        }
        if let release = step["release"] {
            let d = try device(release["device"])
            let path = release["get"]?.string ?? ""
            holds[d.name]?.removeAll { $0 == path }
            if let i = heldReads.firstIndex(where: { $0.device == d.name && $0.path == path }) {
                let r = heldReads.remove(at: i)
                r.cont.resume(returning: r.data)
            }
        }
    }

    private var lastSend: Date?
    /// Sockets per device that got their hello (an `open` waits for the app to make the next one).
    private var greeted: [String: Int] = [:]

    private func device(_ v: JSONValue?) throws -> SyncDevice {
        guard let n = v?.string, let d = devices[n] else { throw SyncUnplayable(description: "no device \(v?.string ?? "nil")") }
        return d
    }

    /// The hub answers every request in flight for the write.
    private func answer(_ d: SyncDevice, _ step: JSONValue) async throws {
        let write = step["write"]?.string ?? ""
        try await waitUntil("\(d.name): reply for \(write) has no pending request; requests: \(d.requests)") {
            self.held.contains { $0.device == d.name && $0.write == write }
        }
        let r = SyncReply(step)
        let done = step["done"]?.bool ?? r.ok
        let mine = held.filter { $0.device == d.name && $0.write == write }
        held.removeAll { $0.device == d.name && $0.write == write }
        for w in mine {
            if done, let receipt = step["receipt"], let op = w.op { receipts[op] = SyncReply(receipt: receipt) }
            w.continuation.resume(with: r.result())
        }
        // A card this device answered, or one the hub says is over: never open again here.
        if d.firstOpIsCard(write), r.ok || r.status == 410 { d.settledCards.insert(write) }
    }

    // MARK: Requests

    /// A request from a device's own transport (`SyncDevice.connect`).
    nonisolated static func http(_ req: LocalRequest, from device: String) async throws -> Data {
        guard let run = await SyncRun.current else { throw URLError(.cancelled) }
        return try await run.serve(req, from: device)
    }

    /// A request that went to Demo Mode's hub instead of a device's transport: it is not told apart from the other
    /// device's, so it is a failure, never served.
    nonisolated static func stray(_ req: LocalRequest) async throws -> Data {
        await MainActor.run { SyncRun.current?.failures.append("a request on no device's transport: \(req.method) \(req.path)") }
        throw URLError(.cancelled)
    }

    private func serve(_ req: LocalRequest, from device: String) async throws -> Data {
        guard let d = devices[device] else { throw URLError(.cancelled) }
        let write = d.note(req)
        if req.method == "GET" {
            if req.path.hasPrefix("/api/v1/ops/") {
                let op = req.path.components(separatedBy: "/").last?.uppercased() ?? ""
                if receipts[op] != nil { return SyncHub.json(["state": .string("done")]) }
                if held.contains(where: { $0.op == op }) { return SyncHub.json(["state": .string("pending")]) }
                throw APIError(status: 404, message: "no such op")
            }
            guard let raw = hub.read(req) else { throw APIError(status: 404, message: "not in the scenario") }
            let data = (try? JSONDecoder().decode(JSONValue.self, from: raw)).flatMap { try? JSONEncoder().encode(d.translated($0)) } ?? raw
            let path = SyncHub.readName(req.path)
            if path == "/interactions", initialInteractionsDelay > .zero, d.answersSubscribe != nil {
                try await Task.sleep(for: initialInteractionsDelay)
            }
            if let i = holds[d.name]?.firstIndex(of: path) {
                holds[d.name]?.remove(at: i)
                return try await withCheckedThrowingContinuation { heldReads.append((d.name, path, data, $0)) }
            }
            return data
        }
        guard let write, req.path.hasSuffix("/messages") || req.path.hasSuffix("/resolve") else { return Data("{}".utf8) }
        let op = req.headers["X-Persona-Op-Id"]?.uppercased()
        if let op, let r = receipts[op] { return try r.result().get() }
        return try await withCheckedThrowingContinuation { held.append(HeldWrite(device: d.name, write: write, op: op, continuation: $0)) }
    }

    // MARK: The view

    private func check(_ expect: JSONValue?, at: String, whole: Bool = false) {
        for d in devices.values {
            let v = d.view
            for broken in d.invariants(v) { failures.append("\(at): \(broken)") }
            guard let e = expect?[d.name] else { continue }
            if let want = e["messages"]?.array {
                let w = want.map { SyncView.Message(from: $0["from"]?.string ?? "", text: $0["text"]?.string ?? "", state: $0["state"]?.string) }
                if w != v.messages { failures.append("\(at): \(d.name) messages\n  want \(w.map(Self.say))\n  got  \(v.messages.map(Self.say))") }
            }
            if let want = e["cards"]?.object {
                for (id, s) in want {
                    let got = v.cards[id]
                    if (s.string == "open") != (got == "open") { failures.append("\(at): \(d.name) card \(id) want \(s.string ?? "?") got \(got ?? "not shown")") }
                }
                for (id, s) in v.cards where s == "open" && want[id] == nil { failures.append("\(at): \(d.name) card \(id) is open, not expected") }
            }
            if let want = e["status"]?.string, want != v.status { failures.append("\(at): \(d.name) status want \(want) got \(v.status ?? "nil")") }
            if let live = e["live"] {
                let want = live.string
                if want != v.live { failures.append("\(at): \(d.name) live want \(want ?? "nil") got \(v.live ?? "nil")") }
            }
            if let want = e["requests"]?.array {
                var from = d.requestsChecked
                // one_of: any one of these, where the protocol allows several ways (#2770)
                func matches(_ got: [String: JSONValue], _ want: JSONValue) -> Bool {
                    if let any = want["one_of"]?.array { return any.contains { matches(got, $0) } }
                    return (want.object ?? [:]).allSatisfy { got[$0.key] == $0.value }
                }
                for r in want {
                    guard let i = d.requests[from...].firstIndex(where: { matches($0, r) }) else {
                        failures.append("\(at): \(d.name) no request \(r) after \(Array(d.requests[d.requestsChecked...]))")
                        break
                    }
                    from = i + 1
                }
                d.requestsChecked = d.requests.count
            }
        }
    }

    private static func say(_ m: SyncView.Message) -> String { "\(m.from)[\(m.state ?? "-")] \(m.text)" }

    /// Everything settles: the socket open, held reads answered, writes left as they are.
    private func finish() async throws {
        for r in heldReads { r.cont.resume(returning: r.data) }
        heldReads = []
        await settle(ms: 400)
    }
}

extension SyncDevice {
    /// The case's name for a write is a card's id.
    func firstOpIsCard(_ write: String) -> Bool { !sends.contains { $0.id == write } }
}
