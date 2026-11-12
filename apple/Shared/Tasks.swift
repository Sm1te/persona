import Foundation

// Tasks dispatched from an assistant conversation (docs/assistant/unified-entry.md, #930): a plan the assistant handed the user,
// the todos it opened (one per task), and the items the hub inserted into the conversation (its feed). The hub's
// shapes are in docs/protocol/protocol.md「Tasks and the chat feed」.

/// One task of a plan, as the assistant proposed it (and the user may have edited it).
nonisolated struct PlanTask: Codable, Hashable, Sendable {
    var title: String
    var prompt: String
    var machine: String
    var cwd: String
    var launcher: String?
    var model: String?
    var permission: String?
    /// Claude Code's ultracode for this task (#12): the assistant's choice; on start, true / false replaces it and left
    /// out keeps it (PlanUltracode.swift).
    var ultracode: Bool?
    var readonly: Bool?
    var why: String?
    /// Set once started: the todo the task is, and why its agent did not start.
    var todoID: String?
    var error: String?

    enum CodingKeys: String, CodingKey {
        case title, prompt, machine, cwd, launcher, model, permission, ultracode, readonly, why, error
        case todoID = "todo_id"
    }

    var isReadOnly: Bool { readonly ?? false }
    /// The directory's last part, which is how the user names a project.
    var project: String { (cwd as NSString).lastPathComponent }
}

nonisolated struct TaskPlan: Codable, Hashable, Identifiable, Sendable {
    enum State: String, Codable, Sendable {
        case open, started, dismissed, unknown
        init(from decoder: Decoder) throws {
            self = State(rawValue: try decoder.singleValueContainer().decode(String.self)) ?? .unknown
        }
    }

    var id: String
    var conversation: String
    var tasks: [PlanTask]
    var state: State
    var createdAt: Date?
    var updatedAt: Date?

    enum CodingKeys: String, CodingKey {
        case id, conversation, tasks, state
        case createdAt = "created_at", updatedAt = "updated_at"
    }
}

/// How an interrupted receipt's task was dealt with, as the hub worked it out (#3834; docs/protocol/protocol.md
/// "Tasks and the chat feed").
nonisolated enum InterruptResolution: String, Codable, Hashable, Sendable {
    case pending, cancelled, rerouted, resumed, accepted, done
}

/// Interruptions with the same cause in one chat within ten minutes, as their first receipt carries them (#3834).
nonisolated struct InterruptBatch: Codable, Hashable, Sendable {
    struct Entry: Codable, Hashable, Sendable {
        var receipt: String
        var todoID: String
        var title: String
        var resolution: String
        enum CodingKeys: String, CodingKey { case receipt, todoID = "todo_id", title, resolution }
        /// A resolution this build doesn't know reads as handled, not as pending.
        var resolved: InterruptResolution { InterruptResolution(rawValue: resolution) ?? .done }
    }
    var cause: String
    var machine: String?
    var total: Int
    var pending: Int
    var resolved: Int
    var tasks: [Entry]
}

/// How the chat draws an interrupted receipt: `hidden` inside its batch's head, `batch` the head of several,
/// `resolved` one dealt with (a grey line), `pending` still to deal with (the red card).
nonisolated enum InterruptLook: Equatable, Sendable {
    case hidden
    case batch(InterruptBatch)
    case resolved(InterruptResolution)
    case pending
}

