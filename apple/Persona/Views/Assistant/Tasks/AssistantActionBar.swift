import SwiftUI

// The row above the assistant's composer on iPhone, iPad and Mac (#3393, docs/assistant/unified-entry.md "Needs you and In
// progress: each thing once"): two entries, Needs You and In Progress, each thing in exactly one of them. The split is
// `AssistantEntries`, a port of the web's `assistantEntries` (web/src/lib/assistant-entries.ts, Android's
// AssistantEntries.kt, #3387) replaying the shared fixture spec/fixtures/entries/cases.json. Each entry opens a tray above
// the bar, one at a time; the entries are drawn as the old dock's capsules (`DockCapsule`), so the bar is as low.

/// One of the bar's entries, and the tray it opens.
nonisolated enum ActionTray: String, CaseIterable, Identifiable, Sendable {
    case needsYou = "needs-you", running

    var id: String { rawValue }
}

/// Which entry each task, Agent, plan and message card goes to (#3387). A port of the web's `assistantEntries`; a change
/// to the rule changes all three platforms and the shared fixture.
nonisolated enum AssistantEntries {
    /// A task's status as the web's `todoStatus` reads it (first that holds wins).
    enum Status: String, Sendable {
        case done, needsYou = "needs_you", interrupted, usageLimit = "usage_limit", unread, working, waiting
        case followUp = "follow_up", notStarted = "not_started", failed

        /// Where it sorts (the web's `TODO_STATUS_RANK`): what the user must act on first.
        var rank: Double {
            switch self {
            case .needsYou: 0
            case .interrupted: 1
            case .usageLimit: 1.5
            case .failed: 3
            case .working: 4
            case .waiting: 4.5
            case .notStarted: 5
            case .unread: 6
            case .followUp: 7
            case .done: 8
            }
        }

        /// The app's task state for the row's mark and words.
        var taskState: TaskState {
            switch self {
            case .done: .done
            case .needsYou: .needsYou
            case .interrupted, .usageLimit, .followUp: .followup
            case .unread: .unread
            case .working: .working
            case .waiting: .waiting
            case .notStarted: .notStarted
            case .failed: .failed
            }
        }
    }

    enum Row: Equatable, Sendable, Identifiable {
        /// A task: its status and how many agents its row carries (they are never rows of their own).
        case task(id: String, status: Status, agents: Int)
        /// An Agent that belongs to no task (the main conversation's only).
        case agent(DockAgent)
        /// A plan waiting for Start, one row whatever its task count.
        case plan(id: String)
        /// A message card waiting for Send: its feed item, and the inbox item the reply goes to.
        case message(feedID: String, itemID: String)

        /// The fixture's row key.
        var id: String {
            switch self {
            case .task(let id, _, _): "task:" + id
            case .agent(let a): "agent:\(a.key.machineID)/\(a.key.sessionID)"
            case .plan(let id): "plan:" + id
            case .message(let feedID, _): "message:" + feedID
            }
        }
    }

    struct Result: Equatable, Sendable {
        var needsYou: [Row] = []
        var running: [Row] = []
        /// Open tasks with no progress for 48 hours and nothing running: folded under In Progress, not counted.
        var shelved: [Row] = []
    }

    /// A task's status, failed too when only its plan says the start failed (a hub from before #1077): the web's
    /// `entryStatus` over `todoStatus`.
    static func status(_ todo: TodoItem, lookup: TodosLogic.SessionLookup?, plans: [String: TaskPlan]) -> Status {
        let st = todoStatus(todo, lookup: lookup)
        guard st == .notStarted, todo.agents.isEmpty, let plan = todo.planID.flatMap({ plans[$0] }),
              let error = plan.tasks.first(where: { $0.todoID == todo.id })?.error, !error.isEmpty else { return st }
        return .failed
    }

    static func todoStatus(_ todo: TodoItem, lookup: TodosLogic.SessionLookup?) -> Status {
        if todo.failedToStart { return .failed }
        if todo.doneAt != nil { return .done }
        let active = todo.activeAgents
        if active.isEmpty { return .notStarted }
        var interrupted = false, limit = false, unread = false, working = false, waiting = false
        for a in active {
            if let e = a.startError, !e.isEmpty { continue } // a failed start counts as stopped
            let s = lookup?(a.machineID, a.sessionID)
            let st = s?.state ?? .offline
            if st == .needsYou { return .needsYou }
            let stopped = st == .unread || st == .idle
            let error = !(s?.turnError ?? "").isEmpty
            if error, s?.turnErrorKind == "usage_limit", stopped { limit = true }
            else if error, stopped { interrupted = true }
            else if st == .unread { unread = true }
            else if s?.waitsToBeWoken == true { waiting = true }
            else if st == .working || s?.starting == true { working = true }
        }
        if interrupted { return .interrupted }
        if limit { return .usageLimit }
        if unread { return .unread }
        if working { return .working }
        if waiting { return .waiting }
        return .followUp
    }

    /// The open ask, as the web reads it (`openAsk`): one with a verdict and a time.
    static func openAsk(_ todo: TodoItem) -> TodoAsk? {
        guard let a = todo.ask, !(a.verdict ?? "").isEmpty, !a.askedAt.isEmpty else { return nil }
        return a
    }

    /// It waits for the user's review while nothing of it runs or asks (the web's `awaitsReview`): rank 2.
    static func rank(_ todo: TodoItem, _ status: Status) -> Double {
        let resting = status == .followUp || status == .unread || status == .notStarted
        return resting && (todo.reviewState == .pendingReview || openAsk(todo) != nil) ? 2 : status.rank
    }

    /// The task waits for the user's decision or review: an open ask (a decided one only while the task is open), or its
    /// supervisor's `pending_review` / `needs_user` on an open task (the web's `taskAwaitsDecision`).
    static func awaitsDecision(_ todo: TodoItem) -> Bool {
        if let ask = openAsk(todo) { return !(todo.doneAt != nil && ask.outcome == "decide") }
        let state = todo.supervision?.state
        return todo.doneAt == nil && (state == "pending_review" || state == "needs_user")
    }

    /// The message card still waits for Send by the hub's fields alone: not sent, not filed (the web's `messageWaits`).
    static func messageWaits(_ item: FeedItem) -> Bool {
        item.kind == .message && item.ref != nil && item.sentAt == nil && item.filedAt == nil
    }

    /// Splits a conversation's things into the two entries (the web's `assistantEntries`). `main`: the main
    /// conversation's id (it watches every task and the Agents opened by hand); `messageWaits`: whether a message card
    /// still waits for Send (the app adds the inbox's word and Later); `restored`: tasks brought back from shelved;
    /// `going`: the agents going on (`AppModel.goingAgents`), else read from `sessions`, which then must be every session.
    @MainActor static func split(conversation: String, main: String?, group: String = "", todos: [TodoItem], sessions: [Session],
                                 interactions: [Interaction], plans: [String: TaskPlan], feed: [FeedItem], now: Date = .now,
                                 restored: [String: Date] = [:], going: [DockAgent]? = nil,
                                 messageWaits: (FeedItem) -> Bool = AssistantEntries.messageWaits) -> Result {
        let lookup = TodosLogic.makeSessionLookup(sessions)
        let isMain = !(main ?? "").isEmpty && main == conversation
        // A function, not a closure value: Swift 6.2.4 (Xcode 26.3) rejects capturing a closure value in the `listed`
        // closure below ("sending 'pair' risks causing data races").
        func pair(_ m: String, _ s: String) -> String { m + "/" + s }
        var asksOf: [String: [Interaction]] = [:]
        for ix in interactions.sorted(by: { ($0.createdAt ?? .distantPast, $0.id) < ($1.createdAt ?? .distantPast, $1.id) }) {
            asksOf[pair(ix.machineID, ix.sessionID), default: []].append(ix)
        }

        // The conversation's tasks (the web's `taskCardSlots`): the main conversation's every to-do, another's the ones it
        // dispatched, plus those its plans and feed name. `at` is when its card came: its plan's, else the to-do's.
        var at: [String: Date] = [:]
        for plan in plans.values.sorted(by: { $0.id < $1.id }) where plan.conversation == conversation {
            for t in plan.tasks { if let id = t.todoID, at[id] == nil { at[id] = plan.createdAt ?? .distantPast } }
        }
        let byID = Dictionary(todos.map { ($0.id, $0) }, uniquingKeysWith: { a, _ in a })
        for item in feed where [.task, .receipt, .review, .report].contains(item.kind) {
            if let id = item.ref, at[id] == nil { at[id] = byID[id]?.createdAt ?? item.at }
        }
        for todo in todos where (isMain || todo.origin == conversation) && at[todo.id] == nil {
            at[todo.id] = todo.createdAt ?? .distantPast
        }

        typealias Ranked = (row: Row, rank: Double, at: Date)
        /// The agents of the tasks listed as rows: folded into them, never rows of their own, whoever made the to-do.
        var folded = Set<String>()
        var asking: [(row: Row, first: Date?)] = []
        var deciding: [Ranked] = [], open: [Ranked] = [], shelved: [Ranked] = []
        // failed starts whose work finished in a related to-do: closed, as the To-dos page has them (#3713)
        let superseded = TodoTriageLogic.supersededFailedStarts(todos)
        for todo in todos {
            guard let when = at[todo.id] else { continue }
            let status = status(todo, lookup: lookup, plans: plans)
            let active = todo.doneAt != nil ? [] : todo.activeAgents
            let asks = active.flatMap { asksOf[pair($0.machineID, $0.sessionID)] ?? [] }
            let row = Row.task(id: todo.id, status: status, agents: active.count)
            let listed = { folded.formUnion(active.map { pair($0.machineID, $0.sessionID) }) }
            let bucket = TodoTriageLogic.bucket(todo, lookup: lookup, now: now, restoredAt: restored[todo.id],
                                                superseded: superseded.contains(todo.id))
            // a failed start carries done_at (the hub ends it), yet it is the user's to retry while its bucket says open
            let waits = status == .failed ? bucket == .open : todo.doneAt == nil && (status == .needsYou || status == .interrupted)
            if !asks.isEmpty { asking.append((row, asks.map { $0.createdAt ?? .distantPast }.min())); listed() }
            else if waits || awaitsDecision(todo) { deciding.append((row, rank(todo, status), when)); listed() }
            else if bucket == .open { open.append((row, rank(todo, status), when)); listed() }
            else if bucket == .shelved { shelved.append((row, rank(todo, status), when)); listed() }
        }

        // Agents that belong to no task, the main conversation's only: asking ones wait on the user, working ones run. The
        // web's `dockAgents` leaves out the agents of conversations' tasks; an agent linked to a to-do made by hand is that
        // to-do's row here too (the main conversation lists every to-do), so it is left out as well: one thing, one row.
        let taskAgents = Set(todos.filter { !($0.origin ?? "").isEmpty && $0.doneAt == nil }.flatMap { t in
            t.activeAgents.map { SessionKey(group: group, machineID: $0.machineID, sessionID: $0.sessionID) }
        })
        let manual = Set(todos.filter { ($0.origin ?? "").isEmpty }.map(\.id))
        let dock = DockAgent.shown(going ?? DockAgent.going(sessions), conversation: conversation, main: main, group: group,
                                   taskAgents: taskAgents, manualTodos: manual)
        var running: [Row] = []
        for agent in dock where !folded.contains(pair(agent.key.machineID, agent.key.sessionID)) {
            if agent.isWaiting {
                asking.append((.agent(agent), asksOf[pair(agent.key.machineID, agent.key.sessionID)]?.first?.createdAt))
            } else {
                running.append(.agent(agent))
            }
        }

        let waitingPlans = plans.values.filter { $0.conversation == conversation && $0.state == .open }
            .sorted { ($0.createdAt ?? .distantPast, $0.id) < ($1.createdAt ?? .distantPast, $1.id) }
            .map { Row.plan(id: $0.id) }
        let messages = feed.filter(messageWaits).compactMap { item in item.ref.map { Row.message(feedID: item.id, itemID: $0) } }
        // stable sorts, as the web's: equal rows keep the order they came in; no time is the earliest (`byTime`)
        func stable<T>(_ xs: [T], _ before: (T, T) -> Bool?) -> [T] {
            xs.enumerated().sorted { a, b in before(a.element, b.element) ?? (a.offset < b.offset) }.map(\.element)
        }
        let newest = { (a: Ranked, b: Ranked) -> Bool? in a.rank != b.rank ? a.rank < b.rank : (a.at != b.at ? a.at > b.at : nil) }
        let epoch = Date(timeIntervalSince1970: 0)
        let byAsk = stable(asking) { a, b in
            let x = a.first ?? epoch, y = b.first ?? epoch
            return x != y ? x < y : nil
        }.map(\.row)
        return Result(needsYou: byAsk + waitingPlans + stable(deciding, newest).map(\.row) + messages,
                      running: stable(open, newest).map(\.row) + running,
                      shelved: stable(shelved, newest).map(\.row))
    }
}

