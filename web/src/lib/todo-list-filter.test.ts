// The To-dos list's filter by due date and its sort inside each section (#3740): the same cases as iOS
// TodoListFilterTests. Local times: these run in whatever zone the machine has, as the page does. Wednesday 2026-11-11,
// noon, is "now"; weeks start on Monday (1) unless a case says otherwise.
import { describe, expect, it } from "vitest";
import { hiddenWaiting, listFilterCounts, matchesListFilter, sortTriageGroups, type TodoListFilter } from "./todo-list-filter";
import { supersededFailedStarts, triageTodos, type TriageGroup } from "./todo-triage";
import type { Todo } from "./todos";

const at = (day: number, hour = 9, month = 11) => new Date(2026, month - 1, day, hour).getTime();
const NOW = at(11, 12);
const MONDAY = 1;
const SUNDAY = 0;

const todo = (id: string, patch: Partial<Todo> = {}): Todo =>
  ({ id, title: id, created_at: new Date(NOW - 86400_000).toISOString(), updated_at: new Date(NOW - 3600_000).toISOString(), done_at: null, agents: [], ...patch }) as Todo;
const iso = (ms: number) => new Date(ms).toISOString();

const LIST: Todo[] = [
  todo("none"),
  todo("today-day", { due_date: "2026-11-11" }),
  todo("today-evening", { due_at: iso(at(11, 18)) }),
  todo("passed-today", { due_at: iso(at(11, 9)) }), // earlier today, open: overdue
  todo("sunday", { due_date: "2026-11-15" }), // the week's last day
  todo("next-monday", { due_date: "2026-11-16" }), // next week, this month
  todo("last-of-month", { due_date: "2026-11-30" }),
  todo("december", { due_date: "2026-12-01" }),
  todo("overdue-open", { due_date: "2026-11-07" }),
  todo("overdue-done", { due_date: "2026-11-07", done_at: iso(NOW - 3600_000) }),
  todo("monday-done", { due_date: "2026-11-09", done_at: iso(NOW - 3600_000) }), // this week, already done
  todo("bad-day", { due_date: "soon" }), // unreadable: no due date
];
const shown = (f: TodoListFilter, first = MONDAY) => LIST.filter((td) => matchesListFilter(td, f, NOW, first)).map((td) => td.id);

describe("the list's filter by due date", () => {
  it("shows everything with All, and those without a due date with No due date", () => {
    expect(shown("all")).toHaveLength(LIST.length);
    expect(shown("no_due")).toEqual(["none", "bad-day"]);
  });

  it("takes open to-dos already overdue into Today, This week and This month, never done ones that were", () => {
    expect(shown("today")).toEqual(["today-day", "today-evening", "passed-today", "overdue-open"]);
    expect(shown("week")).toEqual(["today-day", "today-evening", "passed-today", "sunday", "overdue-open", "monday-done"]);
    expect(shown("month")).toEqual(["today-day", "today-evening", "passed-today", "sunday", "next-monday", "last-of-month", "overdue-open", "overdue-done", "monday-done"]);
  });

  it("takes the week the calendar has: Sunday-first, the 15th is next week's", () => {
    const sunday = todo("sunday", { due_date: "2026-11-15" });
    const monday = todo("monday", { due_date: "2026-11-09" });
    expect(matchesListFilter(sunday, "week", NOW, SUNDAY)).toBe(false);
    expect(matchesListFilter(monday, "week", NOW, SUNDAY)).toBe(true);
    expect(matchesListFilter(sunday, "week", NOW, MONDAY)).toBe(true);
  });

  it("counts what each filter would show", () => {
    expect(listFilterCounts(LIST, NOW, MONDAY)).toEqual({ all: 12, no_due: 2, today: 4, week: 6, month: 9 });
  });
});

const group = (members: Todo[]): TriageGroup => ({ key: members[0].id, section: "other", lead: members[0], members, triage: new Map(), last: NOW });

describe("the list's sort inside a section", () => {
  it("keeps Recent activity; puts the earliest due first, none last; goes high, normal, low, then by due", () => {
    const rows = [
      group([todo("a", { priority: "low" })]),
      group([todo("b", { due_date: "2026-11-20" })]),
      group([todo("c", { due_date: "2026-11-12", priority: "high" })]),
      group([todo("d")]),
      group([todo("e", { due_date: "2026-11-12" })]),
    ];
    const ids = (s: "recent" | "due" | "priority") => sortTriageGroups(rows, s).map((g) => g.key);
    expect(ids("recent")).toEqual(["a", "b", "c", "d", "e"]);
    expect(ids("due")).toEqual(["c", "e", "b", "a", "d"]);
    expect(ids("priority")).toEqual(["c", "e", "b", "d", "a"]);
  });

  it("sorts a merged row by its earliest due and its highest priority member", () => {
    const rows = [group([todo("lead"), todo("member", { due_date: "2026-11-12", priority: "high" })]), group([todo("other", { due_date: "2026-11-13" })])];
    expect(sortTriageGroups(rows, "due").map((g) => g.key)).toEqual(["lead", "other"]);
    expect(sortTriageGroups(rows, "priority").map((g) => g.key)).toEqual(["lead", "other"]);
  });
});

// The same case as iOS TodoListFilterTests.aFilterNeverPutsASupersededFailedStartBackInNeedsYou (#3713 with #3740).
describe("a failed start superseded by finished work, under a filter or a search", () => {
  const closed = iso(NOW - 3600_000);
  const failed = todo("failed", { due_date: "2026-11-11", done_at: closed, plan_id: "p", start_error: "no launcher" }); // due today: shown
  const finished = todo("finished", { done_at: closed, plan_id: "p" }); // its plan's work, no due date: left out
  const hiddenFailed = todo("hidden-failed", { done_at: closed, plan_id: "q", start_error: "no launcher" });
  const hiddenFinished = todo("hidden-finished", { done_at: closed, plan_id: "q" });
  const waiting = todo("waiting", { done_at: closed, start_error: "no launcher" }); // nothing finished beside it: waits
  const all = [failed, finished, hiddenFailed, hiddenFinished, waiting];
  const superseded = supersededFailedStarts(all);
  const today = (td: Todo) => matchesListFilter(td, "today", NOW, MONDAY);
  const ids = (gs: TriageGroup[]) => gs.flatMap((g) => g.members.map((m) => m.id));

  it("stays out of Needs you when the filter leaves its finished work out", () => {
    const r = triageTodos(all.filter(today), undefined, NOW, new Map(), { superseded });
    expect(ids(r.sections.waiting)).toEqual([]);
    expect(ids(r.sections.just_done)).toEqual(["failed"]);
  });

  it("is not counted in the line about those the filter leaves out", () => {
    expect(hiddenWaiting(all, today, undefined, NOW, new Map(), superseded)).toBe(1);
  });

  it("stays out of Needs you when the search leaves its finished work out", () => {
    const r = triageTodos([failed, waiting], undefined, NOW, new Map(), { superseded });
    expect(ids(r.sections.waiting)).toEqual(["waiting"]);
  });
});