/// An item the hub inserted into an assistant conversation; drawn among the transcript's lines by `at`.
nonisolated struct FeedItem: Codable, Hashable, Identifiable, Sendable {
    enum Kind: String, Codable, Sendable {
        case plan, task, answered, receipt, routed, today, message, view, notice, review, report, brief, unknown
        init(from decoder: Decoder) throws {
            self = Kind(rawValue: try decoder.singleValueContainer().decode(String.self)) ?? .unknown
        }
    }

    var id: String
    var conversation: String
    var at: Date
    var kind: Kind
    /// The plan id (`plan`) or the todo id (everything else).
    var ref: String?
    var body: JSONValue?

    enum CodingKeys: String, CodingKey { case id, conversation, at, kind, ref, body }

    /// A plan card carries the plan in its body: drawn even when the `task_plan` event for it was missed or raced.
    var plan: TaskPlan? {
        guard case .object(let o)? = body, let p = o["plan"] else { return nil }
        return try? JSON.decoder.decode(TaskPlan.self, from: JSONEncoder().encode(p))
    }

    /// A live card's creation record; newer content comes from the feed snapshot or assistant_view event.
    var assistantView: ExtensionAssistantView? {
        guard kind == .view, case .object(let o)? = body, let view = o["view"] else { return nil }
        return try? JSON.decoder.decode(ExtensionAssistantView.self, from: JSONEncoder().encode(view))
    }

    private func string(_ key: String) -> String? {
        guard case .object(let o)? = body, case .string(let s)? = o[key] else { return nil }
        return s
    }

    /// `answered`: what the card asked and the user's answer, a line each (the collapsed row).
    var question: String? { string("question") }
    var answer: String? { string("answer") }

    /// `answered`: every question of the card with the user's answer in full, newlines kept (#1231). A record from
    /// before it has only the line, which stands in; empty when there is nothing to show.
    var answeredItems: [AnsweredItem] {
        if case .object(let o)? = body, case .array(let list)? = o["items"] {
            let items = list.compactMap { v -> AnsweredItem? in
                guard case .object(let it) = v else { return nil }
                let item = AnsweredItem(question: it["question"]?.string ?? "", answer: it["answer"]?.string ?? "")
                return item.question.isEmpty && item.answer.isEmpty ? nil : item
            }
            if !items.isEmpty { return items }
        }
        let line = AnsweredItem(question: question ?? "", answer: answer ?? "")
        return line.question.isEmpty && line.answer.isEmpty ? [] : [line]
    }
    /// `receipt`: the task's title, done / failed, the agent's last words, its PR and machine.
    var title: String? { string("title") }
    var failed: Bool { string("state") == "failed" }
    /// A turn the model service cut off (#1516): not done, whatever its to-do says.
    var turnInterrupted: Bool { string("state") == "interrupted" }
    /// An interrupted receipt's raw error (the API error that cut the turn off); older hubs put it in `summary`.
    var receiptError: String? { string("error") }
    var summary: String? { string("summary") }
    /// How the chat draws an interrupted receipt, from what the hub worked out (#3834): nil from a hub before it.
    var interruptLook: InterruptLook? {
        guard kind == .receipt, turnInterrupted, let raw = string("resolution"),
              let resolution = InterruptResolution(rawValue: raw) else { return nil }
        if let group = string("group"), !group.isEmpty, group != id { return .hidden }
        if case .object(let o)? = body, let b = o["batch"],
           let data = try? JSONEncoder().encode(b), let batch = try? JSONDecoder().decode(InterruptBatch.self, from: data),
           batch.tasks.count > 1 {
            return .batch(batch)
        }
        return resolution == .pending ? .pending : .resolved(resolution)
    }
    var pr: String? { string("pr") }
    var machine: String? { string("machine") }
    /// `routed`: what the user said straight to the task; `message`: the message itself.
    var text: String? { string("text") }
    /// `notice`: a line the hub wrote in the user's language (#4305), nil when it has no words.
    var noticeText: String? {
        guard kind == .notice, let t = text?.trimmingCharacters(in: .whitespacesAndNewlines), !t.isEmpty else { return nil }
        return t
    }
    /// `message`: a message about a task with the AI twin's draft reply; `ref` is the inbox item.
    var todoID: String? { string("todo_id") }
    var source: String? { string("source") }
    var chat: String? { string("chat") }
    var sender: String? { string("sender") }
    var draft: String? { string("draft") }
    /// `message`: the hub says the reply went out from a card (`sent_at`, what it said in `sent`), or that the item was
    /// handled elsewhere first (`filed_at`, #4797): either way Send is refused, and a sent card is never filed.
    var sentAt: Date? { string("sent_at").flatMap(JSON.parseDate) }
    var sent: String? { string("sent") }
    var filedAt: Date? { sentAt == nil ? string("filed_at").flatMap(JSON.parseDate) : nil }
    /// A per-card decision; it does not file the Inbox item or stop the task's Agent.
    var dismissedAt: Date? { string("dismissed_at").flatMap(JSON.parseDate) }
    var dismissalID: String? { string("dismissal_id") }

    /// `review`: the supervisor's verdict (accepted / rework / needs_user / cancelled), the rework round, its note.
    var verdict: String? { string("verdict") }
    var round: Int? { if case .object(let o)? = body { o["round"]?.int } else { nil } }
    var note: String? { string("note") }
    /// `report`: what the task's agent told Persona (question / blocked / done), with its evidence and step.
    var reportKind: String? { kind == .report ? string("kind") : nil }
    var evidence: [String] {
        guard case .object(let o)? = body, let list = o["evidence"]?.array else { return [] }
        return list.compactMap { v in
            guard let s = v.string?.trimmingCharacters(in: .whitespacesAndNewlines), !s.isEmpty else { return nil }
            return s
        }
    }
    var step: Int? { if case .object(let o)? = body { o["step"]?.int } else { nil } }
    var steps: Int? { if case .object(let o)? = body { o["steps"]?.int } else { nil } }
    /// `routed`: Persona stopped the agent's turn first and then told it (agent_interrupt with then, #5112).
    var interrupted: Bool { if case .object(let o)? = body { o["interrupted"]?.bool == true } else { false } }
    /// `routed`: Persona wrote it to the task's agent (agent_send with a task, agent_interrupt's then, a rework; #1542), so
    /// it is drawn folded as Persona's (`AgentSentLine`), never as the user's bubble. What the user sent has no `from`.
    var sentByPersona: Bool { kind == .routed && string("from") == "assistant" }

    /// `today`: the day's card (TodayBody).
    var today: TodayBody? {
        guard kind == .today, let body, let data = try? JSON.encoder.encode(body) else { return nil }
        return try? JSON.decoder.decode(TodayBody.self, from: data)
    }

    /// "PR #901" from a pull request URL.
    var prLabel: String? {
        guard let pr, let n = pr.split(separator: "/").last, Int(n) != nil else { return nil }
        return "PR #\(n)"
    }
}

