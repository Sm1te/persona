import Foundation
import Observation

/// One tool invocation with its result, as shown in a chat.
nonisolated struct ToolCall: Identifiable, Hashable, Sendable {
    var use: Item
    var result: Item?
    var images: [ImagePayload] = []
    var results: [Item] = []
    var agentStatus: String?

    var id: String { use.id }
    var name: String { use.toolName ?? "tool" }
    var input: JSONValue { use.toolInput ?? .object([:]) }
    var isRunning: Bool {
        if let agentStatus { return !SubagentDelivery.isTerminal(agentStatus) }
        return result == nil
    }
    var isError: Bool { agentStatus == "failed" || agentStatus == "error" || (result?.isError ?? false) }
    /// What it came to, when the node said (`ToolOutcome`).
    var outcome: ToolOutcome? { result?.outcome }
    /// An error that is not the call being denied, refused or interrupted: what it ran failed. The red a row and its
    /// chip turn; `isError` still says the model got an error back.
    var failed: Bool { isError && !(outcome?.stopped ?? false) }
}

nonisolated extension ChatRow {
    /// The item of a user input row, including image-only messages.
    var userInput: Item? {
        switch self {
        case .user(let i): i
        case .image(let i) where i.role == "user" && i.toolUseID == nil: i
        default: nil
        }
    }

    var isUserInput: Bool { userInput != nil }
}

nonisolated enum ChatRow: Identifiable, Hashable, Sendable {
    case user(Item)
    case assistant(Item)
    case thinking(Item)
    case tools([ToolStep])
    case notice(Item)
    case image(Item)
    case recap(Item)

    /// An answer or a notice that may be a model API error (`APIErrorInfo` decides).
    var errorCandidate: Item? {
        switch self {
        case .assistant(let i), .notice(let i): i
        default: nil
        }
    }

    var id: String {
        switch self {
        case .user(let i), .assistant(let i), .thinking(let i), .notice(let i), .image(let i), .recap(let i): i.id
        case .tools(let steps): "tools-" + (steps.first?.id ?? "")
        }
    }
}

nonisolated extension ChatRow {
    var firstTS: Date? {
        switch self {
        case .user(let i), .assistant(let i), .thinking(let i), .notice(let i), .image(let i), .recap(let i): i.ts
        case .tools(let steps): steps.lazy.compactMap(\.firstTS).first
        }
    }
    var lastTS: Date? {
        switch self {
        case .user(let i), .assistant(let i), .thinking(let i), .notice(let i), .image(let i), .recap(let i): i.ts
        case .tools(let steps): steps.reversed().lazy.compactMap(\.lastTS).first
        }
    }
}

/// A step inside a run of tool activity: a call, a thought between calls, or a run of them the page left out
/// (`Item.fold`, fetched when the work line is opened).
nonisolated enum ToolStep: Identifiable, Hashable, Sendable {
    case call(ToolCall)
    case thought(Item)
    case folded(Item)

    var id: String {
        switch self {
        case .call(let c): c.id
        case .thought(let i), .folded(let i): i.id
        }
    }
    var firstTS: Date? {
        switch self {
        case .call(let c): c.use.ts
        case .thought(let i), .folded(let i): i.ts
        }
    }
    var lastTS: Date? {
        switch self {
        case .call(let c): c.result?.ts ?? c.use.ts
        case .thought(let i): i.ts
        case .folded(let i): i.fold?.until ?? i.ts
        }
    }
    /// Tool calls and thoughts in it (a fold counts what it stands for).
    var tools: Int {
        switch self {
        case .call: 1
        case .thought: 0
        case .folded(let i): i.fold?.tools ?? 0
        }
    }
    var thoughts: Int {
        switch self {
        case .call: 0
        case .thought: 1
        case .folded(let i): i.fold?.thoughts ?? 0
        }
    }
}

/// One exchange: what the user said (including messages queued while the agent worked), everything the agent did,
/// and its final answer. Finished turns render as messages → one folded "process" row → the answer.
nonisolated struct Turn: Identifiable, Hashable, Sendable {
    var id: String
    var users: [ChatRow] = []
    var process: [ChatRow] = []
    var answer: ChatRow?
    var trailing: [ChatRow] = [] // notices that belong to the turn (e.g. "interrupted")
    /// Background tasks that woke the agent during this exchange (their notices).
    var wakes: [Item] = []
    /// The latest wake has not produced an answer yet: its continuation is live, even after its rows move into `ordered`.
    var hasPendingWake = false
    /// What came after the answer (a follow-up once a background task reported back).
    var tail: [ChatRow] = []
    /// Everything the agent did in arrival order, answer included.
    var steps: [ChatRow] = []
    /// Messages and steps in arrival order (how the running turn is shown: input sent mid-turn stays where it was sent).
    var ordered: [ChatRow] = []

    /// From the first message to the last thing the agent did ("Worked 5 min 12 s").
    var startedAt: Date? { (users + steps).lazy.compactMap(\.firstTS).first }
    var endedAt: Date? { (steps + trailing).reversed().lazy.compactMap(\.lastTS).first }

    var toolCount: Int {
        process.reduce(0) { n, r in
            if case .tools(let steps) = r { return n + steps.reduce(0) { $0 + $1.tools } }
            return n
        }
    }
    var thoughtCount: Int {
        process.reduce(0) { n, r in
            switch r {
            case .thinking: return n + 1
            case .tools(let steps): return n + steps.reduce(0) { $0 + $1.thoughts }
            default: return n
            }
        }
    }
    var interimCount: Int { process.filter { if case .assistant = $0 { true } else { false } }.count }

    /// A wake continues the same exchange; its tools and narration join the ordinary execution summary. Until new
    /// words arrive, `hasPendingWake` keeps them in the summary rather than promoting the earlier narration to a
    /// separate answer (#2796).
    mutating func pickAnswerAcrossWakes() {
        let all = ordered
        var pick: Int?
        hasPendingWake = false
        for (i, row) in all.enumerated() {
            if case .notice(let n) = row, n.wake == true { pick = nil; hasPendingWake = true }
            if case .assistant = row { pick = i; hasPendingWake = false }
        }
        guard let pick else { return }
        tail = all[(pick + 1)...].filter { !$0.isUserInput }
        ordered = Array(all[...pick]) + all[(pick + 1)...].filter(\.isUserInput)
        process = ordered.filter { !$0.isUserInput }
    }
}

nonisolated enum SubagentDelivery {
    static func isTerminal(_ status: String?) -> Bool {
        switch status {
        case "completed", "failed", "error", "killed", "stopped": true
        default: false
        }
    }

    static func isReceipt(_ item: Item) -> Bool {
        if let task = item.task, task.kind == "agent" {
            guard task.status == "running" || task.status == "waiting", task.detail == nil else { return false }
        }
        let text = item.text ?? ""
        return text.hasPrefix("Async agent launched successfully") || text.contains("moved to the background (ID:")
    }

    static func isWake(_ row: ChatRow) -> Bool {
        guard case .notice(let item) = row else { return false }
        return item.kind == .toolResult && item.task?.kind == "agent" && item.wake == true
    }
}

