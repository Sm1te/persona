import { formatDate, t } from "../i18n";
import type { Todo } from "./todos";

// A task the main conversation supervises (#3124, docs/assistant/unified-entry.md "Supervision"): what done means, where its review
// stands, and the reviews so far. Only such tasks carry `supervision`; an older hub sends none.

export type ReviewState = "" | "pending_review" | "rework" | "needs_user" | "parked" | "accepted" | "cancelled";

/** What a parked task waits for (#3804, docs/assistant/todos.md "Parked tasks"): the reason in one sentence, until when,
 *  what kind of thing it waits on, and how many times in a row it was parked with no agent turn in between. */
export interface Parked {
  reason: string;
  until: string;
  waiting_on: "owner_priority" | "someone_else" | "external_event" | "date";
  count: number;
}

export interface ReviewCheck {
  criterion: string;
  met: boolean;
  evidence?: string;
}

export interface TaskReview {
  at: string;
  verdict: "accepted" | "rework" | "needs_user" | "parked" | "cancelled";
  /** "reviewer": a rework the independent reviewer's findings started (#2173); absent: the supervisor's. */
  by?: string;
  round?: number;
  checks?: ReviewCheck[];
  note?: string;
  reason?: string;
  recommend?: string;
  options?: { label: string; leads_to?: string }[];
}

export interface Supervision {
  supervisor: string;
  done_when?: string[];
  /** Latest per-entry reports/checks. Absent on older hubs; done_when owns text and order. */
  criteria?: CriterionStatus[];
  expect_minutes?: number;
  state?: ReviewState;
  /** Present while `state` is "parked": what the task waits for. Absent otherwise, and on an older hub. */
  parked?: Parked;
  round?: number;
  reviews?: TaskReview[];
  updated_at?: string;
  /** What its agent last reported to Persona (report_to_supervisor); kept by the hub while it runs. */
  progress?: TaskProgress;
  /** The latest independent review of its change (#2173); absent when none ran (or on an older hub). */
  auto_review?: AutoReview;
}

export interface CriterionStatus {
  criterion: string;
  state: "pending" | "met" | "not_met";
  evidence?: string;
  at?: string;
  /** agent:<machine>/<session>, supervisor:<conversation>, or judge. */
  by?: string;
}

/** An independent reviewer's run over a supervised task's change: its state, counts and report. */
export interface AutoReview {
  id: string;
  state: "running" | "clean" | "blocking" | "failed" | "cancelled";
  round?: number;
  count?: number;
  blocking?: number;
  findings?: string;
  started_at?: string;
  ended_at?: string;
}

/** The independent review in a line for the task's page ("Nothing blocking · 2 findings"); undefined when there is
 *  none to show (none ran, or it was cancelled because the work went on). */
export function autoReviewLine(sup: Supervision | undefined): string | undefined {
  const a = sup?.auto_review;
  if (!a) return undefined;
  const findings = a.count ? t("assistant-tasks.auto_review_findings", { count: a.count }) : "";
  switch (a.state) {
    case "running":
      return t("assistant-tasks.auto_review_running");
    case "clean":
      return [t("assistant-tasks.auto_review_clean"), findings].filter(Boolean).join(" · ");
    case "blocking":
      return [t("assistant-tasks.auto_review_blocking", { count: a.blocking ?? 1 }), findings].filter(Boolean).join(" · ");
    case "failed":
      return t("assistant-tasks.auto_review_failed");
  }
  return undefined;
}

export type ReportKind = "progress" | "question" | "blocked" | "done";

/** A supervised task's agent's latest report: the step of how many (0 or absent: not said), what it said in a line. */
export interface TaskProgress {
  kind: ReportKind;
  step?: number;
  steps?: number;
  /** The agent's own words, in one line. */
  text: string;
  /** The report paraphrased in one sentence in the user's language (#3181); absent until the hub's model wrote it, with
   *  no model, or on an older hub. */
  line?: string;
  /** The report as the agent wrote it (Markdown); absent when it is `text`, or on an older hub. */
  detail?: string;
  at?: string;
}

/** Whether its supervisor's acceptance still stands: the latest review accepted it and no review came since. That
 *  holds even when the hub reopened it because its agent was sent more work (#3156): the task is accepted until a new
 *  review says otherwise (docs/assistant/todo-status-sop.md, "Accepted tasks"). */
export function acceptanceStands(todo: { supervision?: Supervision }): boolean {
  const sup = todo.supervision;
  return sup?.state === "accepted" && latestReview(sup)?.verdict === "accepted";
}

/** The review state that holds now: a cancelled task reopened goes on again; an accepted one stays accepted while
 *  its acceptance stands (acceptanceStands); a parked one that was closed is no longer parked. */
export function reviewState(todo: Pick<Todo, "done_at"> & { supervision?: Supervision }): ReviewState {
  const st = todo.supervision?.state ?? "";
  if (!todo.done_at && st === "accepted" && !acceptanceStands(todo)) return "";
  if (!todo.done_at && st === "cancelled") return "";
  if (st === "parked" && (todo.done_at || !todo.supervision?.parked)) return "";
  return st;
}

/** "Parked until Nov 14, 9:00 AM · The design team has to pick the icon set first": a parked task in a line, for its
 *  row among the tasks in progress and for its page; undefined for a task that is not parked. */