/// What Persona sent to its tasks' agents in a conversation's feed (#1542, docs/assistant/unified-entry.md "Messages to agents"):
/// a task's card lists them, the hand-off sent with the card is drawn only there, a later one is a folded line.
nonisolated enum SentToAgents {
    /// How long after its task's card a message of Persona's counts as the card's hand-off: agent_send with a task writes
    /// the card and, right after it, what it sent.
    static let handOff: TimeInterval = 60

    /// The hand-offs the task cards take in (not drawn on their own): Persona's messages within `handOff` after their own
    /// task's card in the same conversation. Only a card drawn in full (from today, `FeedLine`) has the list: from another
    /// day it is one line (`TaskLine`), and its hand-off stays a folded line under it (#1668).
    static func takenIn(_ feed: [FeedItem], now: Date = .now, calendar: Calendar = .current) -> Set<String> {
        var cards: [String: [Date]] = [:]
        for item in feed where item.kind == .task && calendar.isDate(item.at, inSameDayAs: now) {
            if let ref = item.ref { cards[item.conversation + "/" + ref, default: []].append(item.at) }
        }
        guard !cards.isEmpty else { return [] }
        var out: Set<String> = []
        for item in feed where item.sentByPersona {
            guard let ref = item.ref, let ats = cards[item.conversation + "/" + ref] else { continue }
            if ats.contains(where: { $0 <= item.at && item.at.timeIntervalSince($0) <= handOff }) { out.insert(item.id) }
        }
        return out
    }

    /// The feed without the hand-offs the cards take in: what the chat draws among its lines.
    static func shown(_ feed: [FeedItem], now: Date = .now, calendar: Calendar = .current) -> [FeedItem] {
        let hidden = takenIn(feed, now: now, calendar: calendar)
        return hidden.isEmpty ? feed : feed.filter { !hidden.contains($0.id) }
    }

    /// Persona's messages to task `todo`, oldest first: what its card lists.
    static func sent(_ feed: [FeedItem], todo: String) -> [FeedItem] {
        feed.filter { $0.sentByPersona && $0.ref == todo }.sorted { ($0.at, $0.id) < ($1.at, $1.id) }
    }

    /// The first line with words in it, trimmed: what a folded message shows.
    static func firstLine(_ text: String) -> String {
        for line in text.split(whereSeparator: \.isNewline) {
            let t = line.trimmingCharacters(in: .whitespaces)
            if !t.isEmpty { return t }
        }
        return ""
    }
}

/// One question of an answered card and what the user answered (`FeedItem.answeredItems`).
nonisolated struct AnsweredItem: Hashable, Sendable {
    var question: String
    var answer: String
}

/// The Today card's content (docs/protocol/protocol.md「Tasks and the chat feed」, kind `today`), built by the hub.
nonisolated struct TodayBody: Codable, Hashable, Sendable {
    nonisolated struct TaskRef: Codable, Hashable, Sendable { var id: String; var title: String }
    nonisolated struct Meeting: Codable, Hashable, Sendable { var start: Date; var end: Date; var title: String }
    nonisolated struct Chat: Codable, Hashable, Sendable { var name: String; var unread: Int?; var preview: String? }
    /// One thing that did not go through overnight (#4124): `task`, a dispatched task whose agent did not start (`id` its
    /// todo); `routine`, a scheduled run that failed or was skipped (`id` the scheduled task, `ref` the run).
    nonisolated struct Failed: Codable, Hashable, Sendable {
        var kind: String
        var id: String
        var ref: String?
        var title: String
        var at: Date
        var why: String?
        var skipped: Bool?
    }

    var date: String
    var done: [TaskRef]?
    var waiting: [TaskRef]?
    var working: Int?
    var meetings: [Meeting]?
    var atMe: [Chat]?
    var unread: Int?
    /// What did not go through since yesterday 18:00, newest first: the hub's newest 8, `failedTotal` all of them. A hub
    /// before #4124 sends neither.
    var failed: [Failed]?
    var failedTotal: Int?

    enum CodingKeys: String, CodingKey {
        case date, done, waiting, working, meetings, unread, failed
        case atMe = "at_me", failedTotal = "failed_total"
    }
}