/// Background tasks and the calls that started them: a notice names its call by tool_use_id, or only by the task id
/// the call's result announced ("running in background with ID: …", "Monitor started (task …"). Built with the
/// rows; a call outside the loaded items is simply not found.
nonisolated struct TaskIndex: Equatable, Sendable {
    /// A call's notices, oldest first, by the call's tool_use_id.
    var notes: [String: [TaskNote]] = [:]
    var reports: [String: [Item]] = [:]
    var agentStatuses: [String: String] = [:]
    /// The call a notice came from (notice item id → tool_use_id).
    var calls: [String: String] = [:]
    /// Calls whose result says they went to the background (they may not have reported back yet).
    var started: Set<String> = []
    /// What each of those calls was: its description, and when it started.
    var descriptions: [String: String] = [:]
    var startedAt: [String: Date] = [:]
    /// Every call in the lines, and those whose result is in (`Subagent.running(in:)`).
    var asked: Set<String> = []
    var answered: Set<String> = []

    /// A subagent's id only after "agentId:", as the node reads it: Claude Code 2.1.319's receipt opens with "including the
    /// agentId below", and "below" taken for the id left its notice without its card (#6381).
    nonisolated(unsafe) private static let launched = /(?:(?:running in background with ID|Monitor started \(task|moved to the background \(ID):?|agentId:)\s*([A-Za-z0-9_-]+)/

    static func build(_ items: [Item]) -> TaskIndex {
        var x = TaskIndex()
        var byTask: [String: String] = [:]
        var uses: [String: Item] = [:]
        for it in items {
            switch it.kind {
            case .toolUse:
                if let id = it.toolUseID { uses[id] = it; x.asked.insert(id) }
            case .toolResult:
                if let id = it.toolUseID { x.answered.insert(id) }
                if let id = it.toolUseID, let note = it.task, note.kind == "agent" {
                    x.agentStatuses[id] = note.status ?? ""
                    x.notes[id, default: []].append(note)
                    x.calls[it.id] = id
                    if !SubagentDelivery.isReceipt(it), !(it.text ?? "").isEmpty {
                        x.reports[id, default: []].append(it)
                    }
                    if let task = note.taskID { byTask[task] = id }
                }
                if it.task?.kind == "agent", !SubagentDelivery.isReceipt(it) { continue }
                guard let id = it.toolUseID, let text = it.text, text.contains("background") || text.contains("Monitor started") || text.contains("agentId"),
                      let m = text.firstMatch(of: launched) else { continue }
                // A subagent that ran to its end says its agentId under its report too (to go on with SendMessage):
                // only its launch receipt means it went to the background (#5602: every finished one read as such).
                if let use = uses[id], use.toolName == "Agent" || use.toolName == "Task",
                   !text.contains("Async agent launched"), !text.contains("moved to the background"), use.toolInput?["run_in_background"]?.bool != true {
                    continue
                }
                byTask[String(m.1)] = id
                x.started.insert(id)
                if let use = uses[id] {
                    x.descriptions[id] = use.toolInput?["description"]?.string
                    x.startedAt[id] = use.ts
                }
            case .notice:
                guard let n = it.task, let call = n.toolUseID ?? n.taskID.flatMap({ byTask[$0] }) else { continue }
                x.notes[call, default: []].append(n)
                x.calls[it.id] = call
                if n.kind == "agent", n.status != "event", x.agentStatuses[call] != nil {
                    x.agentStatuses[call] = n.status ?? ""
                }
                if n.kind == "agent", n.status != "event", n.detail?.isEmpty == false {
                    var report = it
                    report.kind = .toolResult
                    report.role = "user"
                    report.toolUseID = call
                    report.text = n.detail
                    report.fullSize = nil
                    report.isError = n.failed || n.status == "error"
                    x.reports[call, default: []].append(report)
                }
                if x.descriptions[call] == nil, let use = uses[call] {
                    x.descriptions[call] = use.toolInput?["description"]?.string
                    x.startedAt[call] = use.ts
                }
            default: break
            }
        }
        return x
    }
}

/// Rows grouped for display: turns, plus standalone rows (recaps, compaction notices) between them.
nonisolated enum ChatBlock: Identifiable, Hashable, Sendable {
    case turn(Turn)
    case row(ChatRow)

    var id: String {
        switch self {
        case .turn(let t): "turn-" + t.id
        case .row(let r): r.id
        }
    }

    static func build(_ rows: [ChatRow]) -> [ChatBlock] {
        var blocks: [ChatBlock] = []
        var current: Turn?
        // The last exchange as it was before closing, and where it sits: a background task that reports back after
        // a recap card (Claude Code writes one when a finished session sits idle) still belongs to that exchange.
        var lastRaw: Turn?
        var lastIndex = 0
        var reopenedAt: Int?

        func close() {
            guard var t = current else { return }
            lastRaw = t
            if !t.wakes.isEmpty { t.pickAnswerAcrossWakes() }
            // The answer is the last thing the assistant said; everything before it is process.
            // (Steps after the final words, rare trailing tool calls, stay in the process.)
            t.steps = t.process
            if let i = t.process.lastIndex(where: { if case .assistant = $0 { true } else { false } }) {
                t.answer = t.process.remove(at: i)
            }
            if let i = reopenedAt {
                blocks.insert(.turn(t), at: i) // back in its place, the recap card still after it
                lastIndex = i
            } else {
                lastIndex = blocks.count
                blocks.append(.turn(t))
            }
            reopenedAt = nil
            current = nil
        }

        var messageEntry: String?
        // Persists across recap/continuation blocks until the next actual input, not merely until `close`.
        var supervisionRound = false
        for row in rows {
            if let item = row.userInput {
                let sameMessage = current?.ordered.last?.isUserInput == true && item.entry == messageEntry
                let supervision = Injected.of(item)?.isSupervision == true
                if current != nil, sameMessage || (item.queued == true && !supervision && !supervisionRound) {
                    current!.users.append(row)
                    current!.ordered.append(row)
                } else {
                    close()
                    current = Turn(id: row.id, users: [row], ordered: [row])
                }
                if !sameMessage { supervisionRound = supervision }
                messageEntry = item.entry
                continue
            }
            switch row {
            case .recap:
                close()
                blocks.append(.row(row))
            case .notice(let item):
                // Inside a turn: "interrupted" closes it visibly; compaction / API errors are part of the process
                // (auto-compaction happens mid-turn and must not split it).
                if item.wake == true, current == nil, let raw = lastRaw, blocks.indices.contains(lastIndex),
                   case .turn(let closed) = blocks[lastIndex], closed.id == raw.id {
                    blocks.remove(at: lastIndex) // reopen the exchange the recap closed
                    reopenedAt = lastIndex
                    current = raw
                }
                if item.wake == true, current != nil {
                    // A background task woke the idle agent: still the same exchange (the answer is picked at close).
                    current!.wakes.append(item)
                    current!.process.append(row)
                    current!.ordered.append(row)
                } else if current == nil {
                    // e.g. a background command finishing after a recap: part of the (next) work, never its own line.
                    current = Turn(id: row.id)
                    current!.process.append(row)
                    current!.ordered.append(row)
                } else if item.task == nil, (item.text ?? "").contains("中断") { // i18n-ignore: matches the hub's notice
                    current!.trailing.append(row)
                } else {
                    current!.process.append(row)
                    current!.ordered.append(row)
                }
            default:
                if current == nil { current = Turn(id: row.id) }
                current!.process.append(row)
                current!.ordered.append(row)
            }
        }
        close()
        return blocks
    }
}

/// Where a batch of items sits in the transcript (the hub's `history` / `from_seq` / `to_seq`; nil from older hubs).
nonisolated struct ItemSpan: Sendable, Equatable {
    /// The version of the transcript file; another one means it was replaced and its seqs started over.
    var history: String?
    /// The seq the batch continues from (catch-up, live pushes, after_seq pages); nil for pages from the end.
    var from: Int?
    /// The last line the batch accounts for: lines without items count, so it is often past its last item.
    var to: Int?
}

nonisolated extension Item {
    /// The transcript entry, shared by all of one input's text and image blocks.
    var entry: String { String(id.prefix { $0 != "#" }) }

    var isUserInput: Bool { role == "user" && (kind == .text || (kind == .image && toolUseID == nil)) }

    /// The block within its transcript line (ids end in "#<n>").
    var part: Int { Self.part(of: id) }

    static func part(of id: String) -> Int {
        guard let hash = id.lastIndex(of: "#") else { return 0 }
        return Int(id[id.index(after: hash)...]) ?? 0
    }

    /// Work a fold may stand for: tool calls, their results and images, thoughts, folds.
    var isWork: Bool {
        switch kind {
        case .thinking, .toolUse, .toolResult, .fold: true
        case .image: toolUseID != nil
        default: false
        }
    }

    /// This fold's range holds `other` (by line, then block).
    func folds(_ other: Item) -> Bool {
        guard let f = fold else { return false }
        return (other.seq, other.part) >= (seq, part) && (other.seq, other.part) <= (f.throughSeq, Self.part(of: f.throughID))
    }
}

/// Transcript items and live state of one session, shared by every view showing it.
@Observable
final class ChatStore {
    let key: SessionKey
    @ObservationIgnored private let cacheFolder: URL?
    private(set) var items: [Item] = []
    private(set) var rows: [ChatRow] = []
    private(set) var blocks: [ChatBlock] = []
    /// Every image in the conversation (the viewer pages through them), rebuilt with `blocks`: a view that derived it
    /// from `items` in its body redrew on every item.
    private(set) var images: [ImagePayload] = []
    /// Background tasks and their calls (`TaskIndex`), rebuilt with `blocks`.
    private(set) var tasks = TaskIndex()
    /// Bumped when the items start over (another history, or the latest page replacing what was here): a frozen view
    /// of the old items (`ChatView.Frozen`) means nothing any more.
    private(set) var resets = 0
    /// Bumped whenever `blocks` is rebuilt: views key derived data (the chat's lines) on it instead of diffing blocks.
    @ObservationIgnored private(set) var version = 0
    private(set) var hasMore = false
    private(set) var loadedOnce = false
    /// `blocks` (so the chat's lines) are built from items that include the first backlog. `loadedOnce` turns true as
    /// the backlog merges, before its lines are built (`scheduleRebuild` builds off the main actor): what is looked up
    /// in the lines on screen waits for this one. A cold start with the chat cached gave up on the saved reading place
    /// while the lines were still the cache's last two turns (#597).
    private(set) var backlogBuilt = false
    var screen: String = ""
    /// Text of the assistant message being generated (headless sessions stream it token by token), or what of an
    /// agent's live message its newest items do not hold yet (`agentStreamed`). Set through `showLive`, which a clear's
    /// fallback watches.
    private(set) var liveText: String = ""
    /// Bumped each time `liveText` is set: a clear's fallback (`clearLive`) does nothing once anything else set it.
    @ObservationIgnored private var liveStamp = 0
    /// The newest line when the live text began: its copy is written after it, so lines up to this one are not.
    @ObservationIgnored private var liveSince = 0
    /// The live text is an agent's (its last delta came through `agentStreamed`): a chat of it draws it under the
    /// newest items, as the main chat draws the assistant's, matched against the transcript (`LiveText`).
    private(set) var liveIsAgents = false
    /// An agent's live message as its last delta brought it (docs/integrations/claude-integration.md "Live replies
    /// (MessageDisplay)"), "" for none: never drawn as it is, only what of it the newest items lack (`liveText`).
    @ObservationIgnored private(set) var agentLive = ""
    /// What an agent's live text should be changed while a rebuild was on its way: worked out again when the rebuild
    /// publishes the rows, so words leave the live copy in the same pass their row comes (no gap, no double).
    @ObservationIgnored private var agentLiveDue = false
    /// Where the latest page from the end begins (line, block): what is before it came with older pages, which an
    /// agent's live text is never checked against. Nil: all of it (a cached chat, an older hub's batches).
    @ObservationIgnored private var newestFrom: (Int, Int)?
    /// The streamed message is in the items, its row not yet in `rows`: the live copy stays until the rebuild publishes
    /// the row (`endLive`).
    @ObservationIgnored private var liveEnds: String?
    var screenStatus: SessionStatus = .unknown
    var screenUpdatedAt: Date?
    var error: String?
    var isLoadingOlder = false
    /// Messages sent from this phone that the transcript has not shown yet: drawn at the end right away, and a failed one
    /// before the first message written after it (`ChatLines.placeFailed`).
    var pending: [PendingMessage] = [] {
        didSet { rebuildQueuePhotoMarks() }
    }
    /// The Persona queue's own messages, kept to derive their photo marks again when the outbox changes.
    @ObservationIgnored private var ownQueue: [String] = []
    /// Local photo counts for the own queue rows, derived once per queue/outbox change rather than in a view body (P4).
    private(set) var queuePhotoMarks: [Int] = []

    func updateQueuePhotoMarks(queued: [String]?, own: Int) {
        ownQueue = Array((queued ?? []).prefix(max(0, own)))
        rebuildQueuePhotoMarks()
    }

    private func rebuildQueuePhotoMarks() {
        let marks = ownQueue.map { raw in
            pending.first { PendingMessage.same($0.text, raw) }?.images.count ?? 0
        }
        if marks != queuePhotoMarks { queuePhotoMarks = marks }
    }
    /// When this phone wrote the messages it sent whose copies have arrived, by their id (`Item.clientMsgID`, upper
    /// case): a failed message goes by the phone's own times, not by when the computer wrote a copy (a queued message
    /// is written when the agent takes it in, minutes later; its clock may differ) (#6023). This run's only.
    @ObservationIgnored private(set) var writtenAt: [String: Date] = [:]
    /// A pending message's real copy arrived and its bubble went (the app saves its outbox).
    @ObservationIgnored var onArrived: (() -> Void)?
    /// A queued message taken back out of the agent (Take Back to Edit, Delete) never reaches the transcript. Its sent bubble, hidden
    /// while the queue strip showed it, came back at the end of the chat as if it had gone out again, and stayed while
    /// the agent worked. Drops that bubble: the oldest sent one with those words. Returns whether one went.
    @discardableResult func pulledBack(_ text: String) -> Bool {
        guard let i = pending.firstIndex(where: { $0.state == .sent && PendingMessage.same(text, $0.text) }) else { return false }
        pending.remove(at: i)
        return true
    }
    /// Sent messages whose transcript copy has arrived (gone from `pending`) while the rows with that copy are still
    /// being built: still drawn as they were until those rows are in, the same pass. Dropped at once, the message was
    /// off the screen for a pass, the first one of a new conversation with it (#1911).
    private(set) var arriving: [PendingMessage] = []

    @ObservationIgnored private var ids = Set<String>()
    /// The version of the transcript the items come from (`ItemSpan.history`). Another one means the file was replaced
    /// and its seqs started over: everything here is stale.
    @ObservationIgnored private(set) var history: String?
    /// Everything from the first item through this seq is here, without holes (lines without items count). Catch-ups
    /// ask for what comes after it; 0 = no baseline yet (nothing loaded from the end of the transcript).
    @ObservationIgnored private(set) var covered = 0
    /// When the last batch of items came (a push, a page, a pull), empty ones included, or streamed words
    /// (`heardStream`): a subscription that stays quiet while its session moves on has stopped
    /// (`AgentSyncPacing.catchUpAt`). Nil before any.
    @ObservationIgnored private(set) var arrivedAt: Date?
    /// Folds opened here (`unfold`), as far as each reached. A page with one of them again (the latest page can come
    /// twice as a chat opens) leaves its work open: folded again, its row had already fetched it and stayed on
    /// "Loading N steps…" (#6038). Starting over forgets them. By the fold's id, the fold as the page brought it.
    @ObservationIgnored private var opened: [String: Item] = [:]

    init(key: SessionKey, cacheFolder: URL? = nil) {
        self.key = key
        self.cacheFolder = cacheFolder
        // Paint the last exchanges from the disk cache right away; the server's backlog merges in after.
        if let cached = ChatCache.load(key, in: cacheFolder), !cached.items.isEmpty {
            // Refs again: a cache written before they named a chat's group of hubs (#1555) sent its pictures to the
            // current group's hub.
            items = Self.withImageRefs(cached.items, key)
            ids = Set(items.map(\.id))
            hasMore = cached.hasMore
            history = cached.history
            covered = cached.covered ?? (cached.items.last?.seq ?? 0)
            rows = ChatStore.buildRows(items)
            blocks = ChatBlock.build(rows)
            tasks = TaskIndex.build(items)
            version += 1
        }
    }

    var lastSeq: Int { items.last?.seq ?? 0 }

    /// Streamed words came for the chat: its subscription is alive while a long reply is written, though no batch
    /// comes until it ends.
    func heardStream() { arrivedAt = .now }
    /// The time of the newest line that has one (nil: none has); a folded run counts as its last step.
    var newestLineAt: Date? { items.last(where: { $0.ts != nil }).flatMap { $0.fold?.until ?? $0.ts } }

    /// What a batch left to do: nothing, fetch what comes after `covered` (a hole, or a catch-up the hub capped), or
    /// start over from the latest page (the transcript was replaced under a continuing batch).
    enum Gap: Equatable { case none, fill, reload }

    /// Takes a batch of items with its place in the transcript. Batches that continue from a seq must start inside
    /// what is here (`span.from <= covered`) — one that does not reveals a hole, which `fill` fetches; its items are
    /// kept meanwhile. A page counted back from the end that does not reach what is here replaces it (an old disk
    /// cache, several exchanges behind). Without a span (an older hub) batches are trusted, as before.
    @discardableResult
    func apply(_ new: [Item], span: ItemSpan, backlog: Bool = false, hasMore: Bool? = nil, moreAfter: Bool = false) -> Gap {
        arrivedAt = .now
        defer { newestArrived() }
        if let h = span.history, let mine = history, h != mine {
            reset()
            history = h
            if let from = span.from, from > 0 { return .reload } // the rest of a file we never saw from its start
        }
        if let h = span.history { history = h }
        guard let to = span.to else {
            merge(new, backlog: backlog, hasMore: hasMore)
            covered = max(covered, lastSeq)
            return moreAfter ? .reload : .none
        }
        if let from = span.from {
            merge(new, backlog: backlog, hasMore: hasMore)
            // No baseline yet: the backlog from the end sets one. A batch from line 0 (a new session watched from its
            // first line) is its own baseline: without it, holes after it went unnoticed. If the backlog already came
            // and set none (the transcript did not exist yet) and the first lines' push was lost, only the latest
            // page tells where we are.
            if covered == 0 && from > 0 { return loadedOnce ? .reload : .none }
            guard from <= covered else { return .fill }
            covered = max(covered, to)
            return moreAfter ? .fill : .none
        }
        // A page from the end.
        if let first = new.first { newestFrom = (first.seq, first.part) }
        if covered > 0, let first = new.first?.seq, first > covered + 1 {
            replace(with: new, hasMore: hasMore ?? true, span: span)
            if backlog { loadedOnce = true }
            return .none
        }
        merge(new, backlog: backlog, hasMore: hasMore)
        covered = max(covered, to)
        return .none
    }

    private func reset() {
        resets += 1
        items = []
        ids = []
        opened = [:]
        newestFrom = nil
        liveSince = 0 // the new file's seqs started over
        covered = 0
        hasMore = false
        loadedOnce = false
        backlogBuilt = false
        scheduleRebuild()
    }

    func merge(_ new: [Item], backlog: Bool = false, hasMore: Bool? = nil) {
        if backlog || !new.isEmpty { error = nil } // data arrived: a stale "machine is offline" no longer applies
        // A replayed disk cache can hold an id twice. Keep the newest copy before indexing, but only after collecting
        // promotion evidence; ids is already a Set, so a count mismatch finds this without scanning every ordinary merge.
        func newestCopies(_ list: [Item]) -> [Item] {
            guard list.count > 1 else { return list }
            var seen = Set<String>()
            return Array(list.reversed().filter { seen.insert($0.id).inserted }.reversed())
        }
        let deduplicated = items.count != ids.count
        // Promotion is monotone, including when deduplication or overlapping real work takes a promoted fold's place.
        // Retain it on the replacement items before either kind of overlap removes evidence. Opened folds count too: a repeated
        // folded page can bring authoritative promotion while the UI keeps that work expanded.
        let evidence = (items + Array(opened.values) + new).filter { $0.supervisionUser == true || $0.fold?.supervisionUser == true }
        let promotedIDs = Set(evidence.filter { $0.supervisionUser == true }.map(\.id))
        let promotedFolds = evidence.filter { $0.fold != nil }
        let promotedWork = evidence.filter(\.isWork)
        func retainingPromotion(_ list: [Item]) -> [Item] {
            guard !evidence.isEmpty else { return list }
            return list.map { it in
                guard promotedIDs.contains(it.id) ||
                    (it.isWork && promotedFolds.contains { $0.folds(it) }) ||
                    (it.fold != nil && promotedWork.contains { it.folds($0) }) else { return it }
                var it = it
                it.supervisionUser = true
                if it.fold != nil { it.fold?.supervisionUser = true }
                return it
            }
        }
        if deduplicated {
            items = newestCopies(items)
            ids = Set(items.map(\.id))
        }
        let retained = retainingPromotion(items)
        var changed = deduplicated || retained != items
        if changed { items = retained; settled = nil }
        let promoted = newestCopies(retainingPromotion(new))
        let batchWork = promoted.filter { $0.isWork && $0.kind != .fold }
        let fresh = promoted.filter { it in
            !stillOpen(it) && !(it.kind == .fold && batchWork.contains { it.folds($0) })
        }
        if dropFolded(by: fresh) { changed = true }
        if dropOverlappingFolds(by: fresh) { changed = true }
        // Pages can repeat an id with new metadata or a completed receipt. The new copy wins, but never erases the
        // evidence above. Append-only pushes keep the fast path without building an index.
        let indices = fresh.contains { ids.contains($0.id) } ? Dictionary(uniqueKeysWithValues: items.enumerated().map { ($0.element.id, $0.offset) }) : [:]
        var inOrder = !deduplicated // Keeping the last cached copies can move an older line behind a newer one.
        var added: [Item] = []
        for it in Self.withImageRefs(fresh, key) {
            if let i = indices[it.id] {
                if items[i] != it {
                    if (items[i].seq, items[i].part) != (it.seq, it.part) { inOrder = false }
                    items[i] = it
                    settled = nil
                    changed = true
                }
                continue
            }
            guard !ids.contains(it.id) else { continue }
            if let last = items.last, (it.seq, it.part) < (last.seq, last.part) { inOrder = false }
            ids.insert(it.id)
            items.append(it)
            added.append(it)
            changed = true
        }
        // "Nothing older" only counts from a page that reaches our oldest item: a catch-up batch (newer items only)
        // saying so had the list stop at its oldest loaded message with no way further up.
        let reachesStart = new.map(\.seq).min().map { $0 <= (items.first?.seq ?? .max) } ?? false
        if let hasMore, backlog, !loadedOnce || items.count <= new.count, hasMore || reachesStart { self.hasMore = hasMore }
        if backlog { loadedOnce = true }
        guard changed else {
            if backlog, !rebuildScheduled, !backlogBuilt { backlogBuilt = true } // nothing new: the built lines have it all
            return
        }
        if !pending.isEmpty { // the real message arrived: drop the local copy, one per message
            let before = pending.count
            for it in added {
                if let i = pending.firstIndex(where: { PendingMessage.arrived($0, as: it) }) {
                    let p = pending.remove(at: i)
                    if let at = p.createdAt {
                        if writtenAt.count >= 500 { writtenAt = writtenAt.filter { $0.value > at.addingTimeInterval(-86400) } }
                        writtenAt[p.id.uuidString] = at
                    }
                    arriving.append(p)
                }
            }
            if pending.count != before { onArrived?() }
        }
        // Streamed items arrive in order: only older pages / reconnect backlogs need the sort.
        if !inOrder { items = Self.ordered(items) }
        scheduleRebuild()
        scheduleSave()
    }

    /// A fold stands for the work in its range: what is here of that work (steps, or the same fold over a shorter
    /// range from an older page) gives way to it.
    private func dropFolded(by new: [Item]) -> Bool {
        let folds = new.filter { f in f.kind == .fold && !items.contains { $0.id == f.id && $0.fold == f.fold } }
        guard !folds.isEmpty else { return false }
        let before = items.count
        items.removeAll { it in it.isWork && folds.contains { $0.folds(it) } }
        guard items.count != before else { return false }
        ids = Set(items.map(\.id))
        settled = nil
        return true
    }

    /// Real work from an overlapping page replaces the old fold, even when only part of its range has arrived.
    /// `merge` has already transferred any authoritative promotion to that work.
    private func dropOverlappingFolds(by new: [Item]) -> Bool {
        let work = new.filter { $0.isWork && $0.kind != .fold }
        guard !work.isEmpty, items.contains(where: { $0.kind == .fold }) else { return false }
        let before = items.count
        items.removeAll { it in it.kind == .fold && work.contains { it.folds($0) } }
        guard items.count != before else { return false }
        ids = Set(items.map(\.id))
        settled = nil
        return true
    }

    /// A fold over work opened here that reaches no further than what was opened (`opened`).
    private func stillOpen(_ f: Item) -> Bool {
        guard f.kind == .fold, let was = opened[f.id]?.fold, let now = f.fold else { return false }
        return (now.throughSeq, Item.part(of: now.throughID)) <= (was.throughSeq, Item.part(of: was.throughID))
    }

    /// The work a fold stood for, fetched (`AppModel.unfold`): it takes the fold's place.
    func unfold(_ fold: Item, into work: [Item]) {
        guard let i = items.firstIndex(where: { $0.id == fold.id }) else { return }
        let retained = items.remove(at: i)
        ids.remove(fold.id)
        opened[fold.id] = retained // the caller may hold an older copy without the latest promotion
        settled = nil
        merge(work.filter { $0.kind != .fold })
        scheduleRebuild()
    }

    @ObservationIgnored private var rebuildScheduled = false
    /// A rebuild is on its way: `blocks` do not hold every item yet (not observed: asked, not watched).
    var isRebuilding: Bool { rebuildScheduled }
    @ObservationIgnored private var rebuildRevision = 0
    @ObservationIgnored private var settled: SettledRows?

    /// The rows and blocks of everything before the last exchange, reused while items only grow at the end (a live
    /// session): rebuilding all of them on every event took 30 ms at 1000 exchanges.
    nonisolated struct SettledRows: Sendable {
        var count: Int
        var firstID: String
        var lastID: String
        var rows: [ChatRow]
        var blocks: [ChatBlock]
    }

    /// `buildRows` + `ChatBlock.build`, redoing only what comes after `settled` when that still holds. An exchange
    /// starts at a prompt and nothing before it changes when items are appended — unless a tool result (or a tool's
    /// image) arrives for a call before it; then everything is built again.
    nonisolated static func rebuild(_ items: [Item], settled: SettledRows?) -> (rows: [ChatRow], blocks: [ChatBlock], settled: SettledRows?) {
        // Before the cut at the last message: a picture sent with it comes first in its line, and went to the rows
        // before it, already built (`picturesAfterTheirText`).
        let items = picturesAfterTheirText(items)
        func full() -> (rows: [ChatRow], blocks: [ChatBlock], settled: SettledRows?) {
            let rows = buildRows(items)
            return (rows, ChatBlock.build(rows), nil)
        }
        guard var cut = items.lastIndex(where: { $0.role == "user" && $0.kind == .text && $0.queued != true }) else { return full() }
        // Several text blocks and images from one input must stay on the same side of the incremental cut.
        while cut > 0, items[cut - 1].isUserInput, items[cut - 1].entry == items[cut].entry { cut -= 1 }
        guard cut > 0, standsAlone(items[cut...]) else { return full() }
        var base = settled
        // Still the same items up to it: an older page or a sorted-in item moves them.
        if let b = base, !(b.count <= cut && items.first?.id == b.firstID && items[b.count - 1].id == b.lastID && standsAlone(items[b.count...])) {
            base = nil
        }
        var rows = base?.rows ?? []
        var blocks = base?.blocks ?? []
        let start = base?.count ?? 0
        if start < cut {
            let more = buildRows(Array(items[start..<cut]))
            rows += more
            blocks += ChatBlock.build(more)
        }
        let next = SettledRows(count: cut, firstID: items[0].id, lastID: items[cut - 1].id, rows: rows, blocks: blocks)
        let tail = buildRows(Array(items[cut...]))
        return (rows + tail, blocks + ChatBlock.build(tail), next)
    }

    /// Every tool result and tool image in the slice belongs to a call in it.
    nonisolated private static func standsAlone(_ items: ArraySlice<Item>) -> Bool {
        var calls = Set<String>()
        for it in items {
            guard let tid = it.toolUseID else { continue }
            if it.kind == .toolUse { calls.insert(tid) } else if !calls.contains(tid) { return false }
        }
        return true
    }

    /// The streamed message `text` landed in the items: its live copy goes when its row comes. The rows are rebuilt off
    /// the main thread and published a pass later; cleared at once, the reply was gone for a frame, the chat ending
    /// at the message before it, then back (2026-11-04: 「流式输出的时候，滚动就不稳定了」).
    func endLive(_ text: String) {
        guard rebuildScheduled else { showLive(""); return }
        liveEnds = text
    }

    /// The streamed text as a delta gave it ("": cleared).
    func showLive(_ text: String) {
        if liveText.isEmpty, !text.isEmpty { liveSince = items.last?.seq ?? 0 }
        liveText = text
        liveStamp += 1
    }

    /// A batch was taken in (`apply`): if it holds the streamed message (`pending`: a newer text kept back to draw), its
    /// live copy goes once its row comes (`endLive`). True when it did: what was kept back is stale now.
    func landed(_ new: [Item], pending: String?) -> Bool {
        let live = pending ?? liveText
        guard new.contains(where: { Self.isLiveCopy($0, of: live, after: liveSince) }) else { return false }
        endLive(live)
        return true
    }

    /// `item` is the transcript line of the streamed `live` text: the reply (a text item), or the words written before a
    /// tool call, which Claude Code streams as text and records as a thinking block (#1966: their live copy never landed,
    /// and stayed at the end of the chat with its cursor). Written after line `since` (the newest when the text began):
    /// an earlier line that starts the same way (the same first word) is not it.
    nonisolated static func isLiveCopy(_ item: Item, of live: String, after since: Int = 0) -> Bool {
        !live.isEmpty && item.seq > since && item.role == "assistant" && (item.kind == .text || item.kind == .thinking)
            && (item.text ?? "").hasPrefix(live)
    }

    /// The streamed `live` text's line is among `items`, looked for back to the line it began after (`since`), the start of
    /// the turn (the user's message that began it) and at most `window` items: a turn of fifteen tool calls puts the
    /// words before them thirty lines back.
    nonisolated static func holdsLive(_ items: [Item], _ live: String, after since: Int = 0, window: Int = 400) -> Bool {
        for item in items.reversed().prefix(window) {
            if item.seq <= since { return false }
            if isLiveCopy(item, of: live, after: since) { return true }
            if item.role == "user", item.kind == .text, item.queued != true { return false }
        }
        return false
    }

    /// A subscription's backlog is in (a new subscription, one made again after the page was hidden, a reconnect that
    /// resumed): a clear that came while this chat was not listening is not sent again (deltas are not replayed), and the
    /// hub sends the live text right after the backlog only while a message is being generated. So what is drawn goes as
    /// a clear makes it go (`clearLive`); a delta that follows takes its place (#1966). An agent's is matched against the
    /// transcript instead (`agentStreamed`).
    func backlogArrived() {
        guard !liveIsAgents, !liveText.isEmpty else { return }
        clearLive(newest: nil)
    }

    /// The stream was cleared (`text`: the newest text streamed). The hub clears it when the turn ends or the next
    /// request starts, and the message's transcript line can come after that: cleared then, the reply was gone until it
    /// did, the chat ending at the message before it. The live copy stays until the item comes (`endLive`), 5 s at most
    /// (a turn cut off before its message was written).
    func clearLive(newest text: String?) {
        if let text, !text.isEmpty, text.hasPrefix(liveText) { showLive(text) }
        let shown = liveText
        guard !shown.isEmpty else { return }
        if Self.holdsLive(items, shown, after: liveSince) { return endLive(shown) }
        let stamp = liveStamp
        Task { @MainActor [weak self] in
            try? await Task.sleep(for: .seconds(5))
            // Anything set since (a new delta, even the same text sent again after a backlog) keeps it.
            guard let self, self.liveStamp == stamp, self.liveText == shown else { return }
            self.showLive("")
        }
    }

    // MARK: An agent's live reply

    /// A delta as the pacer lets it through (`AppModel`'s `.delta`): an agent's (`agents`) goes through
    /// `agentStreamed`; the headless assistant's is its whole reply so far, "" when it ends (`newest`: a newer text the
    /// pacer kept back).
    func streamed(_ text: String, agents: Bool, newest: String? = nil) {
        if agents {
            agentStreamed(text)
        } else if text.isEmpty {
            clearLive(newest: newest)
        } else if liveText.isEmpty, Self.holdsLive(items, text) {
            // Its line is in already: the text sent again after a backlog (its line came with it), or a last delta
            // after its copy landed. Drawn, it was taken for a new message and its line never ended it (#1966).
            return
        } else {
            showLive(text)
        }
    }

    /// An agent's delta as the pacer lets it through (docs/integrations/claude-integration.md "Live replies
    /// (MessageDisplay)"): its message as Claude shows it, less what the node's transcript had; "" once the transcript has
    /// it or its turn ended. What the newest items hold of it is taken off too (`LiveText.unlandedLive`).
    func agentStreamed(_ text: String) {
        if !liveIsAgents { liveIsAgents = true }
        agentLive = text
        showAgentLive()
    }

    /// The newest items changed (a push, the latest page, a pull: `apply`, `replace`): an agent's live text is checked
    /// against them again. An older page or an opened fold (`prepend`, `unfold`) never is the reply being written, and
    /// leaves it as it is.
    private func newestArrived() {
        guard liveIsAgents, !agentLive.isEmpty || !liveText.isEmpty else { return }
        showAgentLive()
    }

    /// The live text drops at once: the socket started over, or (an agent's) the chat's subscription ended or started
    /// again (`AppModel.forgetAgentLive`); the hub sends the current one again after its catch-up.
    func dropLive() {
        showLive("")
        agentLive = ""
        agentLiveDue = false
    }

    private func showAgentLive() {
        guard !rebuildScheduled else { agentLiveDue = true; return } // with the rows it waits for
        let want = agentLiveShown()
        if want != liveText { showLive(want) }
    }

    /// What of the agent's live message to draw: what the newest items do not hold.
    private func agentLiveShown() -> String {
        agentLive.isEmpty ? "" : LiveText.unlandedLive(agentLive, liveTail())
    }

    /// The items an agent's live text is checked against: the newest ones, from the latest exchange's opening on (the
    /// check never looks further back) and never before the latest page from the end (older pages are not the reply
    /// being written), with the work of folds opened here folded again, as the page brought it, so that opening a
    /// fold never changes what the live text is.
    private func liveTail() -> [Item] {
        let floor = newestFrom.flatMap { f in items.firstIndex { ($0.seq, $0.part) >= f } } ?? items.startIndex
        let start = max(floor, items.lastIndex { LiveText.opens($0) } ?? floor)
        guard !opened.isEmpty else { return Array(items[start...]) }
        let folds = opened.values.filter { $0.fold != nil }
        var out: [Item] = []
        for it in items[start...] {
            if let f = folds.first(where: { $0.folds(it) }) {
                if out.last?.id != f.id { out.append(f) }
            } else {
                out.append(it)
            }
        }
        return out
    }

    /// Coalesces events into one background worker; only a current snapshot is published on the main actor.
    private func scheduleRebuild() {
        rebuildRevision += 1
        guard !rebuildScheduled else { return }
        rebuildScheduled = true
        Task { [weak self] in
            guard let self else { return }
            while true {
                let revision = self.rebuildRevision
                let items = self.items, settled = self.settled
                let (built, images, tasks) = await Task.detached(priority: .userInitiated) {
                    (ChatStore.rebuild(items, settled: settled), items.compactMap(\.image), TaskIndex.build(items))
                }.value
                guard revision == self.rebuildRevision else { continue }
                self.rows = built.rows
                self.blocks = built.blocks
                // The sent messages' rows are in: their local copies go in the same pass.
                if !self.arriving.isEmpty { self.arriving = [] }
                // The streamed message's row is in: its live copy goes in the same pass (a newer one that started
                // meanwhile stays).
                if let ended = self.liveEnds {
                    self.liveEnds = nil
                    if ended.hasPrefix(self.liveText) { self.showLive("") }
                }
                // An agent's live text that waited for these rows (`showAgentLive`): in the same pass.
                if self.agentLiveDue {
                    self.agentLiveDue = false
                    let want = self.agentLiveShown()
                    if want != self.liveText { self.showLive(want) }
                }
                // No item changed since the snapshot (same revision): these blocks hold everything loaded so far.
                if self.backlogBuilt != self.loadedOnce { self.backlogBuilt = self.loadedOnce }
                self.settled = built.settled
                if images.map(\.identity) != self.images.map(\.identity) { self.images = images }
                if tasks != self.tasks { self.tasks = tasks }
                self.version += 1
                self.rebuildScheduled = false
                return
            }
        }
    }

    @ObservationIgnored private var saveTask: Task<Void, Never>?

    /// Writes the last two exchanges to the disk cache, a moment after the latest change.
    private func scheduleSave() {
        saveTask?.cancel()
        let key = key, cacheFolder = cacheFolder
        saveTask = Task { [weak self] in
            try? await Task.sleep(for: .seconds(2))
            guard !Task.isCancelled, let self else { return }
            let tail = ChatCache.tail(self.items, turns: 2)
            let more = self.hasMore || tail.count < self.items.count
            let history = self.history, covered = self.covered
            Task.detached(priority: .utility) { ChatCache.save(key, items: tail, hasMore: more, history: history, covered: covered, in: cacheFolder) }
        }
    }

    /// Lazy images learn where to fetch their bytes from, and so do those a fold says its work looked at. Every way
    /// items come in (a page, a push, the disk cache, the background sync) passes here, so the view feedback goes too.
    nonisolated static func withImageRefs(_ items: [Item], _ key: SessionKey) -> [Item] {
        let items = withoutViewFeedback(items)
        guard items.contains(where: { $0.image?.lazy == true || $0.fold?.images?.isEmpty == false }) else { return items }
        return items.map { it in
            var it = it
            if it.image?.lazy == true { it.image?.ref = ImageRef.make(key, item: it.id) }
            if let images = it.fold?.images {
                it.fold?.images = images.map { im in
                    var im = im
                    if let id = im.itemID { im.ref = ImageRef.make(key, item: id) }
                    return im
                }
            }
            return it
        }
    }

    /// Starts over from the latest page: what came in between was too much to fetch, and a hole would never fill.
    func replace(with latest: [Item], hasMore: Bool, span: ItemSpan = ItemSpan()) {
        arrivedAt = .now
        defer { newestArrived() }
        // Pushes that came in while the page was on its way, past what it holds, stay (the next push finds the hole
        // between, if any). Not after another history: those seqs belong to the old file.
        let keep = span.history == nil || span.history == history ? items.filter { $0.seq > (span.to ?? .max) } : []
        resets += 1
        items = []
        ids = []
        opened = [:]
        newestFrom = latest.first.map { ($0.seq, $0.part) }
        self.hasMore = hasMore
        if let h = span.history { history = h }
        merge(latest)
        merge(keep)
        covered = span.to ?? lastSeq
        scheduleRebuild() // also when the page is empty
    }

    /// The node's `<persona-view-errors>` feedback after the assistant's snapshot (its last rich views failed their
    /// check) is for the model, not the user's words: an older hub (#4315) gave it as a user item of its own, the line's
    /// block before the message, or at the start of the message, and the bubble showed its raw tags. Taken out of user
    /// text; an item left with nothing goes.
    nonisolated static func withoutViewFeedback(_ items: [Item]) -> [Item] {
        guard items.contains(where: { $0.role == "user" && $0.kind == .text && ($0.text ?? "").contains("<persona-view-errors") }) else { return items }
        return items.compactMap { it in
            guard it.role == "user", it.kind == .text, let text = it.text, text.contains("<persona-view-errors") else { return it }
            let left = text.replacingOccurrences(of: #"<persona-view-errors\b[^>]*>[\s\S]*?</persona-view-errors>\s*"#,
                                                 with: "", options: .regularExpression)
            guard !left.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return nil }
            var it = it
            it.text = left
            return it
        }
    }

    /// Items in transcript order: by line, then by block within the line (ids end in "#<block>"; "#10" after "#2").
    nonisolated static func ordered(_ items: [Item]) -> [Item] {
        items.sorted { ($0.seq, $0.part) < ($1.seq, $1.part) }
    }

    func prepend(_ older: [Item], hasMore: Bool) {
        self.hasMore = hasMore
        merge(older)
    }

    /// Live preview: the lines Claude is currently printing, cut above the input box.
    var livePreview: [String] { ScreenParser.activity(screen) }
    var spinnerLine: String? { ScreenParser.spinner(screen) }

    /// Pictures the user sent come after the text of their message. claude -p writes a message sent to Persona with its
    /// pictures first (image blocks, then the text), where a picture pasted in an Agents chat comes after the text:
    /// first, a picture went to the exchange before its own message (into its work, out of sight) and the chat no
    /// longer showed what was sent once the transcript's copy replaced the one being sent.
    nonisolated static func picturesAfterTheirText(_ items: [Item]) -> [Item] {
        func isPhoto(_ it: Item) -> Bool { it.kind == .image && it.role == "user" && it.toolUseID == nil }
        guard items.contains(where: isPhoto) else { return items }
        var out: [Item] = []
        out.reserveCapacity(items.count)
        var held: [Item] = [] // the pictures of one line, waiting for its text
        for it in items {
            if isPhoto(it) {
                if let first = held.first, first.seq != it.seq { out += held; held.removeAll() }
                held.append(it)
                continue
            }
            if let first = held.first {
                if it.kind == .text, it.role == "user", it.seq == first.seq {
                    out.append(it) // the message, then its pictures
                    out += held
                    held.removeAll()
                    continue
                }
                out += held
                held.removeAll()
            }
            out.append(it)
        }
        return out + held
    }

    /// A compaction is one divider: Claude Code writes the boundary (the hub's 「上下文已压缩」, Codex's too), then the summary
    /// it carries on with (「已从压缩的上下文继续」), and after an idle one a note about it ("Compacted while idle…").
    /// The boundary keeps the others as its notes; they are no rows of their own. A summary whose boundary is on an
    /// older page stands in for it.
    nonisolated static let compactedText = "上下文已压缩" // i18n-ignore: matches the hub's notice
    nonisolated static let continuedText = "已从压缩的上下文继续" // i18n-ignore: matches the hub's notice
    nonisolated static func compactions(_ items: [Item]) -> [Item] {
        func isNotice(_ it: Item, _ text: String) -> Bool { it.kind == .notice && it.task == nil && it.wake != true && it.text == text }
        func isIdleNote(_ it: Item) -> Bool { it.kind == .notice && (it.text ?? "").hasPrefix("Compacted while idle") }
        guard items.contains(where: { $0.compact != nil || isNotice($0, compactedText) || isNotice($0, continuedText) }) else { return items }
        var out: [Item] = []
        out.reserveCapacity(items.count)
        var open: Int? // the boundary in `out` that the notes after it still join
        for it in items {
            if it.compact != nil || isNotice(it, compactedText) {
                var b = it
                var c = it.compact ?? Compaction()
                c.notes = (c.notes ?? []) + [it.text ?? compactedText]
                b.compact = c
                out.append(b)
                open = out.count - 1
                continue
            }
            if isNotice(it, continuedText) || isIdleNote(it) {
                if let i = open {
                    out[i].compact?.notes?.append(it.text ?? "")
                    if isIdleNote(it) { out[i].compact?.trigger = "idle" }
                    continue
                }
                if isNotice(it, continuedText) { // its boundary is not here
                    var b = it
                    b.compact = Compaction(notes: [it.text ?? continuedText])
                    out.append(b)
                    open = out.count - 1
                    continue
                }
            }
            open = nil // anything else ends it
            out.append(it)
        }
        return out
    }

    nonisolated static func buildRows(_ items: [Item]) -> [ChatRow] {
        let items = compactions(picturesAfterTheirText(items))
        var results: [String: [Item]] = [:]
        var images: [String: [ImagePayload]] = [:]
        for it in items {
            guard let tid = it.toolUseID else { continue }
            if it.kind == .toolResult { results[tid, default: []].append(it) }
            if it.kind == .image, let img = it.image { images[tid, default: []].append(img) }
        }
        var rows: [ChatRow] = []
        var run: [ToolStep] = []
        var pendingThoughts: [Item] = []

        func flushThoughts(asRows: Bool) {
            if asRows { rows.append(contentsOf: pendingThoughts.map { .thinking($0) }) }
            else { run.append(contentsOf: pendingThoughts.map { .thought($0) }) }
            pendingThoughts.removeAll()
        }
        func flushRun() {
            if !run.isEmpty { rows.append(.tools(run)); run.removeAll() }
        }

        for it in items {
            switch it.kind {
            case .toolUse:
                flushThoughts(asRows: run.isEmpty)
                let deliveries = it.toolUseID.flatMap { results[$0] } ?? []
                let agent = it.toolName == "Agent" || it.toolName == "Task"
                var call = ToolCall(use: it, result: deliveries.last, results: agent ? deliveries : [],
                                    agentStatus: deliveries.last(where: { $0.task?.kind == "agent" }).map { $0.task?.status ?? "" })
                call.images = it.toolUseID.flatMap { images[$0] } ?? []
                run.append(.call(call))
            case .thinking:
                pendingThoughts.append(it)
            case .fold:
                flushThoughts(asRows: run.isEmpty)
                run.append(.folded(it))
            case .toolResult:
                if it.task?.kind == "agent", it.wake == true {
                    flushThoughts(asRows: run.isEmpty)
                    flushRun()
                    rows.append(.notice(it))
                }
                continue
            case .image where it.toolUseID != nil:
                continue
            default:
                if run.isEmpty { flushThoughts(asRows: true) } else { flushThoughts(asRows: false); flushRun() }
                switch it.kind {
                case .text: rows.append(it.role == "user" ? .user(it) : .assistant(it))
                case .notice: rows.append(.notice(it))
                case .recap: rows.append(.recap(it))
                case .image: rows.append(.image(it))
                default: break
                }
            }
        }
        if !run.isEmpty {
            flushThoughts(asRows: false)
            flushRun()
        } else {
            flushThoughts(asRows: true)
        }
        return rows
    }
}

/// Extracts useful bits from Claude Code's terminal screen.
nonisolated enum ScreenParser {
    private static let spinnerGlyphs: Set<Character> = ["✻", "✶", "✳", "✢", "✽", "·", "*", "⏺", "◐", "◓", "◑", "◒"]

    /// "Cogitating… (12s · ↓ 3.2k tokens)" from the status line above the input box.
    static func spinner(_ screen: String) -> String? {
        for line in screen.split(separator: "\n", omittingEmptySubsequences: true).reversed() {
            let t = line.trimmingCharacters(in: .whitespaces)
            guard let first = t.first, spinnerGlyphs.contains(first), t.contains("…") else { continue }
            var s = String(t.dropFirst()).trimmingCharacters(in: .whitespaces)
            s = s.replacingOccurrences(of: " · esc to interrupt", with: "").replacingOccurrences(of: "esc to interrupt", with: "")
            return s.replacingOccurrences(of: "()", with: "").trimmingCharacters(in: .whitespaces)
        }
        return nil
    }

    /// The last few content lines above the prompt box (what is streaming right now).
    static func activity(_ screen: String, max: Int = 8) -> [String] {
        var lines = screen.components(separatedBy: "\n")
        // Cut at the first horizontal rule from the bottom that starts the input box.
        if let idx = lines.lastIndex(where: { isRule($0) }), let top = lines[..<idx].lastIndex(where: { isRule($0) }) {
            lines = Array(lines[..<top])
        } else if let idx = lines.lastIndex(where: { isRule($0) }) {
            lines = Array(lines[..<idx])
        }
        let content = lines
            .map { $0.replacingOccurrences(of: "\u{00a0}", with: " ") }
            .filter { line in
                let t = line.trimmingCharacters(in: .whitespaces)
                return !t.isEmpty && spinner(t) == nil
            }
        return Array(content.suffix(max))
    }

    /// Terminal lines as text to read: the tree / box glyphs Claude Code draws in front of tool output (⎿ └ │ ├) and
    /// the indent go, blank lines too; at most `max` lines.
    static func plainLines(_ text: String, max: Int) -> String {
        let glyphs = CharacterSet(charactersIn: "⎿└├│┃╰╭╮╯┌┐┘ \t\u{00a0}")
        let lines = text.components(separatedBy: "\n")
            .map { String($0.unicodeScalars.drop { glyphs.contains($0) }) }
            .map { $0.trimmingCharacters(in: .whitespaces) }
            .filter { !$0.isEmpty }
        return lines.prefix(max).joined(separator: "\n")
    }

    /// What a terminal with no dialog to read waits on: the last tool call on the screen ("⏺ Bash(git rebase …)")
    /// and the last line of its output, as plain text; nil when there is neither.
    static func waitingOn(_ screen: String) -> String? {
        let lines = activity(screen, max: 40)
        guard let call = lines.lastIndex(where: { $0.trimmingCharacters(in: .whitespaces).hasPrefix("⏺") }) else {
            let tail = plainLines(lines.suffix(2).joined(separator: "\n"), max: 2)
            return tail.isEmpty ? nil : tail
        }
        let head = lines[call].trimmingCharacters(in: .whitespaces).dropFirst().trimmingCharacters(in: .whitespaces)
        let last = plainLines(lines[(call + 1)...].suffix(1).joined(separator: "\n"), max: 1)
        return last.isEmpty ? head : head + "\n" + last
    }

    static func isRule(_ line: String) -> Bool {
        let t = line.trimmingCharacters(in: .whitespaces)
        return t.count > 20 && t.allSatisfy { $0 == "─" || $0 == "━" || $0 == "-" }
    }

    /// A choice the terminal is waiting on (AskUserQuestion without the hook, a permission prompt…), read off the
    /// screen: its header chip, the question, numbered options with their descriptions.
    struct Dialog: Equatable, Sendable {
        enum Kind: Equatable, Sendable { case plain, typeSomething, chat }
        struct Option: Equatable, Sendable {
            var key: String
            var label: String
            var detail: String?
            var kind: Kind = .plain
            /// Ticked at the computer (a multi-select box), or an answered question's pick.
            var checked = false
            /// What is typed into the free-text field.
            var text: String?
        }
        /// A step of a multi-question AskUserQuestion: the TUI's tab bar 「←  ☐ 审核策略  ✔ 推送代码  ✔ Submit  →」.
        struct Step: Equatable, Sendable {
            var label: String
            var done: Bool
        }
        var header: String?
        var question: String
        /// Lines above the question that belong to it (the command a permission prompt is about).
        var detail: String?
        var options: [Option]
        /// The questions of a multi-question dialog, from its tab bar (taken out of `detail`, where it read as text).
        var steps: [Step] = []
        /// A multi-select question (the hub's reading): boxes sent together by the `submit` row ("Submit" / "Next").
        var multi = false
        var submit: String?
        /// The tab that submits every answer ("Review your answers"); incomplete: a question has none yet.
        var review = false
        var incomplete = false

        init(header: String?, question: String, detail: String?, options: [Option]) {
            self.header = header
            self.question = question
            self.options = options
            let (steps, rest) = ScreenParser.steps(in: detail)
            self.steps = steps
            self.detail = rest
        }
    }

    /// The tab bar of a multi-question dialog out of the lines above the question: its steps (Submit dropped), and the
    /// lines left (nil when none).
    static func steps(in detail: String?) -> ([Dialog.Step], String?) {
        guard let detail, !detail.isEmpty else { return ([], nil) }
        let marks: Set<Character> = ["☐", "□", "☒", "✔", "◻", "■", "✓"]
        var steps: [Dialog.Step] = []
        var rest: [String] = []
        for line in detail.components(separatedBy: "\n") {
            let count = line.filter { marks.contains($0) }.count
            guard count >= 2 || (count >= 1 && (line.contains("←") || line.contains("→"))) else { rest.append(line); continue }
            var label = "", mark: Character?
            func flush() {
                let t = label.trimmingCharacters(in: .whitespaces)
                if let m = mark, !t.isEmpty, t.lowercased() != "submit" { steps.append(.init(label: t, done: m == "☒" || m == "✔" || m == "■" || m == "✓")) }
                label = ""
            }
            for c in line where c != "←" && c != "→" {
                if marks.contains(c) { flush(); mark = c } else { label.append(c) }
            }
            flush()
        }
        return (steps, rest.isEmpty ? nil : rest.joined(separator: "\n"))
    }

    /// The dialog at the bottom of the screen, or nil when there is none (plain options or none at all).
    static func dialog(_ screen: String) -> Dialog? {
        let lines = screen.components(separatedBy: "\n")
        // Numbered rows: (line index, number, label, indent of the label).
        var rows: [(i: Int, n: Int, label: String, indent: Int)] = []
        for (i, raw) in lines.enumerated() {
            var t = Substring(raw)
            let lead = t.prefix { $0 == " " }.count
            t = t.dropFirst(lead)
            var indent = lead
            if t.hasPrefix("❯") || t.hasPrefix(">") {
                t = t.dropFirst()
                let sp = t.prefix { $0 == " " }.count
                t = t.dropFirst(sp)
                indent += 1 + sp
            }
            guard let dot = t.firstIndex(of: "."), let n = Int(t[..<dot]), (1...9).contains(n) else { continue }
            let after = t[t.index(after: dot)...]
            guard after.hasPrefix(" ") else { continue }
            let label = after.trimmingCharacters(in: .whitespaces)
            guard !label.isEmpty else { continue }
            rows.append((i, n, label, indent + t.distance(from: t.startIndex, to: dot) + 2))
        }
        // The last run 1, 2, 3… is the live dialog.
        guard let start = rows.lastIndex(where: { $0.n == 1 }) else { return nil }
        var block = [rows[start]]
        for r in rows[(start + 1)...] where r.n == block.last!.n + 1 { block.append(r) }
        guard block.count >= 2 else { return nil }

        var options: [Dialog.Option] = []
        for (k, r) in block.enumerated() {
            let end = k + 1 < block.count ? block[k + 1].i : min(lines.count, r.i + 6)
            var detail: [String] = []
            for line in lines[(r.i + 1)..<end] {
                let lead = line.prefix { $0 == " " }.count
                let t = line.trimmingCharacters(in: .whitespaces)
                if t.isEmpty || isRule(t) || lead < r.indent - 1 { if !detail.isEmpty { break } else { continue } }
                detail.append(t)
            }
            let lower = r.label.lowercased()
            let kind: Dialog.Kind = lower.hasPrefix("type something") ? .typeSomething : lower.hasPrefix("chat about this") ? .chat : .plain
            options.append(Dialog.Option(key: String(r.n), label: r.label, detail: detail.isEmpty ? nil : joinWrapped(detail), kind: kind))
        }

        // Above the options, up to the rule that opens the dialog.
        let first = block[0].i
        var top = 0
        for i in stride(from: first - 1, through: 0, by: -1) where isRule(lines[i]) { top = i + 1; break }
        var header: String?
        var quoted: [String] = []
        var plain: [String] = []
        for line in lines[top..<first] {
            let t = line.trimmingCharacters(in: .whitespaces)
            if t.isEmpty { continue }
            if let c = t.first, "☐□☒✔◻■".contains(c) {
                header = t.dropFirst().trimmingCharacters(in: .whitespaces)
            } else if t.hasPrefix("│") || t.hasPrefix("|") {
                quoted.append(t.dropFirst().trimmingCharacters(in: .whitespaces))
            } else {
                plain.append(t)
            }
        }
        let question: String
        var detail: String?
        if !quoted.isEmpty {
            question = joinWrapped(quoted)
            if !plain.isEmpty { detail = plain.joined(separator: "\n") }
        } else if let last = plain.last {
            question = last
            if plain.count > 1 { detail = plain.dropLast().joined(separator: "\n") }
        } else {
            question = header ?? ""
            if header != nil { header = nil }
        }
        return Dialog(header: header, question: question, detail: detail, options: options)
    }

    /// Rows the terminal wrapped, back into one text: no space where a row ended inside CJK text.
    static func joinWrapped(_ rows: [String]) -> String {
        var out = ""
        for r in rows {
            if let last = out.last, let next = r.first, !(last.isCJK || next.isCJK) { out += " " }
            out += r
        }
        return out
    }

    /// Numbered options on a TUI dialog ("❯ 1. Yes", "2. No") → (number, label).
    static func options(_ screen: String) -> [(key: String, label: String, selected: Bool)] {
        var out: [(String, String, Bool)] = []
        for raw in screen.components(separatedBy: "\n") {
            var t = raw.trimmingCharacters(in: .whitespaces)
            var selected = false
            if t.hasPrefix("❯") { selected = true; t = String(t.dropFirst()).trimmingCharacters(in: .whitespaces) }
            guard let dot = t.firstIndex(of: "."), let n = Int(t[..<dot]), n >= 1, n <= 9 else { continue }
            let label = t[t.index(after: dot)...].trimmingCharacters(in: .whitespaces)
            guard !label.isEmpty else { continue }
            out.append((String(n), label, selected))
        }
        // Keep only the trailing contiguous 1…N block (the active dialog).
        if let lastOne = out.lastIndex(where: { $0.0 == "1" }) { out = Array(out[lastOne...]) }
        return out
    }
}

/// On-disk copy of each conversation's last exchanges (Caches/chats), so opening or switching to a chat never
/// shows a blank page while the hub catches up.
nonisolated enum ChatCache {
    private struct Entry: Codable {
        var items: [Item]
        var hasMore: Bool
        var history: String?
        var covered: Int?
    }
    private static let maxBytes = 6 << 20
    /// The caches folder holding the copies. v2: items carry `wake`; older copies would keep the merged turns until
    /// they scrolled out. v3: notices carry `task` (a page's copy of an item never replaces the cached one: old
    /// notices stayed centered blocks). v4: Codex unknown events are retained and external sources have distinct identities.
    /// v5: several groups of hubs (docs/protocol/multi-hub.md): the default group's copies at the top, another group's in
    /// `group-<id>/`. v6: a Feishu /agent request's first message is rewritten by the hub (its instructions in a
    /// <persona-context> block, #876), so v5's copies would keep showing the old text: they are dropped, not moved
    /// (`dropOlder`). v7: items carry `client_msg_id`, the message sent from the phone they came from (#968).
    /// v8: folds carry the images their work looked at (`Fold.images`); v7's folds had none, and their turns no strip.
    /// v9: user lines Persona typed in for the model carry `injected` (docs/protocol/protocol.md「Messages injected by Persona」, #2358).
    /// v10: a background workflow's end notice is kind `workflow` (#3731) and the hub gives it in the App's language
    /// (#5511); v9's copies would keep its Chinese text for English and Japanese users. v11: folds carry the task
    /// lists their calls left (`Fold.tasks`), the chat's plan while its TodoWrite is folded (#5602).
    /// v12: relayed and automated assistant input retains additive source metadata.
    /// v13: cached tails count user questions, not automatic supervision; discard the old shallow tails.
    /// v14: injected supervision levels and folded promotion are retained, including client evidence on replaced folds.
    /// v16: tool results carry `outcome` (docs/protocol/protocol.md "Tool outcomes", #4303); older copies have none, and
    /// their denied or interrupted calls would stay red.
    /// Bump it whenever the item structure or the text the hub gives an item changes.
    static let folder = "chats-v16"
    /// The folder before, removed on the first launch with this one.
    private static let previous = "chats-v15"

    /// Once per launch, before the first copy is read or written (a static let runs once, on whichever thread asks first).
    private static let dropOlder: Void = {
        guard let base = FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask).first else { return }
        try? FileManager.default.removeItem(at: base.appending(path: previous))
    }()

    /// Whether a caches folder is one of ChatCache's, this version or an older one ("chats", "chats-v<N>"): what
    /// Settings › Storage counts under 「Agent 对话」.
    static func isFolder(_ name: String) -> Bool {
        guard name != "chats" else { return true }
        guard name.hasPrefix("chats-v") else { return false }
        let version = name.dropFirst("chats-v".count)
        return !version.isEmpty && version.allSatisfy { $0.isASCII && $0.isNumber }
    }

    /// The default group's copies where they always were; another group's in a folder of its own (docs/protocol/multi-hub.md:
    /// the same machine and session ids can be in two groups).
    private static func url(_ key: SessionKey, in root: URL?) -> URL? {
        guard let dir = directory(key.group, in: root) else { return nil }
        let safe = "\(key.machineID)_\(key.sessionID)".map { $0.isLetter || $0.isNumber || $0 == "-" || $0 == "_" ? $0 : "_" }
        return dir.appending(path: String(safe) + ".json")
    }

    private static func directory(_ group: String, in root: URL?) -> URL? {
        // An isolated model owns its cache too; late saves must never target another model's disk.
        if root == nil { _ = dropOlder }
        guard let base = root ?? FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask).first else { return nil }
        let dir = base.appending(path: folder)
        guard !group.isEmpty else { return dir }
        let safe = group.map { $0.isLetter || $0.isNumber || $0 == "-" || $0 == "_" ? $0 : "_" }
        return dir.appending(path: "group-" + String(safe))
    }

    /// A group that is gone: its copies go too (the default group's: the files at the top, not the other groups' folders).
    static func removeGroup(_ group: String, in root: URL? = nil) {
        guard let dir = directory(group, in: root) else { return }
        guard group.isEmpty else { try? FileManager.default.removeItem(at: dir); return }
        let files = (try? FileManager.default.contentsOfDirectory(at: dir, includingPropertiesForKeys: nil)) ?? []
        for f in files where f.pathExtension == "json" { try? FileManager.default.removeItem(at: f) }
    }

    static func load(_ key: SessionKey, in root: URL? = nil) -> (items: [Item], hasMore: Bool, history: String?, covered: Int?)? {
        guard let url = url(key, in: root), let data = try? Data(contentsOf: url),
              let e = try? JSON.decoder.decode(Entry.self, from: data) else { return nil }
        return (e.items, e.hasMore, e.history, e.covered)
    }

    static func removeAll() {
        _ = dropOlder
        guard let base = FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask).first else { return }
        try? FileManager.default.removeItem(at: base.appending(path: folder))
    }

    static func remove(_ key: SessionKey, in root: URL? = nil) {
        guard let url = url(key, in: root) else { return }
        try? FileManager.default.removeItem(at: url)
    }

    static func save(_ key: SessionKey, items: [Item], hasMore: Bool, history: String? = nil, covered: Int? = nil, in root: URL? = nil) {
        guard let url = url(key, in: root), !items.isEmpty else { return }
        var entry = Entry(items: items, hasMore: hasMore, history: history, covered: covered)
        guard var data = try? JSON.encoder.encode(entry) else { return }
        if data.count > maxBytes { // screenshots make turns huge: keep the words, drop the pictures
            entry.items = items.filter { $0.kind != .image }
            guard let slim = try? JSON.encoder.encode(entry) else { return }
            data = slim
        }
        try? FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
        try? data.write(to: url, options: .atomic)
    }

    /// Items from the `turns`-th last user question on; automatic prompts do not consume the budget (never half a transcript line: what a page before
    /// its first seq would bring back leaves out the rest of that line).
    static func tail(_ items: [Item], turns: Int) -> [Item] {
        var seen = 0
        var start = 0
        for i in items.indices.reversed() where items[i].role == "user" && items[i].kind == .text
            && items[i].queued != true && items[i].wake != true && Injected.of(items[i]) == nil {
            seen += 1
            if seen == turns { start = i; break }
        }
        guard start > 0 else { return items }
        var launches: [String: Int] = [:]
        var question = 0
        for i in items.indices {
            let item = items[i]
            if item.isUserInput, item.queued != true, Injected.of(item) == nil { question = i }
            if item.kind == .toolUse, item.toolName == "Agent" || item.toolName == "Task", let id = item.toolUseID {
                launches[id] = question
            }
        }
        var i = items.count - 1
        while i >= start {
            let item = items[i]
            if item.task?.kind == "agent", let call = item.toolUseID ?? item.task?.toolUseID, let launch = launches[call] {
                start = min(start, launch)
            }
            i -= 1
        }
        while start > 0 && items[start - 1].seq == items[start].seq { start -= 1 }
        return Array(items[start...])
    }
}

