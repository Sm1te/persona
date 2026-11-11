import Foundation

// The To-dos list's filter and sort (#3740, docs/assistant/todos.md "List filter and sort"): a view on the device, never
// sent to the hub. A filter narrows the to-dos before the status sections place them (as a search does), so each
// section and its count follow it; a sort orders the rows inside each section, the sections stay in their order.

/// Which to-dos the list shows, by due date. Today, This Week and This Month also take open to-dos already overdue.
nonisolated enum TodoListFilter: String, CaseIterable, Sendable {
    case all
    case noDue = "no_due"
    case today, week, month

    var title: String {
        switch self {
        case .all: String(localized: "All")
        case .noDue: String(localized: "No Due Date")
        case .today: String(localized: "Today")
        case .week: String(localized: "This Week")
        case .month: String(localized: "This Month")
        }
    }
}

/// How rows are ordered inside each section. Recent (most recently active first) is the list's own order.
nonisolated enum TodoListSort: String, CaseIterable, Sendable {
    case recent, due, priority

    var title: String {
        switch self {
        case .recent: String(localized: "Recent Activity")
        case .due: String(localized: "Due Date")
        case .priority: String(localized: "Priority")
        }
    }
}

/// The list under a filter: the triage of what it shows, and how many to-dos it leaves out that wait on the owner.
nonisolated struct TodoFilteredList: Sendable {
    var result: TodoTriageResult
    /// To-dos waiting on the owner that the filter leaves out (the line over the list).
    var hiddenWaiting: Int
}

nonisolated enum TodoListLogic {
    /// `searched` (the to-dos the search keeps) under `filter`, placed in sections. Whether a failed start was superseded
    /// (#3713) is decided on `all`, every to-do: a search or a filter that leaves its finished work out never puts it back
    /// in Needs you, nor in the count of those left out.
    static func list(_ all: [TodoItem], searched: [TodoItem], filter: TodoListFilter, lookup: TodosLogic.SessionLookup?,
                     now: Date, restored: [String: Date], calendar: Calendar) -> TodoFilteredList {
        let superseded = TodoTriageLogic.supersededFailedStarts(all)
        let shown = searched.filter { matches($0, filter, now: now, calendar: calendar) }
        let result = TodoTriageLogic.triageTodos(shown, lookup: lookup, now: now, restored: restored, superseded: superseded)
        guard filter != .all else { return TodoFilteredList(result: result, hiddenWaiting: 0) }
        let ids = Set(shown.map(\.id))
        let hidden = searched.filter { td in
            !ids.contains(td.id)
                && TodoTriageLogic.triage(td, lookup: lookup, now: now, restoredAt: restored[td.id],
                                          superseded: superseded.contains(td.id)).section == .waiting
        }.count
        return TodoFilteredList(result: result, hiddenWaiting: hidden)
    }

    /// Whether `todo` is in `filter` at `now`. The week is `calendar`'s (the calendar view's, `TodoCalendar.userCalendar`);
    /// a due day counts by its start, a due moment by itself.
    static func matches(_ todo: TodoItem, _ filter: TodoListFilter, now: Date, calendar: Calendar) -> Bool {
        let unit: Calendar.Component
        switch filter {
        case .all: return true
        case .noDue: return due(todo, calendar: calendar) == nil
        case .today: unit = .day
        case .week: unit = .weekOfYear
        case .month: unit = .month
        }
        guard let at = due(todo, calendar: calendar) else { return false }
        if TodosLogic.isOverdue(todo, now: now, calendar: calendar) { return true }
        guard let span = calendar.dateInterval(of: unit, for: now) else { return false }
        return at >= span.start && at < span.end
    }

    /// When a to-do is due, for the filter: its moment, or the start of its day; nil without either.
    static func due(_ todo: TodoItem, calendar: Calendar) -> Date? {
        todo.dueAt ?? TodosLogic.dueDay(todo, calendar: calendar)
    }

    /// How many to-dos each filter would show (the menu says it under each).
    static func counts(_ todos: [TodoItem], now: Date, calendar: Calendar) -> [TodoListFilter: Int] {
        Dictionary(uniqueKeysWithValues: TodoListFilter.allCases.map { f in
            (f, todos.filter { matches($0, f, now: now, calendar: calendar) }.count)
        })
    }

    /// high first, then normal, then low.
    static func priorityRank(_ todo: TodoItem) -> Int {
        switch todo.priority ?? "" {
        case "high": 0
        case "low": 2
        default: 1
        }
    }

    /// A section's rows in `sort`'s order. A merged row sorts by its earliest due / highest priority member; ties keep
    /// the list's order (most recently active first). `.recent` leaves the rows as they are.
    static func sorted(_ groups: [TodoTriageGroup], by sort: TodoListSort, calendar: Calendar) -> [TodoTriageGroup] {
        if sort == .recent { return groups }
        let far = Date.distantFuture
        func dueKey(_ g: TodoTriageGroup) -> Date { g.members.compactMap { TodosLogic.dueBy($0, calendar: calendar) }.min() ?? far }
        func rankKey(_ g: TodoTriageGroup) -> Int { g.members.map(priorityRank).min() ?? 1 }
        let keyed = groups.enumerated().map { (offset: $0.offset, group: $0.element, due: dueKey($0.element), rank: rankKey($0.element)) }
        return keyed.sorted { a, b in
            if sort == .priority, a.rank != b.rank { return a.rank < b.rank }
            if a.due != b.due { return a.due < b.due }
            return a.offset < b.offset
        }.map { $0.group }
    }
}