/// A todo (docs/assistant/todos.md); a task is one whose `origin` is an assistant conversation.
nonisolated struct TodoItem: Codable, Hashable, Identifiable, Sendable {
    nonisolated struct Space: Codable, Hashable, Sendable {
        var machineID: String
        var space: String
        var linkedAt: Date?

        enum CodingKeys: String, CodingKey {
            case space
            case machineID = "machine_id", linkedAt = "linked_at"
        }
    }

    nonisolated struct Agent: Codable, Hashable, Sendable {
        var machineID: String
        var sessionID: String
        var title: String?
        var cwd: String?
        var agent: String?
        var startError: String?
        var releasedAt: Date?
        var linkedAt: Date?
        var source: String?
        var via: String?

        enum CodingKeys: String, CodingKey {
            case title, cwd, agent, source, via
            case machineID = "machine_id", sessionID = "session_id", startError = "start_error",
                 releasedAt = "released_at", linkedAt = "linked_at"
        }

        var isActive: Bool { releasedAt == nil }
    }

    var id: String
    var title: String
    var notes: String?
    var doneAt: Date?
    var createdAt: Date?
    var updatedAt: Date?
    /// When it was last reopened (by the user, or by the hub when its agent got more work); nil from older hubs.
    var reopenedAt: Date?
    var origin: String?
    var planID: String?
    /// The task's agent did not start, and why: the hub ended the todo there (it is done, #1077).
    var startError: String?
    /// "auto" when the hub marked the task done because its work was judged finished, with why (#1092,
    /// docs/assistant/todos.md「Automatic completion」); nil when the user did.
    var doneBy: String?
    var doneReason: String?
    var agents: [Agent]
    var spaces: [Space]?
    /// How far its work got (`stage`): one of `TodoStage`'s values, empty when the hub does not know (#2776). Kept as
    /// the hub sent it: a value this build does not know draws no stage bar either.
    var stage: String?
    /// The pull request its work is in (`pr_url`, #2768); nil from a hub without it.
    var prURL: String?
    /// Started from the main conversation while Notify Main-Chat Tasks Only in the Main Chat was on (#1731, fixed when the hub made it):
    /// the main conversation tells the user about it, nothing else does (the island, notifications, badges, the watch).
    /// Its card in the conversation shows as always. False from a hub without it.
    var quiet: Bool
    /// The main conversation supervises it (#3124, `Supervision.swift`): what done means, its reviews, its agent's
    /// latest report. Nil for a todo that is not supervised, from an older hub, or when it does not decode.
    var supervision: TaskSupervision?
    /// The current owner decision or verification round; malformed asks never hide the task.
    var ask: TodoAsk?
    /// When it is due (#1683, docs/assistant/todos.md "Due date, reminder, priority"): a moment, or a day ("yyyy-MM-dd",
    /// due by its end in the user's time zone); at most one. Nil from a hub without them.
    var dueAt: Date?
    var dueDate: String?
    /// When the phone is pushed about it, and when that was done (a new time is pushed again).
    var remindAt: Date?
    var remindedAt: Date?
    /// "high" or "low"; nil is normal.
    var priority: String?
    /// Where its finished work stands (`delivery`, docs/assistant/todos.md "Delivery"); nil from a hub without it.
    var delivery: TodoDelivery?

    enum CodingKeys: String, CodingKey {
        case id, title, notes, origin, agents, spaces, quiet, stage, supervision, priority, ask, delivery
        case doneAt = "done_at", createdAt = "created_at", updatedAt = "updated_at", reopenedAt = "reopened_at", planID = "plan_id", startError = "start_error",
             doneBy = "done_by", doneReason = "done_reason", prURL = "pr_url", dueAt = "due_at", dueDate = "due_date",
             remindAt = "remind_at", remindedAt = "reminded_at"
    }

    init(id: String, title: String, notes: String? = nil, doneAt: Date? = nil, createdAt: Date? = nil, updatedAt: Date? = nil,
         origin: String? = nil, planID: String? = nil, startError: String? = nil, doneBy: String? = nil, doneReason: String? = nil,
         agents: [Agent] = [], spaces: [Space]? = nil, quiet: Bool = false, stage: String? = nil, prURL: String? = nil,
         supervision: TaskSupervision? = nil, dueAt: Date? = nil, dueDate: String? = nil, remindAt: Date? = nil,
         remindedAt: Date? = nil, priority: String? = nil, ask: TodoAsk? = nil, reopenedAt: Date? = nil,
         delivery: TodoDelivery? = nil) {
        self.id = id
        self.title = title
        self.notes = notes
        self.doneAt = doneAt
        self.createdAt = createdAt
        self.updatedAt = updatedAt
        self.origin = origin
        self.planID = planID
        self.startError = startError
        self.doneBy = doneBy
        self.doneReason = doneReason
        self.agents = agents
        self.spaces = spaces
        self.quiet = quiet
        self.stage = stage
        self.prURL = prURL
        self.supervision = supervision
        self.dueAt = dueAt
        self.dueDate = dueDate
        self.remindAt = remindAt
        self.remindedAt = remindedAt
        self.priority = priority
        self.ask = ask
        self.reopenedAt = reopenedAt
        self.delivery = delivery
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        title = try c.decodeIfPresent(String.self, forKey: .title) ?? ""
        notes = try c.decodeIfPresent(String.self, forKey: .notes)
        doneAt = try c.decodeIfPresent(Date.self, forKey: .doneAt)
        createdAt = try c.decodeIfPresent(Date.self, forKey: .createdAt)
        updatedAt = try c.decodeIfPresent(Date.self, forKey: .updatedAt)
        origin = try c.decodeIfPresent(String.self, forKey: .origin)
        planID = try c.decodeIfPresent(String.self, forKey: .planID)
        startError = try c.decodeIfPresent(String.self, forKey: .startError)
        doneBy = try c.decodeIfPresent(String.self, forKey: .doneBy)
        doneReason = try c.decodeIfPresent(String.self, forKey: .doneReason)
        agents = try c.decodeIfPresent([Agent].self, forKey: .agents) ?? []
        spaces = try c.decodeIfPresent([Space].self, forKey: .spaces)
        quiet = try c.decodeIfPresent(Bool.self, forKey: .quiet) ?? false
        // A hub before #2776 / #2768 sends neither: old payloads decode as before.
        stage = try c.decodeIfPresent(String.self, forKey: .stage)
        prURL = try c.decodeIfPresent(String.self, forKey: .prURL)
        // Optional and new (#3124): one that does not decode is as if there were none, never the whole todo failing.
        supervision = (try? c.decodeIfPresent(TaskSupervision.self, forKey: .supervision)) ?? nil
        // New (#1683): absent from older hubs; one that does not decode is as if there were none.
        dueAt = (try? c.decodeIfPresent(Date.self, forKey: .dueAt)) ?? nil
        dueDate = (try? c.decodeIfPresent(String.self, forKey: .dueDate)) ?? nil
        remindAt = (try? c.decodeIfPresent(Date.self, forKey: .remindAt)) ?? nil
        remindedAt = (try? c.decodeIfPresent(Date.self, forKey: .remindedAt)) ?? nil
        priority = (try? c.decodeIfPresent(String.self, forKey: .priority)) ?? nil
        ask = (try? c.decodeIfPresent(TodoAsk.self, forKey: .ask)) ?? nil
        // New (#3206): absent from older hubs; one that does not decode is as if there were none.
        reopenedAt = (try? c.decodeIfPresent(Date.self, forKey: .reopenedAt)) ?? nil
        delivery = (try? c.decodeIfPresent(TodoDelivery.self, forKey: .delivery)) ?? nil
    }

    /// An ended task cannot keep an old decision card alive.
    var openAsk: TodoAsk? { doneAt == nil && ask?.askedAt.isEmpty == false ? ask : nil }
    var activeAgents: [Agent] { agents.filter(\.isActive) }
    /// The hub ended it because its agent did not start.
    var failedToStart: Bool { !(startError ?? "").isEmpty }
    /// The hub marked it done because its task's work was judged finished; reopening undoes it.
    var autoDone: Bool { doneAt != nil && doneBy == "auto" }
}