/// What the two trays list: the split, and in front of Needs You the conversation's own cards (Persona asking, an
/// approval), which the screen adds as the web does.
nonisolated struct ActionTrayContent: Equatable, Sendable {
    /// A card the conversation itself shows, answered below the bar.
    struct Ask: Equatable, Sendable, Identifiable {
        var id: String
        var title: String
    }

    enum Row: Equatable, Sendable, Identifiable {
        case ask(Ask)
        case entry(AssistantEntries.Row)

        var id: String {
            switch self {
            case .ask(let a): "ask:" + a.id
            case .entry(let r): r.id
            }
        }
    }

    /// Needs You's rows from one supervising conversation (docs/apple/ios.md "Background rounds").
    struct NeedsYouGroup: Equatable, Sendable, Identifiable {
        /// The supervising conversation's id; "" for the conversation's own group.
        var id: String
        /// Its header; nil for the conversation's own group, which comes first.
        var title: String?
        var rows: [Row]
    }

    var needsYou: [Row] = []
    var running: [Row] = []
    /// Folded under In Progress, not counted.
    var shelved: [AssistantEntries.Row] = []
    /// Needs You's rows grouped by supervising conversation (`grouped`); empty when the tray is one list, as it always
    /// was. Grouping never changes the rows or the count.
    var needsYouGroups: [NeedsYouGroup] = []

    init() {}

    init(_ entries: AssistantEntries.Result, asks: [Ask] = []) {
        needsYou = asks.map(Row.ask) + entries.needsYou.map(Row.entry)
        running = entries.running.map(Row.entry)
        shelved = entries.shelved
    }

    func rows(_ tray: ActionTray) -> [Row] {
        switch tray {
        case .needsYou: needsYou
        case .running: running
        }
    }

    /// The number its entry shows: its rows (shelved ones are not counted).
    func count(_ tray: ActionTray) -> Int { rows(tray).count }

    /// Groups Needs You (`grouped`) when `enabled` (the main conversation folding background rounds); otherwise one list.
    mutating func groupNeedsYou(_ enabled: Bool, conversation: String, origin: (String) -> String?, title: (String) -> String?) {
        needsYouGroups = enabled ? Self.grouped(needsYou, conversation: conversation, origin: origin, title: title) : []
    }

    /// Needs You's rows by the conversation that supervises each task (its `origin`), the conversation's own group first
    /// and the others in the order their first row comes. Rows that are not tasks (asks, agents, plans, messages) and
    /// tasks without another origin are the conversation's own. Within a group rows keep their order. Empty with only
    /// one group: the tray then looks as it always did. `title` names another conversation; "Another conversation"
    /// when it has no name.
    static func grouped(_ rows: [Row], conversation: String, origin: (String) -> String?,
                        title: (String) -> String?) -> [NeedsYouGroup] {
        var order: [String] = [""]
        var byGroup: [String: [Row]] = [:]
        for row in rows {
            var group = ""
            if case .entry(.task(let id, _, _)) = row, let o = origin(id), !o.isEmpty, o != conversation { group = o }
            if byGroup[group] == nil, !group.isEmpty { order.append(group) }
            byGroup[group, default: []].append(row)
        }
        let ids = order.filter { byGroup[$0]?.isEmpty == false }
        guard ids.count > 1 else { return [] }
        return ids.map { id -> NeedsYouGroup in
            guard !id.isEmpty else { return NeedsYouGroup(id: id, title: nil, rows: byGroup[id] ?? []) }
            let name = title(id)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
            return NeedsYouGroup(id: id, title: name.isEmpty ? String(localized: "Another conversation") : name, rows: byGroup[id] ?? [])
        }
    }
}

