// The To-dos page's sections (#3156, docs/assistant/todo-status-sop.md): each to-do's place comes from real signals only — its
// agents' sessions (alive, running, asking), its PR / delivery state, the latest task_review verdict and when it last moved —
// never from what an agent says about itself. The rules are the SOP's table, in its order; the first that holds wins.

import { acceptanceStands, latestReview, reviewState } from "./supervision";
import { openAsk, todoDueBy, todoStatus, type SessionLookup, type Todo, type TodoStatus } from "./todos";
import { todoStageIndex } from "./todo-stage";

/** Where a to-do shows, in the page's order. The first four are open sections; the last three start folded. */
export type TriageSection = "waiting" | "working" | "just_done" | "pr" | "other" | "shelved" | "done";

export const TRIAGE_SECTIONS: readonly TriageSection[] = ["waiting", "working", "just_done", "pr", "other", "shelved", "done"];

/** Sections that start folded. */
export const FOLDED_SECTIONS: ReadonlySet<TriageSection> = new Set(["other", "shelved", "done"]);

/** No activity for this long, nothing of it running: shelved (SOP rule 11). */
export const SHELVE_AFTER_MS = 48 * 3600_000;
/** Finished this recently: "Just finished"; earlier, "Done". */
export const JUST_DONE_MS = 24 * 3600_000;

/** Which SOP rule placed a to-do (for tests and the SOP's wording); the section follows from it. */
export type TriageRule =
  | "failed_start"
  | "superseded"
  | "agent_asks"
  | "owner_decides"
  | "agent_running"
  | "closed"
  | "accepted"
  | "deployed"
  | "finished_unseen"
  | "pr_open"
  | "stale"
  | "rest";

export interface Triage {
  section: TriageSection;
  rule: TriageRule;
  /** The last moment anything of it moved, ms (0 when nothing says). */
  last: number;
}

function ms(at: string | null | undefined): number {
  if (!at) return 0;
  const v = Date.parse(at);
  return Number.isNaN(v) ? 0 : v;
}

/** The last moment the to-do or any of its agents moved: its own times, its supervision's (reviews, reports), its
 *  delivery and ask, and each linked agent's session. A restore from Shelved counts as a move (`restoredAt`). */
export function lastActivity(todo: Todo, lookup?: SessionLookup, restoredAt = 0): number {
  const sup = todo.supervision;
  let last = Math.max(
    ms(todo.updated_at),
    ms(todo.created_at),
    ms(todo.reopened_at),
    ms(todo.done_at),
    ms(sup?.updated_at),
    ms(sup?.progress?.at),
    ms(latestReview(sup)?.at),
    ms(todo.delivery?.at),
    ms(todo.ask?.asked_at),
    restoredAt,
  );
  for (const a of todo.agents ?? []) {
    if (a.released_at) continue;
    const s = lookup?.(a.machine_id, a.session_id);
    if (s) last = Math.max(last, ms(s.updated_at), ms(s.state_since));
  }
  return last;
}

/** The PR its work stopped at, still to merge: the hub says the delivery stopped at a PR, or it knows a PR and neither
 *  a delivery state nor the stage is past it (a stage at merge or later is a merged PR, #3205). */
export function openPR(todo: Todo): boolean {
  const d = todo.delivery?.state;
  if (d === "pr") return true;
  return Boolean(todo.pr_url) && (!d || d === "none") && todoStageIndex(todo.stage) < todoStageIndex("merge");
}

/** Whether a to-do may be shelved at all: it is a task (an agent was linked, or a conversation handed it out) with no
 *  due date ahead. The user's own to-dos without agents — reminders, errands — never shelve themselves. */
function shelvable(todo: Todo, now: number): boolean {
  const due = todoDueBy(todo);
  if (due !== undefined && due >= now) return false;
  return (todo.agents ?? []).length > 0 || Boolean(todo.origin);
}

/**
 * A to-do's section by the SOP's table (docs/assistant/todo-status-sop.md, "Decision table"), first match wins:
 *
 *  1. failed_start    the task's agent never started                    → Waiting on you (retry or remove), Done after 48 h;
 *     superseded      … and a related to-do (its row) really finished   → Just finished (24 h), then Done (#3713)
 *  2. agent_asks      an agent asks (question, approval) or was cut off → Waiting on you
 *  3. owner_decides   review says needs_user, or a decision / "cannot" ask → Waiting on you
 *  4. agent_running   an agent is working or starting                   → Working
 *  5. closed          done_at set                                       → Just finished (24 h), then Done
 *  6. accepted        the latest review accepted it (even reopened)     → Just finished (24 h), then Done
 *  7. deployed        its delivery is live                              → Just finished (24 h), then Done
 *  8. finished_unseen an agent finished unseen, awaits review, or a done ask → Just finished
 *  9. pr_open         its PR is open, nothing running                   → PR to merge (however old)
 * 10. stale           a task with no activity for 48 h                  → Shelved
 * 11. rest            anything else                                     → Other open
 */