/// Where a task's finished work stands (`delivery`): `live`, `pr`, `merged`, `off` or `none`, what holds it and who
/// can unhold it. Kept as the hub sent it; a state this build does not know is just not one of those.
nonisolated struct TodoDelivery: Codable, Hashable, Sendable {
    var state: String
    var gate: String?
    var unblock: String?
    var at: Date?

    enum CodingKeys: String, CodingKey { case state, gate, unblock, at }

    init(state: String, gate: String? = nil, unblock: String? = nil, at: Date? = nil) {
        self.state = state
        self.gate = gate
        self.unblock = unblock
        self.at = at
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        state = (try? c.decodeIfPresent(String.self, forKey: .state)) ?? ""
        gate = (try? c.decodeIfPresent(String.self, forKey: .gate)) ?? nil
        unblock = (try? c.decodeIfPresent(String.self, forKey: .unblock)) ?? nil
        at = (try? c.decodeIfPresent(Date.self, forKey: .at)) ?? nil
    }
}

/// The agents of quiet tasks (#1731, `TodoItem.quiet`): the main conversation tells the user about them, so the island,
/// notifications, badges, the watch and the Mac's menu bar leave them out. The hub marks such a session `quiet` itself;
/// the todo list is the fallback for a session the hub has not sent again since (and for a cache from before).
nonisolated struct QuietTasks: Equatable, Sendable {
    /// The quiet todos' ids.
    var todos: Set<String> = []
    /// "machine/session" of every agent a quiet todo has had: a released one too, its finish stays the conversation's.
    var agents: Set<String> = []

    init(todos: Set<String> = [], agents: Set<String> = []) {
        self.todos = todos
        self.agents = agents
    }

    init(_ list: some Sequence<TodoItem>) {
        for t in list where t.quiet {
            todos.insert(t.id)
            for a in t.agents { agents.insert(Self.key(machine: a.machineID, session: a.sessionID)) }
        }
    }

    static func key(machine: String, session: String) -> String { machine + "/" + session }

    /// A session of the todos' hub (the caller checks the group): quiet by the hub's word, its todo, or a quiet todo
    /// that has it as an agent.
    func covers(_ s: Session) -> Bool {
        if s.quiet { return true }
        if let id = s.todo?.id, todos.contains(id) { return true }
        return agents.contains(Self.key(machine: s.machineID, session: s.sessionID))
    }

    /// A fleet item's id ("machine/session") or a session key of the todos' hub without a session at hand.
    func covers(machine: String, session: String) -> Bool { agents.contains(Self.key(machine: machine, session: session)) }
}

extension QuietTasks {
    /// The watch's lists (#1731): the sessions without the quiet ones, and the questions without theirs.
    static func leavingOut(sessions: [Session], interactions: [Interaction], quiet: (Session) -> Bool)
        -> (sessions: [Session], interactions: [Interaction]) {
        let keys = Set(sessions.filter(quiet).map(\.key))
        guard !keys.isEmpty else { return (sessions, interactions) }
        return (sessions.filter { !keys.contains($0.key) }, interactions.filter { !keys.contains($0.sessionKey) })
    }
}

/// Shaping of the cards the watch pulls itself from GET /api/v1/interactions.
nonisolated enum WatchInteractions {
    /// A muted agent's or space's card stays off the watch entirely (#4360): no row and no buzz when it arrives,
    /// like the phone's Agents badge and inbox. The hub stamps `muted`; an older hub leaves it false. A question the
    /// assistant is still looking at stays off too (#544): it buzzes once it is handed to the user.
    static func leavingMuted(_ interactions: [Interaction]) -> [Interaction] {
        interactions.filter { !$0.muted && !$0.isDelegating }
    }

    /// The cards the watch has not shown yet: only these are worth a buzz. Called after the muted ones are out, so a
    /// muted card arriving never plays the notification sound.
    static func newOnes(in interactions: [Interaction], alreadyShown: [Interaction]) -> [Interaction] {
        interactions.filter { ix in !alreadyShown.contains { $0.uid == ix.uid } }
    }
}