extension ActionTray {
    var title: String {
        switch self {
        case .needsYou: String(localized: "Needs You [Action Bar]")
        case .running: String(localized: "In Progress [Action Bar]")
        }
    }

    var empty: String {
        switch self {
        case .needsYou: String(localized: "All Caught Up")
        case .running: String(localized: "Nothing in progress right now")
        }
    }
}

/// The bar and the tray open over it.
struct AssistantActionBar: View {
    var conversation: String
    /// The conversation's own session: its cards (Persona asking, an approval) wait in Needs You.
    var key: SessionKey? = nil
    /// The conversation's model operating a Mac (#1343): its pill after the entries.
    var computer: ComputerActivity? = nil
    @State private var open: ActionTray?
    /// The open tray's rows, measured: the tray is as tall as they are, up to its cap.
    @State private var listHeight: CGFloat = 0
    @State private var removing: TodoItem?
    private var store: TasksStore { .shared }

    var body: some View {
        let content = Self.content(conversation, key: key)
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            if let open {
                tray(open, content)
                    .transition(.move(edge: .bottom).combined(with: .opacity))
            }
            HStack(spacing: Theme.Space.s) {
                ForEach(ActionTray.allCases) { entry($0, count: content.count($0)) }
                if ComputerActivityPill.shows(computer) {
                    ComputerActivityPill(activity: computer).background(Theme.pageBackground, in: .capsule) // as the entries
                }
            }
            .padding(.horizontal, Theme.Space.xs)
            #if !os(macOS)
            .padding(.top, Theme.Space.s)
            #endif
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        // No fade of its own: the chat runs on behind the bar and fades out there (`FooterFog`, #3696).
        .onChange(of: conversation) { open = nil }
        #if DEBUG
        .onAppear {
            if let value = UserDefaults.standard.string(forKey: "showActionTray"), let tray = ActionTray(rawValue: value) {
                open = tray
            }
        }
        #endif
        .confirmTaskRemoval($removing)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("action-bar")
    }