export function triage(todo: Todo, lookup: SessionLookup | undefined, now: number, restoredAt = 0, superseded = false): Triage {
  const last = lastActivity(todo, lookup, restoredAt);
  const status: TodoStatus = todoStatus(todo, lookup);
  const recent = (at: number) => now - at < JUST_DONE_MS;
  const ask = openAsk(todo);
  const at = (section: TriageSection, rule: TriageRule): Triage => ({ section, rule, last });

  if (status === "failed") {
    // the same piece of work finished in a related to-do (`supersededFailedStarts`): history, nothing left to retry
    if (superseded) return at(recent(ms(todo.done_at) || last) ? "just_done" : "done", "superseded");
    return at(now - last < SHELVE_AFTER_MS ? "waiting" : "done", "failed_start");
  }
  if (status === "needs_you" || status === "interrupted") return at("waiting", "agent_asks");
  if (!todo.done_at && (reviewState(todo) === "needs_user" || ask?.outcome === "decide" || ask?.outcome === "cannot")) return at("waiting", "owner_decides");
  if (status === "working" || status === "waiting") return at("working", "agent_running");
  if (todo.done_at) return at(recent(ms(todo.done_at)) ? "just_done" : "done", "closed");
  if (acceptanceStands(todo)) return at(recent(ms(latestReview(todo.supervision)?.at)) ? "just_done" : "done", "accepted");
  if (todo.delivery?.state === "live") return at(recent(ms(todo.delivery.at) || last) ? "just_done" : "done", "deployed");
  if (status === "unread" || reviewState(todo) === "pending_review" || (ask && !ask.outcome) || ask?.outcome === "done") return at("just_done", "finished_unseen");
  if (openPR(todo)) return at("pr", "pr_open");
  if (shelvable(todo, now) && now - last >= SHELVE_AFTER_MS) return at("shelved", "stale");
  return at("other", "rest");
}

/** Whether a to-do counts toward the header's open count: not closed, not accepted or deployed, not shelved. A finished
 *  turn nobody looked at yet (Just finished, still open) counts: only a review or the user closes a to-do. */
export function countsAsOpen(todo: Todo, t: Triage): boolean {
  // a failed start has done_at too (the hub ends it), yet while it waits on the owner it is theirs to retry: open
  if (t.rule === "failed_start") return t.section === "waiting";
  if (todo.done_at || t.section === "shelved" || t.section === "done") return false;
  return t.rule !== "accepted" && t.rule !== "deployed";
}

/** Where a to-do stands for every count of open work (#3331): the To-dos page's "Not done" and the conversation's
 *  Progress entry read this one rule, so the two numbers agree. `open` counts, `shelved` folds (no activity for 48 h,
 *  nothing running), `closed` is finished (done, accepted, deployed, an old failed start). */
export type TodoBucket = "open" | "shelved" | "closed";

export function todoBucket(todo: Todo, lookup: SessionLookup | undefined, now: number, restoredAt = 0, superseded = false): TodoBucket {
  const t = triage(todo, lookup, now, restoredAt, superseded);
  if (countsAsOpen(todo, t)) return "open";
  return t.section === "shelved" ? "shelved" : "closed";
}

/** Whether a to-do's checkbox shows it open: not closed, or a failed start still waiting on the owner (rule 1), which
 *  the hub closed itself and the page counts as open (#3713). Ticking an open one closes it (`done: true`: for a failed
 *  start the hub clears its start error, so it moves to Just finished); unticking a closed one reopens it. */
export function checksAsOpen(todo: Todo, section?: TriageSection): boolean {
  if (!todo.done_at) return true;
  return Boolean(todo.start_error) && section === "waiting";
}

// ── Related to-dos ────────────────────────────────────────────

const ISSUE_REF = /#(\d{2,7})\b/;

/** The keys that make two to-dos the same piece of work: the same PR, the same issue number in the title, the same
 *  title once case, spaces and punctuation are dropped, or the same plan (its subtasks under one request). */