export function parkedLine(todo: (Pick<Todo, "done_at"> & { supervision?: Supervision }) | undefined): string | undefined {
  const p = todo?.supervision?.parked;
  if (!todo || !p || reviewState(todo) !== "parked") return undefined;
  const time = formatDate(p.until, { dateStyle: "medium", timeStyle: "short" });
  return time ? t("assistant-tasks.review_parked_until", { time, reason: p.reason }) : t("assistant-tasks.review_parked_reason", { reason: p.reason });
}

export type ReviewTone = "accent" | "red" | "orange" | "green" | "muted";

/** What the state is called and how it is coloured; undefined for a task that is not supervised. */
export function reviewBadge(todo: Pick<Todo, "done_at"> & { supervision?: Supervision }): { label: string; tone: ReviewTone } | undefined {
  const sup = todo.supervision;
  if (!sup) return undefined;
  switch (reviewState(todo)) {
    case "pending_review":
      return { label: t("assistant-tasks.review_pending"), tone: "accent" };
    case "rework":
      return { label: t("assistant-tasks.review_rework", { round: sup.round ?? 1 }), tone: "orange" };
    case "needs_user":
      return { label: t("assistant-tasks.review_needs_user"), tone: "orange" };
    case "parked":
      return { label: t("assistant-tasks.review_parked"), tone: "muted" };
    case "accepted":
      return { label: t("assistant-tasks.review_accepted"), tone: "green" };
    case "cancelled":
      return { label: t("assistant-tasks.review_cancelled"), tone: "muted" };
  }
  return { label: t("assistant-tasks.review_ongoing"), tone: "muted" };
}

export type CheckMark = "met" | "missing" | "open" | "unchecked";

export interface ChecklistEntry {
  criterion: string;
  mark: CheckMark;
  evidence?: string;
  at?: string;
  by?: string;
}

/**
 * Current criterion progress, independent of the task's acceptance. Older hubs
 * omit criteria: retain their review-derived display, including unchecked entries
 * added after a standing acceptance (#3156).
 */
export function checklist(sup: Supervision | undefined): ChecklistEntry[] {
  if (!sup) return [];
  const criteria = sup.done_when ?? [];
  const current = criteria.length ? sup.criteria : undefined;
  const latest = new Map<string, ChecklistEntry>();
  for (const c of current ?? sup.reviews?.flatMap((r) => r.checks ?? []) ?? []) {
    latest.set(c.criterion, {
      criterion: c.criterion,
      mark: "state" in c ? (c.state === "met" ? "met" : c.state === "not_met" ? "missing" : "open") : c.met ? "met" : "missing",
      evidence: c.evidence || undefined,
      ...("state" in c ? { at: c.at, by: c.by } : {}),
    });
  }
  const settled = !current && acceptanceStands({ supervision: sup });
  return [
    ...criteria.map((criterion): ChecklistEntry => latest.get(criterion) ?? { criterion, mark: settled ? "unchecked" : "open", evidence: undefined }),
    ...(current ? [] : [...latest.values()].filter((c) => !criteria.includes(c.criterion))),
  ];
}

/** The latest review, if any. */
export function latestReview(sup: Supervision | undefined): TaskReview | undefined {
  const list = sup?.reviews ?? [];
  return list[list.length - 1];
}

/** "2/3": criteria met of all, for a card; "" without criteria. Criteria left unchecked by a standing acceptance
 *  are not counted (an accepted task reads "5/5", not "5/8"). */
export function checkCount(sup: Supervision | undefined): string {
  const list = checklist(sup).filter((c) => c.mark !== "unchecked");
  if (list.length === 0) return "";
  return `${list.filter((c) => c.mark === "met").length}/${list.length}`;
}

/** "Step 3/5 · …", "Asked Persona: …": the agent's latest report in a line for a card, in the hub's paraphrase when it
 *  has one (#3181), else in the agent's words; undefined when it has none. */
export function progressLine(sup: Supervision | undefined): string | undefined {
  const p = sup?.progress;
  const text = p?.line?.trim() || p?.text;
  if (!p || !text) return undefined;
  return reportLine(p.kind, text, p.step, p.steps);
}

/** The agent's latest report as it wrote it, for the task's details (Markdown); undefined when it has none. */
export function reportText(sup: Supervision | undefined): string | undefined {
  const p = sup?.progress;
  return p?.detail?.trim() || p?.text?.trim() || undefined;
}

/** A report (on a card, or a feed item of kind "report") in a line. */
export function reportLine(kind: string, text: string, step?: number, steps?: number): string {
  switch (kind) {
    case "question":
      return t("assistant-tasks.report_question", { text });
    case "blocked":
      return t("assistant-tasks.report_blocked", { text });
    case "done":
      return t("assistant-tasks.report_done", { text });
  }
  if (step && steps) return t("assistant-tasks.report_step_of", { step, steps, text });
  if (step) return t("assistant-tasks.report_step", { step, text });
  return text;
}

/** A review in the conversation's feed (kind "review"), in a line. */
export function reviewVerdictLabel(verdict: string, round?: number): string {
  switch (verdict) {
    case "accepted":
      return t("assistant-tasks.review_accepted");
    case "rework":
      return t("assistant-tasks.review_rework", { round: round ?? 1 });
    case "needs_user":
      return t("assistant-tasks.review_needs_user");
    case "parked":
      return t("assistant-tasks.review_parked");
    case "cancelled":
      return t("assistant-tasks.review_cancelled");
  }
  return t("assistant-tasks.review_line");
}