    static func content(_ conversation: String, key: SessionKey? = nil) -> ActionTrayContent {
        let store = TasksStore.shared
        let model = AppModel.shared
        let todos = store.todos.values.sorted { ($0.createdAt ?? .distantPast, $0.id) > ($1.createdAt ?? .distantPast, $1.id) }
        // Only the tasks' own agents, which is all a task's status reads, and the agents going on as the dock had them: the
        // bar must not follow every agent's push (AllSessionsReadersTests, #3446).
        let entries = AssistantEntries.split(
            conversation: conversation, main: model.assistantMain?.sessionID, group: model.hub?.group ?? "", todos: todos,
            sessions: todos.filter { $0.doneAt == nil || $0.failedToStart }.flatMap { store.sessions($0) },
            interactions: model.pendingInteractions, plans: store.plans,
            feed: store.feed(conversation), restored: TodoRestores.load(), going: model.goingAgents,
            // the hub's fields, the inbox's word on the item, and Later
            messageWaits: { item in
                guard item.kind == .message, let id = item.ref, !store.laterMessages.contains(item.id) else { return false }
                return store.messageState(item, itemID: id) == .open
            })
        let asks = (key.map { model.interactions(for: $0) } ?? []).sorted { ($0.createdAt ?? .distantPast) < ($1.createdAt ?? .distantPast) }
        var content = ActionTrayContent(entries, asks: asks.map { .init(id: $0.id, title: $0.askLine(includeToolName: false) ?? $0.title) })
        // The main conversation, while it folds background rounds, groups Needs You by supervising conversation.
        content.groupNeedsYou(conversation == model.assistantMain?.sessionID && model.mainBackgroundFolded,
                              conversation: conversation, origin: { store.todos[$0]?.origin },
                              title: { id in
                                  if let name = model.directionNames[id] { return name }
                                  let title = model.assistantConfig?.conversations.first(where: { $0.sessionID == id })?.title
                                  return title == "Claude Code" ? nil : title
                              })
        return content
    }

