// The To-dos list's filter by due date and its order inside each status section (#3740, docs/assistant/todos.md "List
// filter and sort"): a view in this browser, never sent to the hub. The same rules as iOS `TodoListLogic`
// (apple/Shared/TodoListFilter.swift): a filter narrows the to-dos before the status sections place them, as a search
// does; a sort orders the rows inside each section and never moves a section.

import { addDays, startOfDay, startOfWeek, weekStartDay } from "./todo-calendar";
import { triage, type TriageGroup } from "./todo-triage";
import { isOverdue, todoDueBy, todoDueDay, type SessionLookup, type Todo } from "./todos";

export type TodoListFilter = "all" | "no_due" | "today" | "week" | "month";
export type TodoListSort = "recent" | "due" | "priority";

export const TODO_LIST_FILTERS: readonly TodoListFilter[] = ["all", "no_due", "today", "week", "month"];
export const TODO_LIST_SORTS: readonly TodoListSort[] = ["recent", "due", "priority"];

export const isTodoListFilter = (v: unknown): v is TodoListFilter => TODO_LIST_FILTERS.includes(v as TodoListFilter);
export const isTodoListSort = (v: unknown): v is TodoListSort => TODO_LIST_SORTS.includes(v as TodoListSort);

/** When a to-do is due, for the filter: its due moment, or the local midnight starting its due day. */
export function todoDueStart(todo: Todo): number | undefined {
  if (todo.due_at) {
    const at = Date.parse(todo.due_at);
    if (!Number.isNaN(at)) return at;
  }
  return todoDueDay(todo);
}

/** [start, end) of the day, week or month `now` is in; the week starts on `first` (0 Sunday … 6 Saturday), the
 *  calendar's (`weekStartDay`). */
function span(filter: "today" | "week" | "month", now: number, first: number): [number, number] {
  if (filter === "today") {
    const d = startOfDay(now);
    return [d, addDays(d, 1)];
  }
  if (filter === "week") {
    const w = startOfWeek(now, first);
    return [w, addDays(w, 7)];
  }
  const d = new Date(now);
  return [new Date(d.getFullYear(), d.getMonth(), 1).getTime(), new Date(d.getFullYear(), d.getMonth() + 1, 1).getTime()];
}

/** Whether `todo` is in `filter` at `now`. Today, This Week and This Month take what is due in them and also open
 *  to-dos already overdue; a done one counts only by its own due date. */
export function matchesListFilter(todo: Todo, filter: TodoListFilter, now: number, first = weekStartDay()): boolean {
  if (filter === "all") return true;
  const due = todoDueStart(todo);
  if (filter === "no_due") return due === undefined;
  if (due === undefined) return false;
  if (isOverdue(todo, now)) return true;
  const [from, to] = span(filter, now, first);
  return due >= from && due < to;
}

/** How many to-dos each filter would show (the menu says it beside each). */
export function listFilterCounts(todos: Todo[], now: number, first = weekStartDay()): Record<TodoListFilter, number> {
  const out = { all: 0, no_due: 0, today: 0, week: 0, month: 0 } as Record<TodoListFilter, number>;
  for (const f of TODO_LIST_FILTERS) out[f] = todos.filter((td) => matchesListFilter(td, f, now, first)).length;
  return out;
}

/** high first, then normal, then low. */
/** How many of `searched` wait on the user (SOP section Needs you) but are not `shown`: the line over the list (#3740).
 *  `superseded` is decided on every to-do (`supersededFailedStarts`), so a failed start whose finished work the filter
 *  leaves out is not counted as waiting (#3713). */
export function hiddenWaiting(
  searched: Todo[],
  shown: (todo: Todo) => boolean,
  lookup: SessionLookup | undefined,
  now: number,
  restored: ReadonlyMap<string, number>,
  superseded: ReadonlySet<string>,
): number {
  return searched.filter((td) => !shown(td) && triage(td, lookup, now, restored.get(td.id) ?? 0, superseded.has(td.id)).section === "waiting").length;
}

export function priorityRank(todo: Todo): number {
  return todo.priority === "high" ? 0 : todo.priority === "low" ? 2 : 1;
}

/** A section's rows in `sort`'s order. A merged row sorts by its earliest due and its highest priority member; ties
 *  keep the list's order (most recently active first). `recent` leaves the rows as they are. */
export function sortTriageGroups(groups: TriageGroup[], sort: TodoListSort): TriageGroup[] {
  if (sort === "recent") return groups;
  const keyed = groups.map((group, i) => ({
    group,
    i,
    due: Math.min(...group.members.map((m) => todoDueBy(m) ?? Infinity)),
    rank: Math.min(...group.members.map(priorityRank)),
  }));
  keyed.sort((a, b) => (sort === "priority" && a.rank !== b.rank ? a.rank - b.rank : 0) || (a.due !== b.due ? a.due - b.due : 0) || a.i - b.i);
  return keyed.map((k) => k.group);
}
