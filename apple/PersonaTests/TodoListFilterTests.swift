import Foundation
import Testing
@testable import Persona

/// The To-dos list's filter by due date and its sort inside each section (#3740, docs/assistant/todos.md "List filter
/// and sort"). Wednesday 2026-11-11, noon in Shanghai; weeks run Monday to Sunday as the calendar view's do in zh-Hans.
@Suite
@MainActor
struct TodoListFilterTests {
    private var calendar: Calendar {
        var c = Calendar(identifier: .gregorian)
        c.timeZone = TimeZone(identifier: "Asia/Shanghai")!
        c.firstWeekday = 2
        return c
    }
    private var now: Date { calendar.date(from: DateComponents(year: 2026, month: 11, day: 11, hour: 12))! }
    private func at(_ day: Int, _ hour: Int = 9, month: Int = 11) -> Date {
        calendar.date(from: DateComponents(year: 2026, month: month, day: day, hour: hour))!
    }

    private func todo(_ id: String, day: String? = nil, at: Date? = nil, done: Bool = false, priority: String? = nil,
                      updatedHoursAgo: Double = 1) -> TodoItem {
        TodoItem(id: id, title: id, doneAt: done ? now.addingTimeInterval(-3600) : nil, createdAt: now.addingTimeInterval(-86400),
                 updatedAt: now.addingTimeInterval(-updatedHoursAgo * 3600), dueAt: at, dueDate: day, priority: priority)
    }

    private func shown(_ todos: [TodoItem], _ filter: TodoListFilter) -> [String] {
        todos.filter { TodoListLogic.matches($0, filter, now: now, calendar: calendar) }.map(\.id)
    }

    private var list: [TodoItem] {
        [
            todo("none"),
            todo("today-day", day: "2026-11-11"),
            todo("today-evening", at: at(11, 18)),
            todo("passed-today", at: at(11, 9)),              // earlier today, open: overdue
            todo("sunday", day: "2026-11-15"),                  // the week's last day
            todo("next-monday", day: "2026-11-16"),             // next week, this month
            todo("last-of-month", day: "2026-11-30"),
            todo("december", day: "2026-12-01"),
            todo("overdue-open", day: "2026-11-07"),
            todo("overdue-done", day: "2026-11-07", done: true),
            todo("monday-done", day: "2026-11-09", done: true), // this week, already done
            todo("bad-day", day: "soon"),                       // unreadable: no due date
        ]
    }

    @Test func allShowsEverything() {
        #expect(shown(list, .all).count == list.count)
    }

    @Test func noDueDateIsTheOnesWithoutOne() {
        #expect(shown(list, .noDue) == ["none", "bad-day"])
    }

    /// Today, This Week and This Month also take open to-dos already overdue, never done ones that were.
    @Test func timeFiltersTakeOpenOverdueToDos() {
        #expect(shown(list, .today) == ["today-day", "today-evening", "passed-today", "overdue-open"])
        #expect(shown(list, .week) == ["today-day", "today-evening", "passed-today", "sunday", "overdue-open", "monday-done"])
        #expect(shown(list, .month) == ["today-day", "today-evening", "passed-today", "sunday", "next-monday", "last-of-month",
                                        "overdue-open", "overdue-done", "monday-done"])
    }

    /// The week is the calendar's: with weeks starting on Sunday, Sunday the 15th is next week's.
    @Test func theWeekFollowsTheCalendarsFirstDay() {
        var sundayFirst = calendar
        sundayFirst.firstWeekday = 1
        let sunday = todo("sunday", day: "2026-11-15"), monday = todo("monday", day: "2026-11-09")
        #expect(!TodoListLogic.matches(sunday, .week, now: now, calendar: sundayFirst))
        #expect(TodoListLogic.matches(monday, .week, now: now, calendar: sundayFirst))
        #expect(TodoListLogic.matches(sunday, .week, now: now, calendar: calendar))
    }

    @Test func countsPerFilter() {
        let c = TodoListLogic.counts(list, now: now, calendar: calendar)
        #expect(c[.all] == 12 && c[.noDue] == 2 && c[.today] == 4 && c[.week] == 6 && c[.month] == 9)
    }

    private func group(_ members: [TodoItem]) -> TodoTriageGroup {
        TodoTriageGroup(key: members[0].id, section: .other, lead: members[0], members: members,
                        triage: [:], last: now)
    }

    /// Recent keeps the list's order; Due puts the earliest first and those without one last; Priority goes high,
    /// normal, low, then by due date; ties keep the list's order.
    @Test func sortsInsideASection() {
        let rows = [
            group([todo("a", priority: "low")]),
            group([todo("b", day: "2026-11-20")]),
            group([todo("c", day: "2026-11-12", priority: "high")]),
            group([todo("d")]),
            group([todo("e", day: "2026-11-12")]),
        ]
        let ids = { (s: TodoListSort) in TodoListLogic.sorted(rows, by: s, calendar: calendar).map(\.key) }
        #expect(ids(.recent) == ["a", "b", "c", "d", "e"])
        #expect(ids(.due) == ["c", "e", "b", "a", "d"])
        #expect(ids(.priority) == ["c", "e", "b", "d", "a"])
    }

    /// A merged row sorts by its earliest due and its highest priority member.
    @Test func aMergedRowSortsByItsBestMember() {
        let rows = [
            group([todo("lead"), todo("member", day: "2026-11-12", priority: "high")]),
            group([todo("other", day: "2026-11-13")]),
        ]
        #expect(TodoListLogic.sorted(rows, by: .due, calendar: calendar).map(\.key) == ["lead", "other"])
        #expect(TodoListLogic.sorted(rows, by: .priority, calendar: calendar).map(\.key) == ["lead", "other"])
    }

    /// A failed start (#3713) whose finished work a filter or a search leaves out is still superseded (decided on every
    /// to-do): not back in Needs you, not in the line about those the filter leaves out.
    @Test func aFilterNeverPutsASupersededFailedStartBackInNeedsYou() {
        var failed = todo("failed", day: "2026-11-11", done: true)       // due today: shown under Today
        failed.planID = "p"; failed.startError = "no launcher"
        var finished = todo("finished", done: true)                      // its plan's work, no due date: left out
        finished.planID = "p"
        var hiddenFailed = todo("hidden-failed", done: true)
        hiddenFailed.planID = "q"; hiddenFailed.startError = "no launcher"
        var hiddenFinished = todo("hidden-finished", done: true)
        hiddenFinished.planID = "q"
        var waiting = todo("waiting", done: true)                        // nothing finished beside it: waits
        waiting.startError = "no launcher"
        let all = [failed, finished, hiddenFailed, hiddenFinished, waiting]

        let today = TodoListLogic.list(all, searched: all, filter: .today, lookup: nil, now: now, restored: [:], calendar: calendar)
        #expect(today.result.groups(.waiting).isEmpty)
        #expect(today.result.groups(.justDone).flatMap { $0.members.map(\.id) } == ["failed"])
        #expect(today.hiddenWaiting == 1)

        let searched = TodoListLogic.list(all, searched: [failed, waiting], filter: .all, lookup: nil, now: now, restored: [:],
                                          calendar: calendar)
        #expect(searched.result.groups(.waiting).flatMap { $0.members.map(\.id) } == ["waiting"])
        #expect(searched.hiddenWaiting == 0)
    }
}