/// A message on its way: shown at once, the request runs in the background.
nonisolated struct PendingMessage: Identifiable, Hashable, Codable, Sendable {
    /// waiting: no network or no hub; it goes by itself once the app is connected and caught up (AppModel.flushOutbox).
    enum State: Hashable, Codable, Sendable { case sending, sent, failed, waiting }
    /// An Agent hand-off's filing intent, restored with the outbox so delivery after an app relaunch still files the item.
    struct InboxHandOff: Hashable, Codable, Sendable {
        let itemID: String
        let manager: String?
        var filed = false
    }
    var id = UUID()
    var text: String
    var images: [ImagePayload]
    var state: State = .sending
    /// Why the last send failed, kept with the message so a caller waiting through outbox retries can show the reason.
    var failure: String?
    var inboxHandOff: InboxHandOff?
    /// When it was written, in whole seconds (it survives the outbox's JSON as it is); nil in outboxes saved before this
    /// was kept.
    var createdAt: Date? = Date(timeIntervalSince1970: Date().timeIntervalSince1970.rounded(.down))
    /// The send broke off while the message was being typed (delivered: unknown): it may be on the computer. A resend
    /// the user asks for goes with a new id, or the hub would answer it from the first one.
    var mayHaveArrived: Bool?
    /// The hub refused it with nothing typed (delivered: none: a draft in the computer's input box, a dialog…) and
    /// answers its id with that refusal from then on: Resend and Clear and Send go with a new id (#6425).
    var refused: Bool?
    /// When its sends started coming back waiting while its group's link was up (AppModel.settleSend, `OutboxWait`);
    /// in memory only: a relaunch starts the run again.
    var stalledSince: Date?

    private enum CodingKeys: String, CodingKey { case id, text, images, state, failure, inboxHandOff, createdAt, mayHaveArrived, refused }

    /// Only on this phone as far as it knows: failed, or waiting for the network. Deleting it (its long press, its mark's
    /// menu) drops the bubble and its outbox entry and sends nothing (#4626). One being sent may be arriving and a sent
    /// one is the hub's: neither can be deleted.
    var onlyHere: Bool { state == .failed || state == .waiting }

    /// Comparable form: what the transcript keeps of it — the phone's image note, pasted wrappers and Claude's
    /// "[Image #21]" placeholders removed, whitespace ignored, and a context or file block read under either
    /// generation's tag name (a hub from before the rename keeps what `HubNaming.outbound` sent it).
    private static let normalized = TextMemo<String>(limit: 512)

    static func normalize(_ s: String) -> String {
        normalized(s) { s in
            var t = s.replacingOccurrences(of: #"(<\s*/?\s*)"# + HubNaming.productPattern + #"-(context|file)"#,
                                           with: "$1persona-$2", options: .regularExpression)
            for pattern in [#"<attached_images>[\s\S]*?</attached_images>"#, #"<[\\/]*pasted_content[^>]*>"#, #"\[Image #\d+\]"#, #"\s+"#] {
                t = t.replacingOccurrences(of: pattern, with: "", options: .regularExpression)
            }
            return t
        }
    }

    /// This transcript item is the message. An item the hub tied to a message (`Item.clientMsgID`) is the one with that
    /// id, whatever its role, kind or words (a rewritten text, the notice `/compact` became), and no other: two "Continue"
    /// each take their own bubble. One it tied to none: a user message that arrived after it was written (two minutes'
    /// slack for the computer's clock), with its text — when the page cut a long one, its start. An older message with
    /// the same words (a second "Continue") is not it: the bubble of one never sent stayed gone.
    static func arrived(_ p: PendingMessage, as item: Item) -> Bool {
        if let tied = tied(item, to: p.id) { return tied }
        guard item.role == "user", item.kind == .text, Injected.of(item) == nil, MessageSource.of(item) == nil else { return false }
        if let created = p.createdAt, let ts = item.ts, ts < created.addingTimeInterval(-120) { return false }
        guard item.fullSize != nil else { return same(item.text ?? "", p.text) }
        let start = normalize(item.cutText), whole = normalize(p.text)
        return start.count >= 8 && whole.hasPrefix(start)
    }

    /// Whether the hub says this item came from the message with this id; nil when it says nothing (older hubs, or
    /// the node could not tie it) and only the words can tell. The app sends `uuidString` (upper case): compared
    /// ignoring case.
    static func tied(_ item: Item, to id: UUID) -> Bool? {
        item.clientMsgID.map { $0.caseInsensitiveCompare(id.uuidString) == .orderedSame }
    }

    /// The transcript's copy of a message sent from here (equal once normalized, or one containing the other:
    /// Claude adds placeholders and the phone adds notes). Containing only counts for longer texts: a pending "ok"
    /// was taken for any message with ok in it.
    static func same(_ a: String, _ b: String) -> Bool {
        let x = normalize(a), y = normalize(b)
        guard !x.isEmpty, !y.isEmpty else { return false }
        return x == y || (min(x.count, y.count) >= 8 && (x.contains(y) || y.contains(x)))
    }
}

nonisolated private extension Character {
    var isCJK: Bool {
        guard let v = unicodeScalars.first?.value else { return false }
        return (0x2E80...0x9FFF).contains(v) || (0xF900...0xFAFF).contains(v) || (0xFF00...0xFFEF).contains(v) || (0x3000...0x303F).contains(v)
    }
}
