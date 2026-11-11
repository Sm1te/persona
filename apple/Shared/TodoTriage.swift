import Foundation

// The To-dos page's sections (#3206, docs/assistant/todo-status-sop.md): each to-do's place comes from real signals only —
// its agents' sessions (alive, running, asking), its PR / delivery state, the latest task_review verdict and when it last
// moved — never from what an agent says about itself. The rules are the SOP's decision table, in its order; the first that
// holds wins. Mirrors web/src/lib/todo-triage.ts (and Android's TodoTriage.kt): a change to the rules changes all three.

nonisolated enum TodoSection: String, CaseIterable, Sendable {
    /// Something only the owner can move: an answer, an approval, a decision, a retry.
    case waiting
    /// An agent is running on it now.
    case working
    /// Closed, accepted or deployed in the last 24 hours, or a turn nobody has seen / a review not done yet.
    case justDone = "just_done"
    /// Its work stopped at a pull request that is still open and nothing runs.
    case pr
    /// Open, recently active, none of the above.
    case other
    /// A task with no activity for 48 hours and nothing running.
    case shelved
    /// Closed, accepted or deployed more than 24 hours ago.
    case done

    /// Other open, Shelved and Done start folded.
    var startsFolded: Bool { self == .other || self == .shelved || self == .done }

    /// The section's header (the web's `todos.section_*`).
    var title: String {
        switch self {
        case .waiting: StateName.needsYou.text
        case .working: StateName.working.text
        case .justDone: String(localized: "Just Finished")
        case .pr: String(localized: "PR to Merge")
        case .other: String(localized: "Other Open")
        case .shelved: String(localized: "Shelved")
        case .done: String(localized: "Done [To-do Sections]")
        }
    }
}

/// Open counts, shelved folds (no activity for 48 h, nothing running), closed is finished (`TodoTriageLogic.bucket`).
nonisolated enum TodoBucket: String, Sendable {
    case open, shelved, closed
}

/// Which SOP rule placed a to-do; the section follows from it.
nonisolated enum TodoTriageRule: String, Sendable {
    case failedStart = "failed_start", superseded, agentAsks = "agent_asks", ownerDecides = "owner_decides", agentRunning = "agent_running"
    case closed, accepted, deployed, finishedUnseen = "finished_unseen", prOpen = "pr_open", stale, rest
}

nonisolated struct TodoTriage: Equatable, Sendable {
    var section: TodoSection
    var rule: TodoTriageRule
    /// The last moment anything of it moved (distantPast when nothing says).
    var last: Date
}

/// One row of the list: related to-dos merged, led by the most urgent member.
nonisolated struct TodoTriageGroup: Identifiable, Sendable {
    /// The lead's id: stable while the lead stays.
    var key: String
    var section: TodoSection
    var lead: TodoItem
    /// Every member, the lead first, then by section and recency.
    var members: [TodoItem]
    /// Each member's triage, by id.
    var triage: [String: TodoTriage]
    var last: Date

    var id: String { key }
    /// The others under the lead ("N related").
    var related: [TodoItem] { Array(members.dropFirst()) }
}

nonisolated struct TodoTriageResult: Sendable {
    var sections: [TodoSection: [TodoTriageGroup]]
    /// To-dos (not rows) per section.
    var todoCounts: [TodoSection: Int]
    /// What the header calls open (`countsAsOpen`), shelved, and finished (closed, accepted or deployed).
    var open: Int
    var shelved: Int
    var finished: Int

    func groups(_ section: TodoSection) -> [TodoTriageGroup] { sections[section] ?? [] }
    var isEmpty: Bool { sections.values.allSatisfy(\.isEmpty) }
}