    private func entry(_ tray: ActionTray, count: Int) -> some View {
        let selected = open == tray
        // Needs You with something in it takes the waiting pill's tint; empty, it is as quiet as In Progress.
        let look: DockCapsule.Look = selected ? .selected : (tray == .needsYou && count > 0 ? .waiting(.needsYou) : .plain)
        return Button {
            Haptics.tap()
            withAnimation(Theme.Motion.state) { open = selected ? nil : tray }
        } label: {
            HStack(spacing: Theme.Space.xs) {
                Text(tray.title)
                if count > 0 { Text(count > 99 ? "99+" : "\(count)").monospacedDigit() }
            }
            .dockCapsule(look)
            // On the page under its see-through fill: the chat fading out behind the row does not read through the
            // words (#3696); with nothing behind, the same pixels as before.
            .background(Theme.pageBackground, in: .capsule)
            .fixedSize()
        }
        .buttonStyle(.plain)
        .accessibilityAddTraits(selected ? .isSelected : [])
        .accessibilityIdentifier("action-bar.\(tray.rawValue)")
    }

    private func tray(_ tray: ActionTray, _ content: ActionTrayContent) -> some View {
        let rows = content.rows(tray)
        return VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: Theme.Space.s) {
                Text(tray.title).font(Theme.Font.sectionHeader).foregroundStyle(.secondary).lineLimit(1)
                Spacer(minLength: Theme.Space.s)
                // Finished things are in neither tray: the board has everything.
                Button {
                    open = nil
                    store.showBoard = true
                } label: {
                    Text("All").dockCapsule().fixedSize()
                }
                .buttonStyle(.plain)
                .accessibilityIdentifier("action-tray.board")
                Button {
                    withAnimation(Theme.Motion.state) { open = nil }
                } label: {
                    ThemedIcon(Theme.Icon.close).font(Theme.Font.labelStrong).foregroundStyle(.secondary)
                        .scaledFrame(width: Theme.Size.badge, height: Theme.Size.badge)
                        .contentShape(.circle)
                }
                .buttonStyle(.plain)
                .accessibilityLabel("Close")
                .accessibilityIdentifier("action-tray.close")
            }
            .padding(.horizontal, Theme.Space.m)
            .padding(.vertical, Theme.Space.s)
            if rows.isEmpty && (tray == .needsYou || content.shelved.isEmpty) {
                Text(tray.empty).font(Theme.Font.caption).foregroundStyle(.secondary).lineLimit(2)
                    .padding(.horizontal, Theme.Space.m)
                    .padding(.bottom, Theme.Space.m)
                    .accessibilityIdentifier("action-tray.empty")
            } else {
                // As tall as its rows; past the cap they scroll inside the tray, the bar stays where it is.
                ScrollView {
                    VStack(alignment: .leading, spacing: 0) {
                        if tray == .needsYou, !content.needsYouGroups.isEmpty {
                            ForEach(content.needsYouGroups) { group in
                                if let title = group.title {
                                    // The tray's own header style: a group reads as a section of the tray.
                                    Text(title).font(Theme.Font.sectionHeader).foregroundStyle(.secondary).lineLimit(1)
                                        .padding(.horizontal, Theme.Space.m)
                                        .padding(.top, Theme.Space.s)
                                        .accessibilityAddTraits(.isHeader)
                                        .accessibilityIdentifier("action-tray.group")
                                }
                                ForEach(group.rows) { row($0) }
                            }
                        } else {
                            ForEach(rows) { row($0) }
                        }
                        if tray == .running, !content.shelved.isEmpty { shelvedLine(content.shelved.count) }
                    }
                    .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { listHeight = $0 }
                }
                .scrollBounceBehavior(.basedOnSize)
                .frame(height: min(listHeight, Theme.Size.box280))
                .padding(.bottom, Theme.Space.xs)
            }
        }
        .surface(in: .rect(cornerRadius: Theme.Radius.panel, style: .continuous))
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("action-tray.\(tray.rawValue)")
    }

    @ViewBuilder private func row(_ row: ActionTrayContent.Row) -> some View {
        switch row {
        case .ask(let ask):
            // The card itself is right below the bar: the row closes the tray to show it.
            Button {
                withAnimation(Theme.Motion.state) { open = nil }
            } label: {
                line(title: ask.title, detail: StateName.needsYou.text, waiting: true) {
                    ThemedIcon(Theme.Icon.askFirst).font(Theme.Font.labelStrong).foregroundStyle(AgentState.needsYou.tone.text)
                }
            }
            .buttonStyle(.plain)
            .accessibilityIdentifier("action-tray.ask")
        case .entry(let entry):
            self.entry(entry)
        }
    }

    @ViewBuilder private func entry(_ row: AssistantEntries.Row) -> some View {
        switch row {
        case .task(let id, let status, let agents):
            if let todo = store.todos[id] {
                let state = status.taskState
                let activity = AssistantTrayActivity(sessions: store.sessions(todo))
                if let ask = todo.openAsk, todo.doneAt == nil {
                    BriefAcceptRow(row: ask.row(for: todo), group: AppModel.shared.hub?.group ?? "")
                        .padding(.horizontal, Theme.Space.m)
                } else {
                    Button {
                        open = nil
                        store.detailTask = id
                    } label: {
                        line(title: todo.title,
                             detail: activity.isActive && status == .waiting ? StateName.working.text : detail(todo, status: status, agents: agents),
                             waiting: open == .needsYou, activity: activity) {
                            if open != .needsYou && (activity.isActive || status == .working) {
                                WorkingIndicator().accessibilityIdentifier("action-tray.working-mark")
                            } else if status == .waiting {
                                ThemedIcon(Theme.Icon.backgroundWaiting).font(Theme.Font.labelStrong)
                                    .foregroundStyle(StatusPalette.backgroundWaiting.dot)
                                    .accessibilityIdentifier("action-tray.waiting-mark")
                            } else {
                                TaskMark(state: state)
                            }
                        }
                    }
                    .buttonStyle(.plain)
                    .accessibilityIdentifier("action-tray.task")
                    .contextMenu { TaskRemoveMenuItem(todo: todo, state: state, pending: $removing) }
                }
            }
        case .agent(let agent):
            Button {
                open = nil
                store.detailAgent = agent.key
            } label: {
                line(title: agent.title,
                     detail: agent.isWaiting ? StateName.needsYou.text : (agent.backgroundWaiting && !agent.activity.isActive ? StateName.taskWaiting.text : StateName.working.text),
                     waiting: agent.isWaiting, activity: agent.activity) {
                    if !agent.isWaiting && (agent.activity.isActive || !agent.backgroundWaiting) {
                        WorkingIndicator().accessibilityIdentifier("action-tray.working-mark")
                    } else {
                        StatusDot(state: agent.state, size: Theme.Size.dotL, waiting: agent.backgroundWaiting)
                            .accessibilityIdentifier(agent.backgroundWaiting ? "action-tray.waiting-mark" : "action-tray.state-mark")
                    }
                }
            }
            .buttonStyle(.plain)
            .accessibilityIdentifier("action-tray.agent")
        case .plan(let id):
            // the plan card itself: Start, Edit and Don't Start are right here
            if let plan = store.plans[id] {
                PlanCard(plan: plan)
                    .padding(.horizontal, Theme.Space.s)
                    .padding(.bottom, Theme.Space.s)
                    .accessibilityIdentifier("action-tray.plan")
            }
        case .message(let feedID, let itemID):
            // the message card itself: Send, Edit and Later are right here
            if let item = store.feed(conversation).first(where: { $0.id == feedID }) {
                MessageCard(item: item, itemID: itemID)
                    .padding(.horizontal, Theme.Space.s)
                    .padding(.bottom, Theme.Space.s)
                    .accessibilityIdentifier("action-tray.message")
            }
        }
    }

    /// The open tasks with no progress for 48 hours: one quiet line under In Progress, not counted; the board lists them.
    private func shelvedLine(_ count: Int) -> some View {
        Button {
            open = nil
            store.showBoard = true
        } label: {
            line(title: String(localized: "Shelved"), detail: "\(count)", waiting: false) {
                ThemedIcon(Theme.Icon.disclosure).font(Theme.Font.labelStrong).foregroundStyle(.tertiary)
            }
            .foregroundStyle(.secondary)
        }
        .buttonStyle(.plain)
        .accessibilityIdentifier("action-tray.shelved")
    }

    /// A task row's words: its status, what a waiting task waits for (#3274), the review it waits for when its status
    /// does not say, and how many agents it has when more than one (they are listed in its details, never as rows).
    private func detail(_ todo: TodoItem, status: AssistantEntries.Status, agents: Int) -> String {
        let review = todo.reviewState
        let resting = status == .followUp || status == .unread || status == .notStarted
        var parts: [String]
        if status == .interrupted { parts = [String(localized: "Interrupted [Chat Layout]")] }
        else if status == .usageLimit { parts = [String(localized: "Usage Limit")] }
        else if resting, review == .pendingReview || review == .needsUser { parts = [todo.reviewBadge?.label ?? status.taskState.label] }
        else { parts = [status.taskState.label] }
        if status == .waiting, let waits = store.waitLine(todo) { parts.append(waits) }
        if agents > 1 { parts.append(String(localized: "\(agents) agents")) }
        return parts.joined(separator: " · ")
    }

    private func line<Mark: View>(title: String, detail: String, waiting: Bool,
                                  activity: AssistantTrayActivity = .init(), @ViewBuilder mark: () -> Mark) -> some View {
        HStack(spacing: Theme.Space.s) {
            mark().scaledFrame(width: Theme.Size.markM)
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                HStack(spacing: Theme.Space.s) {
                    Text(title).font(Theme.Font.body).lineLimit(1)
                    Spacer(minLength: Theme.Space.s)
                    Text(detail).font(Theme.Font.caption).lineLimit(1).fixedSize()
                        .foregroundStyle(waiting ? AnyShapeStyle(AgentState.needsYou.tone.text) : AnyShapeStyle(.secondary))
                }
                if activity.isActive {
                    AssistantTrayActivityLine(activity: activity)
                }
            }
        }
        .padding(.horizontal, Theme.Space.m)
        .padding(.vertical, Theme.Space.s)
        .contentShape(.rect)
        .accessibilityElement(children: .combine)
    }
}