export function relatedKeys(todo: Todo): string[] {
  const keys: string[] = [];
  const pr = (todo.pr_url ?? "").trim().replace(/[#?].*$/, "").replace(/\/+$/, "").toLowerCase();
  if (pr) keys.push("pr:" + pr);
  const issue = ISSUE_REF.exec(todo.title);
  if (issue) keys.push("issue:" + issue[1]);
  const title = todo.title.toLowerCase().replace(/[\s\p{P}\p{S}]+/gu, "");
  // long enough that two short generic titles ("test it") do not merge
  if ([...title].length >= 6) keys.push("title:" + title);
  if (todo.plan_id) keys.push("plan:" + todo.plan_id);
  return keys;
}

/** The row each to-do is in: the root of its related to-dos (union of shared keys). Closed to-dos and open ones never
 *  share a row: a closed one is history, not part of the open work. */
function relatedRoots(list: Todo[]): (id: string) => string {
  const parent = new Map(list.map((td) => [td.id, td.id]));
  const find = (id: string): string => {
    let r = id;
    while (parent.get(r) !== r) r = parent.get(r)!;
    parent.set(id, r);
    return r;
  };
  const owner = new Map<string, string>();
  for (const td of list) {
    const side = td.done_at ? "c" : "o";
    for (const k of relatedKeys(td)) {
      const key = side + "|" + k;
      const other = owner.get(key);
      if (other === undefined) owner.set(key, td.id);
      else parent.set(find(td.id), find(other));
    }
  }
  return find;
}

/**
 * Failed starts (rule 1) whose row has a to-do that really finished: closed without a start error, so done by the user,
 * a review or the hub's completion check (#3713). The work got done another way; the failed start is history, not the
 * owner's to retry: it goes to Just finished / Done and counts as closed, on the page, the badge and Progress alike.
 */
export function supersededFailedStarts(todos: Todo[]): Set<string> {
  const root = relatedRoots(todos);
  const finishedRows = new Set(todos.filter((td) => td.done_at && !td.start_error).map((td) => root(td.id)));
  return new Set(todos.filter((td) => td.start_error && finishedRows.has(root(td.id))).map((td) => td.id));
}

export interface TriageGroup {
  /** The lead's id: stable while the lead stays. */
  key: string;
  section: TriageSection;
  /** The member whose section is the group's, most recently active among those. */
  lead: Todo;
  /** Every member, the lead first, then by section and recency. */
  members: Todo[];
  /** Each member's triage, by id. */
  triage: ReadonlyMap<string, Triage>;
  last: number;
}

export interface TriageResult {
  sections: Record<TriageSection, TriageGroup[]>;
  /** To-dos (not rows) per section. */
  todoCounts: Record<TriageSection, number>;
  /** To-dos the header calls open (countsAsOpen), shelved, and finished (closed, accepted or deployed). */
  open: number;
  shelved: number;
  finished: number;
}

const ORDER = new Map(TRIAGE_SECTIONS.map((s, i) => [s, i]));

/**
 * Every to-do placed in its section, related ones merged into one row (union of shared keys). Closed to-dos are not
 * merged into open ones' rows: a group is open members only, or closed members only. A group takes its most urgent
 * member's section (a superseded failed start never leads); rows in a section go most recently active first. `quiet: false` leaves quiet tasks (#1731) out,
 * as the sidebar's badge does; the page lists them.
 */
export function triageTodos(
  todos: Todo[],
  lookup: SessionLookup | undefined,
  now: number,
  restored: ReadonlyMap<string, number> = new Map(),
  { quiet = true, superseded: known }: { quiet?: boolean; superseded?: ReadonlySet<string> } = {},
): TriageResult {
  const list = quiet ? todos : todos.filter((td) => !td.quiet);
  // a caller that narrows the list (search, a filter) passes the set decided on every to-do
  const superseded = known ?? supersededFailedStarts(list);
  const placed = new Map(list.map((td) => [td.id, triage(td, lookup, now, restored.get(td.id) ?? 0, superseded.has(td.id))]));
  const find = relatedRoots(list);
  const byRoot = new Map<string, Todo[]>();
  for (const td of list) {
    const r = find(td.id);
    const arr = byRoot.get(r);
    if (arr) arr.push(td);
    else byRoot.set(r, [td]);
  }

  const sections = Object.fromEntries(TRIAGE_SECTIONS.map((s) => [s, [] as TriageGroup[]])) as Record<TriageSection, TriageGroup[]>;
  const todoCounts = Object.fromEntries(TRIAGE_SECTIONS.map((s) => [s, 0])) as Record<TriageSection, number>;
  let open = 0;
  for (const td of list) {
    const tr = placed.get(td.id)!;
    todoCounts[tr.section]++;
    if (countsAsOpen(td, tr)) open++;
  }
  const shelved = todoCounts.shelved;
  const finished = list.length - open - shelved;
  for (const members of byRoot.values()) {
    members.sort((a, b) => {
      const ta = placed.get(a.id)!;
      const tb = placed.get(b.id)!;
      // a superseded failed start never leads the row of the work that finished
      const behind = (t: Triage) => (t.rule === "superseded" ? 1 : 0);
      return ORDER.get(ta.section)! - ORDER.get(tb.section)! || behind(ta) - behind(tb) || tb.last - ta.last || a.id.localeCompare(b.id);
    });
    const lead = members[0];
    const lt = placed.get(lead.id)!;
    const triageOf = new Map(members.map((m) => [m.id, placed.get(m.id)!]));
    sections[lt.section].push({ key: lead.id, section: lt.section, lead, members, triage: triageOf, last: Math.max(...members.map((m) => placed.get(m.id)!.last)) });
  }
  for (const s of TRIAGE_SECTIONS) sections[s].sort((a, b) => b.last - a.last || a.key.localeCompare(b.key));
  return { sections, todoCounts, open, shelved, finished };
}