nonisolated enum TodoTriageLogic {
    /// No activity for this long, nothing of it running: shelved.
    static let shelveAfter: TimeInterval = 48 * 3600
    /// Finished this recently: Just finished; earlier, Done.
    static let justDone: TimeInterval = 24 * 3600

    /// How a to-do's linked agents stand, the way the web's `todoStatus` reads them: an open ask does not count here
    /// (rules 3 and 8 read it), unlike `TaskState(todo:agents:)`.
    enum AgentsStatus: Equatable, Sendable {
        case failed, done, notStarted, needsYou, interrupted, usageLimit, unread, working, followUp
    }

    static func agentsStatus(_ todo: TodoItem, lookup: TodosLogic.SessionLookup?) -> AgentsStatus {
        if todo.failedToStart { return .failed }
        if todo.doneAt != nil { return .done }
        let active = todo.activeAgents
        if active.isEmpty { return .notStarted }
        var interrupted = false, limit = false, unread = false, working = false
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
            else if st == .working || s?.starting == true { working = true }
        }
        if interrupted { return .interrupted }
        if limit { return .usageLimit }
        if unread { return .unread }
        if working { return .working }
        return .followUp
    }

    /// The last moment the to-do or any of its agents moved: its own times, its supervision's (reviews, reports), its
    /// delivery and ask, and each linked agent's session. A restore from Shelved counts as a move.
    static func lastActivity(_ todo: TodoItem, lookup: TodosLogic.SessionLookup?, restoredAt: Date? = nil) -> Date {
        let sup = todo.supervision
        var times: [Date?] = [todo.updatedAt, todo.createdAt, todo.reopenedAt, todo.doneAt, sup?.updatedAt, sup?.progress?.at,
                              sup?.latestReview?.at, todo.delivery?.at, todo.ask.flatMap { JSON.parseDate($0.askedAt) }, restoredAt]
        for a in todo.agents where a.releasedAt == nil {
            if let s = lookup?(a.machineID, a.sessionID) { times += [s.updatedAt, s.stateSince] }
        }
        return times.compactMap { $0 }.max() ?? .distantPast
    }

    /// The PR its work stopped at, still to merge: the hub says the delivery stopped at a PR, or it knows a PR and no
    /// delivery state past it.
    static func openPR(_ todo: TodoItem) -> Bool {
        let d = todo.delivery?.state ?? ""
        if d == "pr" { return true }
        return !(todo.prURL ?? "").isEmpty && (d.isEmpty || d == "none")
    }

    /// A task (an agent was linked, or a conversation handed it out) with no due date ahead. The user's own to-dos
    /// without agents — reminders, errands — never shelve themselves.
    static func shelvable(_ todo: TodoItem, now: Date, calendar: Calendar = .current) -> Bool {
        if let due = TodosLogic.dueBy(todo, calendar: calendar), due >= now { return false }
        return !todo.agents.isEmpty || !(todo.origin ?? "").isEmpty
    }

    /// The open ask, as the web reads it (`openAsk`): one with a verdict and a time.
    private static func openAsk(_ todo: TodoItem) -> TodoAsk? {
        guard let a = todo.ask, !(a.verdict ?? "").isEmpty, !a.askedAt.isEmpty else { return nil }
        return a
    }

    /// A to-do's section by the SOP's decision table, first match wins:
    ///
    ///  1. failedStart    the task's agent never started                    → Waiting on you, Done after 48 h;
    ///     superseded     … and a related to-do (its row) really finished   → Just finished (24 h), then Done (#3713)
    ///  2. agentAsks      an agent asks (question, approval) or was cut off → Waiting on you
    ///  3. ownerDecides   review says needs_user, or a decision / "cannot" ask → Waiting on you
    ///  4. agentRunning   an agent is working or starting                   → Working
    ///  5. closed         done_at set                                       → Just finished (24 h), then Done
    ///  6. accepted       the latest review accepted it (even reopened)     → Just finished (24 h), then Done
    ///  7. deployed       its delivery is live                              → Just finished (24 h), then Done
    ///  8. finishedUnseen an agent finished unseen, awaits review, or a done ask → Just finished
    ///  9. prOpen         its PR is open, nothing running                   → PR to merge (however old)
    /// 10. stale          a task with no activity for 48 h                  → Shelved
    /// 11. rest           anything else                                     → Other open
    static func triage(_ todo: TodoItem, lookup: TodosLogic.SessionLookup?, now: Date, restoredAt: Date? = nil,
                       superseded: Bool = false, calendar: Calendar = .current) -> TodoTriage {
        let last = lastActivity(todo, lookup: lookup, restoredAt: restoredAt)
        let status = agentsStatus(todo, lookup: lookup)
        let recent = { (at: Date) in now.timeIntervalSince(at) < justDone }
        let ask = openAsk(todo)
        let review = todo.reviewState
        func at(_ s: TodoSection, _ r: TodoTriageRule) -> TodoTriage { TodoTriage(section: s, rule: r, last: last) }

        if status == .failed {
            // the same piece of work finished in a related to-do (`supersededFailedStarts`): history, nothing to retry
            if superseded { return at(recent(todo.doneAt ?? last) ? .justDone : .done, .superseded) }
            return at(now.timeIntervalSince(last) < shelveAfter ? .waiting : .done, .failedStart)
        }
        if status == .needsYou || status == .interrupted { return at(.waiting, .agentAsks) }
        if todo.doneAt == nil, review == .needsUser || ask?.outcome == "decide" || ask?.outcome == "cannot" {
            return at(.waiting, .ownerDecides)
        }
        if status == .working { return at(.working, .agentRunning) }
        if let done = todo.doneAt { return at(recent(done) ? .justDone : .done, .closed) }
        if todo.acceptanceStands {
            return at(recent(todo.supervision?.latestReview?.at ?? .distantPast) ? .justDone : .done, .accepted)
        }
        if todo.delivery?.state == "live" { return at(recent(todo.delivery?.at ?? last) ? .justDone : .done, .deployed) }
        if status == .unread || review == .pendingReview || (ask != nil && (ask?.outcome ?? "").isEmpty) || ask?.outcome == "done" {
            return at(.justDone, .finishedUnseen)
        }
        if openPR(todo) { return at(.pr, .prOpen) }
        if shelvable(todo, now: now, calendar: calendar), now.timeIntervalSince(last) >= shelveAfter { return at(.shelved, .stale) }
        return at(.other, .rest)
    }

    /// Whether a to-do counts toward the header's open count: not closed, not accepted or deployed, not shelved. A
    /// finished turn nobody looked at yet (Just finished, still open) counts: only a review or the user closes a to-do.
    static func countsAsOpen(_ todo: TodoItem, _ t: TodoTriage) -> Bool {
        // a failed start has done_at too (the hub ends it), yet while it waits on the owner it is theirs to retry: open
        if t.rule == .failedStart { return t.section == .waiting }
        if todo.doneAt != nil || t.section == .shelved || t.section == .done { return false }
        return t.rule != .accepted && t.rule != .deployed
    }

    /// Whether a to-do's checkbox shows it open (the web's `checksAsOpen`): not closed, or a failed start still waiting
    /// on the owner (rule 1), which the hub closed itself and the page counts as open (#3713). Ticking an open one closes
    /// it (done: for a failed start the hub clears its start error, so it moves to Just finished); unticking a closed
    /// one reopens it.
    static func checksAsOpen(_ todo: TodoItem, _ t: TodoTriage?) -> Bool {
        if todo.doneAt == nil { return true }
        return todo.failedToStart && t?.rule == .failedStart && t?.section == .waiting
    }

    /// Where a to-do stands for every count of open work (#3331): the To-dos page's "Not done" and the assistant's
    /// Progress entry read this one rule, so the two numbers agree (the web's `todoBucket`).
    static func bucket(_ todo: TodoItem, lookup: TodosLogic.SessionLookup?, now: Date, restoredAt: Date? = nil,
                       superseded: Bool = false) -> TodoBucket {
        let t = triage(todo, lookup: lookup, now: now, restoredAt: restoredAt, superseded: superseded)
        if countsAsOpen(todo, t) { return .open }
        return t.section == .shelved ? .shelved : .closed
    }

    // MARK: Related to-dos

    /// The first issue number in a title, as the web's `/#(\d{2,7})\b/` finds it: "#" then 2–7 ASCII digits not followed
    /// by another word character.
    static func issueNumber(_ title: String) -> String? {
        let c = Array(title.unicodeScalars)
        let isDigit = { (u: Unicode.Scalar) in u >= "0" && u <= "9" }
        let isWord = { (u: Unicode.Scalar) in isDigit(u) || (u >= "a" && u <= "z") || (u >= "A" && u <= "Z") || u == "_" }
        var i = 0
        while i < c.count {
            if c[i] == "#" {
                var j = i + 1
                while j < c.count, isDigit(c[j]) { j += 1 }
                let n = j - i - 1
                if n >= 2, n <= 7, j == c.count || !isWord(c[j]) {
                    var out = String.UnicodeScalarView()
                    out.append(contentsOf: c[(i + 1)..<j])
                    return String(out)
                }
            }
            i += 1
        }
        return nil
    }

    /// The keys that make two to-dos the same piece of work: the same PR, the same issue number in the title, the same
    /// title once case, spaces and punctuation are dropped (six characters or more), or the same plan.
    static func relatedKeys(_ todo: TodoItem) -> [String] {
        var keys: [String] = []
        var pr = (todo.prURL ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        if let cut = pr.firstIndex(where: { $0 == "#" || $0 == "?" }) { pr = String(pr[..<cut]) }
        while pr.hasSuffix("/") { pr.removeLast() }
        if !pr.isEmpty { keys.append("pr:" + pr.lowercased()) }
        if let issue = issueNumber(todo.title) { keys.append("issue:" + issue) }
        let drop = CharacterSet.whitespacesAndNewlines.union(.punctuationCharacters).union(.symbols)
        var scalars = String.UnicodeScalarView()
        scalars.append(contentsOf: todo.title.lowercased().unicodeScalars.filter { !drop.contains($0) })
        // long enough that two short generic titles ("test it") do not merge
        if scalars.count >= 6 { keys.append("title:" + String(scalars)) }
        if let plan = todo.planID, !plan.isEmpty { keys.append("plan:" + plan) }
        return keys
    }

    /// The row each to-do is in: the root of its related to-dos (union of shared keys), open and closed kept apart.
    static func relatedRoots(_ list: [TodoItem]) -> [String: String] {
        var parent: [String: String] = Dictionary(list.map { ($0.id, $0.id) }, uniquingKeysWith: { a, _ in a })
        func find(_ id: String) -> String {
            var r = id
            while let p = parent[r], p != r { r = p }
            parent[id] = r
            return r
        }
        var owner: [String: String] = [:]
        for td in list {
            let side = td.doneAt != nil ? "c" : "o"
            for k in relatedKeys(td) {
                let key = side + "|" + k
                if let other = owner[key] {
                    let a = find(td.id), b = find(other)
                    parent[a] = b
                } else {
                    owner[key] = td.id
                }
            }
        }
        var roots: [String: String] = [:]
        for td in list { roots[td.id] = find(td.id) }
        return roots
    }

    /// Failed starts (rule 1) whose row has a to-do that really finished: closed without a start error (#3713). The
    /// work got done another way; the failed start goes to Just finished / Done and counts as closed, on the page, the
    /// badge and the assistant's entries alike (the web's `supersededFailedStarts`).
    static func supersededFailedStarts(_ todos: [TodoItem]) -> Set<String> {
        let root = relatedRoots(todos)
        let finished = Set(todos.filter { $0.doneAt != nil && !$0.failedToStart }.compactMap { root[$0.id] })
        return Set(todos.filter { $0.failedToStart && root[$0.id].map(finished.contains) == true }.map(\.id))
    }

    /// Every to-do placed in its section, related ones merged into one row (union of shared keys). Closed to-dos are
    /// not merged into open ones' rows. A row takes its most urgent member's section (a superseded failed start never
    /// leads); rows in a section go most recently active first. `quiet: false` leaves quiet tasks (#1731) out, as
    /// badges do; the page lists them. A caller that narrows the list (search, a filter) passes `superseded` decided on
    /// every to-do, so a failed start whose finished work is left out does not wait again.
    static func triageTodos(_ todos: [TodoItem], lookup: TodosLogic.SessionLookup?, now: Date,
                            restored: [String: Date] = [:], quiet: Bool = true, superseded known: Set<String>? = nil,
                            calendar: Calendar = .current) -> TodoTriageResult {
        let list = quiet ? todos : todos.filter { !$0.quiet }
        let superseded = known ?? supersededFailedStarts(list)
        var placed: [String: TodoTriage] = [:]
        for td in list {
            placed[td.id] = triage(td, lookup: lookup, now: now, restoredAt: restored[td.id], superseded: superseded.contains(td.id),
                                   calendar: calendar)
        }
        let roots = relatedRoots(list)
        var byRoot: [String: [TodoItem]] = [:]
        var rootOrder: [String] = []
        for td in list {
            let r = roots[td.id] ?? td.id
            if byRoot[r] == nil { rootOrder.append(r) }
            byRoot[r, default: []].append(td)
        }

        var counts = Dictionary(uniqueKeysWithValues: TodoSection.allCases.map { ($0, 0) })
        var open = 0
        for td in list {
            let t = placed[td.id]!
            counts[t.section, default: 0] += 1
            if countsAsOpen(td, t) { open += 1 }
        }
        let shelved = counts[.shelved] ?? 0
        let order = Dictionary(uniqueKeysWithValues: TodoSection.allCases.enumerated().map { ($0.element, $0.offset) })
        var sections = Dictionary(uniqueKeysWithValues: TodoSection.allCases.map { ($0, [TodoTriageGroup]()) })
        for r in rootOrder {
            let members = byRoot[r]!.sorted { a, b in
                let ta = placed[a.id]!, tb = placed[b.id]!
                if ta.section != tb.section { return order[ta.section]! < order[tb.section]! }
                if (ta.rule == .superseded) != (tb.rule == .superseded) { return tb.rule == .superseded }
                if ta.last != tb.last { return ta.last > tb.last }
                return a.id < b.id
            }
            let lead = members[0]
            let lt = placed[lead.id]!
            var triageOf: [String: TodoTriage] = [:]
            for m in members { triageOf[m.id] = placed[m.id]! }
            let last = members.map { placed[$0.id]!.last }.max() ?? lt.last
            sections[lt.section]!.append(TodoTriageGroup(key: lead.id, section: lt.section, lead: lead, members: members,
                                                         triage: triageOf, last: last))
        }
        for s in TodoSection.allCases {
            sections[s]!.sort { a, b in a.last != b.last ? a.last > b.last : a.key < b.key }
        }
        return TodoTriageResult(sections: sections, todoCounts: counts, open: open, shelved: shelved,
                                finished: list.count - open - shelved)
    }
}

/// Restores from Shelved (#3206): a restore counts as activity now, kept on this device only (as the web keeps it per
/// browser): another device still shows the to-do shelved until it moves. Entries older than the shelving window are
/// dropped, they no longer change anything.
nonisolated enum TodoRestores {
    static let key = "todos.restored"

    static func load(_ defaults: UserDefaults = .standard, now: Date = .now) -> [String: Date] {
        guard let raw = defaults.dictionary(forKey: key) as? [String: Double] else { return [:] }
        var out: [String: Date] = [:]
        for (id, t) in raw where now.timeIntervalSince1970 - t < TodoTriageLogic.shelveAfter { out[id] = Date(timeIntervalSince1970: t) }
        return out
    }

    @discardableResult
    static func restore(_ ids: [String], _ defaults: UserDefaults = .standard, now: Date = .now) -> [String: Date] {
        var all = load(defaults, now: now)
        for id in ids { all[id] = now }
        defaults.set(all.mapValues(\.timeIntervalSince1970), forKey: key)
        return all
    }
}