extension AssistantTrayActivity {
    /// Workflow phase first, with other work on a separate line when activity is mixed.
    var subtitle: String { [workflowSubtitle, otherSubtitle].filter { !$0.isEmpty }.joined(separator: " · ") }

    var workflowSubtitle: String {
        guard workflowCount > 0 else { return "" }
        return ([String(localized: "Background workflow")] + (workflows.first.map { [$0.phase ?? $0.title] } ?? []))
            .joined(separator: " · ")
    }

    var otherSubtitle: String {
        var parts: [String] = []
        if backgroundTasks > 0 { parts.append(String(localized: "\(backgroundTasks) background tasks running")) }
        if subagents > 0 { parts.append(String(localized: "\(subagents) subagents running")) }
        if parts.isEmpty, workflowCount == 0, reportedBackgroundWork { return String(localized: "Working (Background)") }
        return parts.joined(separator: " · ")
    }
}

/// A tray's activity subtitle: even a long phase cannot cut the workflow's done/started counts.
struct AssistantTrayActivityLine: View {
    var activity: AssistantTrayActivity

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.xxs) {
            if activity.workflowCount > 0 {
                HStack(spacing: Theme.Space.xs) {
                    Text(activity.workflowSubtitle).lineLimit(1).truncationMode(.tail)
                    if let counts = activity.workflows.first?.counts {
                        Text("· " + counts).monospacedDigit().lineLimit(1).fixedSize()
                            .layoutPriority(1)
                            .accessibilityIdentifier("action-tray.workflow-progress")
                    }
                    if activity.workflowCount > 1 {
                        Text(String(localized: " · and \(activity.workflowCount - 1) more")).lineLimit(1).fixedSize()
                    }
                }
            }
            if !activity.otherSubtitle.isEmpty {
                Text(activity.otherSubtitle).lineLimit(1)
            }
        }
        .font(Theme.Font.caption)
        .foregroundStyle(.secondary)
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("action-tray.activity")
    }
}