/// A small, value-only snapshot of the work a tray row shows. It does not carry transcripts or subscribe to every session.
nonisolated struct AssistantTrayActivity: Hashable, Sendable {
    struct Workflow: Hashable, Sendable {
        var title: String
        var phase: String?
        var counts: String?
        var startedAt: Date? = nil
    }

    var backgroundTasks = 0
    var subagents = 0
    var workflows: [Workflow] = []
    var workflowCount = 0
    /// An older complete response may say background work runs without listing its kind.
    var reportedBackgroundWork = false

    var isActive: Bool { backgroundTasks > 0 || subagents > 0 || workflowCount > 0 || reportedBackgroundWork }

    init() {}

    init(sessions: [Session]) {
        var seen = Set<SessionKey>()
        for session in sessions where seen.insert(session.key).inserted {
            let background = session.background ?? []
            // Fresh bounded-summary counts, including omitted zeros, win over retained chat arrays (#3588).
            if session.listWaiting != nil || session.listBackgroundTasksCount != nil {
                let count = max(0, session.listRunningWorkflows ?? 0)
                let agents = max(0, session.listRunningSubagents ?? 0)
                workflowCount += count
                subagents += agents
                backgroundTasks += max(0, session.listBackgroundTasksCount ?? ((session.listBackgroundCount ?? 0) - count - agents))
                if count > 0, let run = session.listWorkflowProgress, run.isRunning {
                    workflows.append(Workflow(title: run.title, phase: run.phaseLabel, counts: run.counts, startedAt: run.startedAt))
                }
                continue
            }
            reportedBackgroundWork = reportedBackgroundWork || (session.backgroundWorking && !session.backgroundWaiting)
            let runs = session.runningWorkflows
            let workflowMarkers = background.filter { task in
                task.isWorkflow && !(session.workflows ?? []).contains { run in
                    (run.id == task.runID || run.taskID == task.id) && !run.isRunning
                }
            }
            let count = max(runs.count, session.listRunningWorkflows ?? 0, workflowMarkers.count)
            workflowCount += count
            workflows += runs.map { Workflow(title: $0.title, phase: $0.phaseLabel, counts: $0.counts, startedAt: $0.startedAt) }
            let listedAgents = session.subagents ?? []
            let backgroundAgents = background.filter { task in
                task.isAgent && !listedAgents.contains { $0.id == task.id && !$0.running }
            }
            let agentIDs = Set(listedAgents.filter(\.running).map(\.id) + backgroundAgents.map(\.id))
            subagents += max(agentIDs.count, session.listRunningSubagents ?? 0)
            if session.background != nil {
                backgroundTasks += background.filter { !$0.isWorkflow && !$0.isAgent }.count
            } else {
                // Bounded lists include compatibility workflow markers in background_count too.
                backgroundTasks += max(0, (session.listBackgroundCount ?? 0) - count)
            }
        }
        workflows = workflows.enumerated().sorted { a, b in
            let first = a.element.startedAt ?? .distantPast, second = b.element.startedAt ?? .distantPast
            return first != second ? first > second : a.offset < b.offset
        }.map(\.element)
    }
}

/// An agent going on (Needs You / Working) as the main conversation's dock shows it after the tasks (#1113): the ones the user
/// opened on the Agents page, which no conversation's task stands for. Made from the Agents page's session list, so
/// it follows the agents without a poll of its own.
nonisolated struct DockAgent: Hashable, Identifiable, Sendable {
    var key: SessionKey
    var title: String
    var state: AgentState
    /// When it entered its state (the hub's `state_since`): the newest first among the same state.
    var since: Date?
    /// The todo it works for, as the hub tags the session (a task's, or one made by hand).
    var todoID: String?
    var activity: AssistantTrayActivity
    var backgroundWaiting: Bool

    var id: SessionKey { key }
    var isWaiting: Bool { state == .needsYou }

    init(key: SessionKey, title: String, state: AgentState, since: Date? = nil, todoID: String? = nil,
         activity: AssistantTrayActivity = .init(), backgroundWaiting: Bool = false) {
        self.key = key
        self.title = title
        self.state = state
        self.since = since
        self.todoID = todoID
        self.activity = activity
        self.backgroundWaiting = backgroundWaiting
    }

    /// The agents going on in a session list (Persona's own conversations are not agents): waiting for the user
    /// first, then the newest. Only what a pill shows goes in, so it changes when a pill would. A subtask (a Codex
    /// subagent, an agent another agent created) is left out while a session above it is going too, which stands for
    /// it, as the Agents list folds it under that one (`CodexTasks`); one that waits for the user never is.
    @MainActor static func going(_ sessions: [Session]) -> [DockAgent] {
        let goes = { (s: Session) in !s.assistant && (s.state == .needsYou || s.state == .working) }
        var going = sessions.filter(goes)
        if going.contains(where: \.isNestedSubtask), let parent = CodexTasks.parents(sessions, runs: goes) {
            going = sessions.indices.filter { parent[$0] < 0 && goes(sessions[$0]) }.map { sessions[$0] }
        }
        return going
            .map { DockAgent(key: $0.key, title: $0.displayTitle, state: $0.state, since: $0.stateSince, todoID: $0.todo?.id,
                             activity: AssistantTrayActivity(sessions: [$0]), backgroundWaiting: $0.waitsToBeWoken) }
            .sorted { a, b in
                if a.state.priority != b.state.priority { return a.state.priority < b.state.priority }
                if a.since != b.since { return (a.since ?? .distantPast) > (b.since ?? .distantPast) }
                return a.key.path < b.key.path
            }
    }

    /// What a conversation's dock shows after its tasks: in the main conversation, every agent going on that no task
    /// stands for (a task's agents are its pill already, whichever conversation opened it); elsewhere nothing.
    /// `taskAgents` are the agents of the tasks not done. An agent the hub tags with a todo shows only when that todo
    /// is one of `manualTodos` (made by hand): a task the todo list has not caught up with yet stays out too. Those
    /// are the todos of `group`'s hub; another group's agent tagged with a todo of its own hub stays out.
    static func shown(_ going: [DockAgent], conversation: String, main: String?, group: String,
                      taskAgents: Set<SessionKey>, manualTodos: Set<String>) -> [DockAgent] {
        guard let main, !main.isEmpty, conversation == main else { return [] }
        return going.filter { a in
            guard !taskAgents.contains(a.key) else { return false }
            guard let todo = a.todoID else { return true }
            return a.key.group == group && manualTodos.contains(todo)
        }
    }
}

