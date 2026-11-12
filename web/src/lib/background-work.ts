import { t } from "../i18n";
import type { Session, Subagent, Workflow } from "./types";
import type { SessionLookup, Todo } from "./todos";
import { waitingOnWorkflow, workflowCounts } from "./workflows";
import { runningChildren } from "./child-sessions";

/** Live work, not a waiting label: summary counts win over omitted detail arrays. */
export interface BackgroundWork {
  tasks: number;
  subagents: number;
  workflows: number;
  workflow?: Workflow;
}

export function backgroundWork(session: Session | undefined): BackgroundWork | undefined {
  if (!session) return undefined;
  const background = session.background ?? [];
  const summary = typeof session.waiting === "boolean";
  const workflow = summary ? session.workflow_progress : session.workflows?.find((w) => w.running);
  const workflows = summary ? session.running_workflows ?? 0 : session.workflows?.filter((w) => w.running).length
    ?? background.filter((b) => b.kind === "workflow").length;
  const own = summary ? session.running_subagents ?? 0 : session.subagents?.filter((a) => a.running).length
    ?? background.filter((b) => b.kind === "agent").length;
  // its child sessions at work (#5120) are its subagents too; a Paseo parent may list the same ones as background
  // agents already, so the larger count stands rather than their sum
  const subagents = Math.max(own, runningChildren(session));
  // Old summaries expose only a raw background count, which may include workflows and agents. It cannot be split
  // reliably: retain a generic active-work line rather than invent an independent job count.
  const tasks = session.background_tasks_count ?? (summary ? 0
    : background.filter((b) => b.kind !== "workflow" && b.kind !== "agent").length);
  return tasks || subagents || workflows || session.background_working || (summary && session.background_tasks_count === undefined && (session.background_count ?? 0) > 0)
    ? { tasks, subagents, workflows, workflow } : undefined;
}

/** The hub says the agent's turn is over and a subagent it launched in the background still runs (#5030, cindy's
 *  background subtasks): the chat draws that turn finished (its summary folded, the subagent's card running inside it),
 *  and the bar above the composer says model usage goes on. A running workflow says so first (workflows.ts
 *  backgroundLine). */
export function waitingOnSubagents(s: Session | undefined, subagents: Subagent[] | undefined = s?.subagents): boolean {
  return Boolean(s?.background_working) && !waitingOnWorkflow(s) && (subagents ?? []).some((a) => a.running && a.background);
}

/** One task can have several active agents: do not mistake a quiet lead for an idle task. */
export function taskBackgroundWork(todo: Todo | undefined, lookup: SessionLookup): BackgroundWork | undefined {
  if (!todo || todo.done_at) return undefined;
  // Deduplicate identities before lookup: an answered-state adapter may return a fresh object on each call.
  const bindings = new Map((todo.agents ?? []).filter((a) => !a.released_at).map((a) => [JSON.stringify([a.machine_id, a.session_id]), a]));
  const work = [...bindings.values()].map((a) => backgroundWork(lookup(a.machine_id, a.session_id))).filter((w): w is BackgroundWork => !!w);
  if (!work.length) return undefined;
  const workflows = work.flatMap((w) => w.workflow ? [w.workflow] : []).sort((a, b) => (b.started_at ?? "").localeCompare(a.started_at ?? ""));
  return { tasks: work.reduce((n, w) => n + w.tasks, 0), subagents: work.reduce((n, w) => n + w.subagents, 0),
    workflows: work.reduce((n, w) => n + w.workflows, 0), workflow: workflows[0] };
}

/** Same phase and done/started count as the Agent's running footer; other work stays explicit. */
export function backgroundWorkLine(work: BackgroundWork | undefined): string | undefined {
  if (!work) return undefined;
  const parts: string[] = [];
  if (work.workflows) {
    parts.push([t("chat-runningstatusbar.background_workflow"), work.workflow?.phase, work.workflow ? workflowCounts(work.workflow) : undefined,
      work.workflows > 1 ? t("chat-runningstatusbar.more_workflows", { count: work.workflows - 1 }) : undefined].filter(Boolean).join(" · "));
  }
  if (work.tasks) parts.push(t("assistant-tasks.background_tasks_running", { count: work.tasks }));
  if (work.subagents) parts.push(t("assistant-tasks.subagents_count_running", { count: work.subagents }));
  return parts.join(" · ") || t("assistant-tasks.background_running");
}