/// Where a task is, from its todo and its agents' states (docs/assistant/todos.md「State calculation rules」, the hub's taskState).
nonisolated enum TaskState: String, Sendable, CaseIterable {
    /// `waiting`: its agents ended their turn, yet something brings one back, its background work or the hub's wake-up
    /// (`Session.waitsToBeWoken`, #3274); only a task nothing brings back is `followup`.
    case needsYou, working, unread, waiting, followup, notStarted, done, failed

    /// `agents`: the states of the agents that do not wait to be woken; `waiting`: whether one does.
    init(todo: TodoItem, agents: [AgentState], waiting: Bool = false) {
        if todo.failedToStart { self = .failed; return }
        if todo.doneAt != nil { self = .done; return }
        if todo.openAsk != nil { self = .needsYou; return }
        let has = Set(agents)
        if has.contains(.needsYou) { self = .needsYou }
        else if has.contains(.unread) { self = .unread }
        else if has.contains(.working) { self = .working }
        else if waiting { self = .waiting }
        else if !agents.isEmpty { self = .followup }
        else { self = .notStarted }
    }

    /// From the task's agents as the app has them: one that waits to be woken makes it waiting, not stopped (#3274).
    init(todo: TodoItem, sessions: [Session]) {
        let waits = sessions.filter(\.waitsToBeWoken)
        let others = sessions.filter { !$0.waitsToBeWoken }.map(\.state)
        self.init(todo: todo, agents: others, waiting: !waits.isEmpty)
    }

    /// The agent state whose tone and mark the task borrows.
    var agentState: AgentState {
        switch self {
        case .needsYou: .needsYou
        case .working, .waiting, .notStarted: .working
        case .unread, .done: .unread
        case .followup, .failed: .idle
        }
    }

    /// Lower sorts first in the dock and the board: what waits for the user, then what runs, then the rest.
    var priority: Int {
        switch self {
        case .needsYou: 0
        case .working, .waiting, .notStarted: 1
        case .unread: 2
        case .followup: 3
        case .done, .failed: 4
        }
    }

    /// The task finished its work (its last turn ended and nothing waits), or never began it.
    var isSettled: Bool { self == .unread || self == .followup || self == .done || self == .failed }

    /// Nothing of the task runs (it ended, stopped, or its agent never started; `notStarted` stays when the hub never
    /// said how the start went): the user may take it out of the conversation.
    var isRemovable: Bool { self != .working && self != .waiting && self != .needsYou }
}

/// What a task's card says its agent is on now (docs/assistant/unified-entry.md「Task cards」): its latest words, or — while it
/// only runs tools (reads code, runs tests, waits for CI) and its words stay put for minutes — the step it is on and
/// since when. New words win again as soon as they come (the hub drops the step then).
nonisolated enum TaskNow: Equatable, Sendable {
    case words(String)
    case step(String, since: Date?)

    /// From the task's lead agent; nil when it has said and done nothing yet. A working agent's step comes before its
    /// words (the hub only sends a step newer than them); once it stopped, its words do (a turn cut short on a tool call).
    init?(lead: Session?, working: Bool) {
        let words = lead?.lastText?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        let step = lead?.activity?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        if !step.isEmpty, working || words.isEmpty {
            self = .step(step, since: working ? lead?.activityAt : nil)
        } else if !words.isEmpty {
            self = .words(words)
        } else {
            return nil
        }
    }

    var text: String {
        switch self {
        case .words(let s), .step(let s, _): s
        }
    }
}

/// What a task waiting for the user asks (#3998): the first of its pending cards that says anything, in the inbox
/// Needs You row's words (`InboxAgentRow`), on one line; nil when none does.
nonisolated enum TaskAsk {
    static func line(_ interactions: [Interaction]) -> String? {
        for ix in interactions {
            let text = ix.askLine(includeToolName: false) ?? ix.summary ?? ix.questions?.first?.question ?? ""
            let line = text.split(whereSeparator: \.isNewline).map { $0.trimmingCharacters(in: .whitespaces) }
                .filter { !$0.isEmpty }.joined(separator: " ")
            if !line.isEmpty { return line }
        }
        return nil
    }

    /// The task card's line while it waits: what it asks, else the generic word.
    static func waitingLine(_ interactions: [Interaction]) -> String {
        line(interactions) ?? String(localized: "Waiting for your answer [Tasks 542ebd]")
    }
}

/// What each of an agent's running subagents is for (docs/assistant/unified-entry.md): shown under its own progress,
/// never instead of it.
nonisolated func runningSubagents(_ lead: Session?) -> [String] {
    (lead?.subagents ?? [])
        .filter(\.running)
        .map {
            let desc = $0.description?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
            if !desc.isEmpty { return desc }
            let typ = $0.type?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
            if !typ.isEmpty { return typ }
            return String(localized: "Subtask")
        }
}

/// A single summary line of running subagents for task cards: "Subagents running: A, B" (at most 3; "…, N in all" if more).
nonisolated func subagentsSummaryLine(_ subagents: [String]) -> String? {
    guard !subagents.isEmpty else { return nil }
    let head = subagents.prefix(3).joined(separator: String(localized: "、", comment: "Separator between names in a list"))
    if subagents.count > 3 { return String(localized: "Subagents running: \(head), \(subagents.count) in all", comment: "Running subagents: the first three names and how many in all") }
    return String(localized: "Subagents running: \(head)")
}

// MARK: - Endpoints

nonisolated extension APIClient {
    struct FeedResponse: Decodable, Sendable {
        var items: [FeedItem]
        var plans: [String: TaskPlan]?
        var views: [String: ExtensionAssistantView]?
        var viewActions: [String: ExtensionViewAction]?
        var viewBlocks: [String]?
        /// nil: legacy hub; empty: the frozen recovery window is complete.
        var nextCursor: String?

        enum CodingKeys: String, CodingKey {
            case items, plans, views
            case nextCursor = "next_cursor"
            case viewActions = "view_actions"
            case viewBlocks = "view_blocks"
        }
    }

    private struct PlanResponse: Decodable { var plan: TaskPlan }
    nonisolated private struct TodosResponse: Decodable, Sendable { var todos: [TodoItem] }
    private struct ItemResponse: Decodable { var item: FeedItem }
    struct DraftDismissal: Decodable, Sendable {
        var item: FeedItem
        var dismissalID: String
        enum CodingKeys: String, CodingKey { case item, dismissalID = "dismissal_id" }
    }

    /// Resolve exactly the displayed draft, using its original text rather than local edits.
    func dismissDraft(_ id: String, original: String) async throws -> DraftDismissal {
        struct Body: Encodable { var draft: String }
        return try await post("/api/v1/assistant/feed/\(Self.pathPart(id))/dismiss", body: Body(draft: original), as: DraftDismissal.self)
    }

    /// Only the decision identified by this receipt can be undone.
    func undoDraftDismissal(_ id: String, receipt: String) async throws -> FeedItem {
        struct Body: Encodable {
            var dismissalID: String
            enum CodingKeys: String, CodingKey { case dismissalID = "dismissal_id" }
        }
        return try await post("/api/v1/assistant/feed/\(Self.pathPart(id))/undo-dismiss", body: Body(dismissalID: receipt), as: ItemResponse.self).item
    }

    /// A conversation's feed after `since` (nil: all of it), with the plans its `plan` items name.
    func feed(conversation: String, since: Date? = nil, cursor: String? = nil) async throws -> FeedResponse {
        let id = conversation.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed.subtracting(CharacterSet(charactersIn: "/?#"))) ?? conversation
        var query: [String: String]
        if let cursor {
            query = ["cursor": cursor]
        } else {
            query = ["paginate": "1"]
            if let since { query["since"] = Self.feedTime.string(from: since) }
        }
        return try await get("/api/v1/assistant/conversations/\(id)/feed", query: query)
    }

    func todos() async throws -> [TodoItem] {
        try await getDecodedOffMain("/api/v1/todos", as: TodosResponse.self).todos
    }

    /// Starts an open plan; `edits` replace the fields the user changed (by index), `remember` lets this kind start
    /// without asking next time.
    func startPlan(_ id: String, edits: [PlanTask]? = nil, remember: Bool = false) async throws -> TaskPlan {
        struct Body: Encodable { var tasks: [PlanTask]?; var remember: Bool }
        return try await post("/api/v1/task-plans/\(id)/start", body: Body(tasks: edits, remember: remember), as: PlanResponse.self).plan
    }

    func dismissPlan(_ id: String) async throws -> TaskPlan {
        try await post("/api/v1/task-plans/\(id)/dismiss", as: PlanResponse.self).plan
    }

    /// The Accept button of a `brief` item's to-do to verify (#2320; the to-do rows' one too, #2056): `askedAt` is the round's
    /// ask as the hub sent it. A 409 means that round is over (asked again since, or already verified).
    func acceptTodo(_ id: String, askedAt: String) async throws {
        struct Body: Encodable {
            var askedAt: String
            enum CodingKeys: String, CodingKey { case askedAt = "asked_at" }
        }
        _ = try await post("/api/v1/todos/\(Self.pathPart(id))/accept", body: Body(askedAt: askedAt))
    }

    /// The Handled button of a question that can wait (a `brief` item's `later` row). A 409 means it is not in the queue any
    /// more (handled before, its to-do ended, or 7 days).
    func laterHandled(_ id: String) async throws {
        _ = try await post("/api/v1/later/\(Self.pathPart(id))/handled")
    }

    private static func pathPart(_ id: String) -> String {
        id.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed.subtracting(CharacterSet(charactersIn: "/?#"))) ?? id
    }

    /// The conversation's Today card for the user's day (made on the first call of the day).
    func today(conversation: String) async throws -> FeedItem {
        let id = conversation.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? conversation
        return try await post("/api/v1/assistant/conversations/\(id)/today", as: ItemResponse.self).item
    }

    struct TrustKind: Decodable, Hashable, Sendable {
        var key: String
        var machine: String
        var cwd: String
        var readonly: Bool
    }
    private struct TrustResponse: Decodable { var kinds: [TrustKind] }

    /// The kinds of tasks remembered with Start These Directly.
    func taskTrust() async throws -> [TrustKind] { try await get("/api/v1/task-trust", as: TrustResponse.self).kinds }

    func forgetTrust(_ key: String) async throws {
        _ = try await request("DELETE", "/api/v1/task-trust", query: ["key": key])
    }

    /// Sends text straight to a task's agent; the answer is the conversation's new `routed` item.
    func route(conversation: String, todoID: String, text: String) async throws -> FeedItem {
        struct Body: Encodable {
            var todoID: String
            var text: String
            enum CodingKeys: String, CodingKey { case text, todoID = "todo_id" }
        }
        let id = conversation.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? conversation
        return try await post("/api/v1/assistant/conversations/\(id)/route", body: Body(todoID: todoID, text: HubNaming.outbound(text, canonical: personaNaming)), as: ItemResponse.self).item
    }

    nonisolated(unsafe) private static let feedTime: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return f
    }()
}
