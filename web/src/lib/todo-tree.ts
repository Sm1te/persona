import { t } from "../i18n";
import type { AgentGroup, AgentMutes } from "./agent-groups";
import { repoOf } from "./agent-repos";
import { backgroundWaiting, stateOf } from "./format";
import { buildTodoAgentGroups, interruptedAgent, todoStatus, type SessionLookup, type Todo, type TodoStatus } from "./todos";
import { usageLimitAgent } from "./usage-limit";
import type { Machine, Session } from "./types";
import { stateName } from "./state-labels";
import { sameValue } from "./stable-lines";
import type { DirectionCounts, DirectionTaskState } from "./direction-tree";

/** The one-word state every node of the tree shows (#2526): "Needs you" before "Working" before "Waiting in the
 *  background" (#6045) before "Done". */
export type TreeState = "needs_you" | "working" | "bg_waiting" | "waiting" | "done";

const TREE_RANK: Record<TreeState, number> = { needs_you: 0, working: 1, bg_waiting: 2, waiting: 3, done: 4 };

export function treeStateLabel(s: TreeState): string {
  return stateName(s);
}

/** A branch with nothing going on whose newest activity is older than this folds by default. */
export const STALE_MS = 24 * 60 * 60 * 1000;

export interface TreeAgent {
  key: string;
  session: Session;
  state: TreeState;
  /** The agents it spawned for the same todo (todo_agents.source "spawned", via this one). */
  children: TreeAgent[];
  folded: boolean;
}

export interface TreeTodo {
  key: string;
  todoId?: string;
  name: string;
  status?: TodoStatus;
  state: TreeState;
  /** A task of the direction tree (#4352): its state as the hub decided it, shown in place of `state`'s word. */
  taskState?: DirectionTaskState;
  agents: TreeAgent[];
  sessions: Session[];
  folded: boolean;
}

export interface TreeProject {
  key: string;
  name: string;
  state: TreeState;
  todos: TreeTodo[];
  sessions: Session[];
  folded: boolean;
  /** A pinned direction of the direction tree (#4352) in place of a project: its counts on the title line. */
  direction?: { counts: DirectionCounts; secretary: boolean };
}

export interface LooseGroup {
  key: string;
  name: string;
  state: TreeState;
  projects: TreeTodo[];
  sessions: Session[];
  folded: boolean;
  hint?: string;
}

export interface TodoTree {
  projects: TreeProject[];
  /** Agents on no todo, by project (one TreeTodo per project, no todoId): "Opened by hand" (opened by hand in herdr, #2623,
   *  open) then "Unassigned" (last, folded unless one needs you). */
  loose: LooseGroup[];
}

export function agentTreeState(s: Session): TreeState {
  const st = stateOf(s);
  if (usageLimitAgent(s)) return "waiting"; // a limit resetting is a wait, not work (#1758)
  if (st === "needs_you" || interruptedAgent(s)) return "needs_you";
  if (s.starting) return "working";
  if (backgroundWaiting(s)) return "bg_waiting";
  if (st === "working") return "working";
  return "done";
}

/** A todo's state in the tree from its status; a working one whose agents only wait in the background (none works, one
 *  waits) is "Waiting in the background" (#6045). */
export function todoTreeState(status: TodoStatus, agents: readonly Session[] = []): TreeState {
  if (status === "needs_you" || status === "interrupted") return "needs_you";
  if (status === "usage_limit") return "waiting";
  if (status === "working") {
    const states = agents.map(agentTreeState);
    return states.includes("bg_waiting") && !states.includes("working") ? "bg_waiting" : "working";
  }
  return "done";
}

/** The most urgent of some states ("Done" for none). */
export function worstTreeState(states: TreeState[]): TreeState {
  return worst(states);
}

function worst(states: TreeState[]): TreeState {
  return states.reduce<TreeState>((a, b) => (TREE_RANK[b] < TREE_RANK[a] ? b : a), "done");
}

function latest(sessions: Session[]): number {
  let at = 0;
  for (const s of sessions) at = Math.max(at, Date.parse(s.updated_at ?? "") || 0, Date.parse(s.state_since ?? "") || 0);
  return at;
}

/** Done, or nothing working or waiting for you and quiet for a day: such a branch starts folded. */
function stale(state: TreeState, sessions: Session[], now: number): boolean {
  return state === "done" && now - latest(sessions) > STALE_MS;
}

/** The project a set of working directories belongs to: the repository most of them sit in ("By repository"'s rule, merged across computers). */
export function projectOf(cwds: string[]): string {
  const count = new Map<string, number>();
  for (const cwd of cwds) {
    const name = repoOf(cwd).name;
    if (name) count.set(name, (count.get(name) ?? 0) + 1);
  }
  let best = "";
  let n = 0;
  for (const [name, c] of count) if (c > n || (c === n && name.localeCompare(best) < 0)) [best, n] = [name, c];
  return best;
}

/** A todo's project from its active agents (the live session's cwd, else the cwd the link recorded); "" for none. */
export function projectOfTodo(todo: Todo, lookup?: SessionLookup): string {
  const cwds = (todo.agents ?? [])
    .filter((a) => !a.released_at)
    .map((a) => lookup?.(a.machine_id, a.session_id)?.cwd || a.cwd || "");
  return projectOf(cwds);
}

const keyOf = (s: Session) => `${s.machine_id}/${s.session_id}`;

/** A todo's agents with each spawned one under the agent that spawned it (when that agent is in the list too). */
export function nestAgents(sessions: Session[], todo: Todo | undefined, now: number): TreeAgent[] {
  const parentOf = new Map<string, string>();
  for (const a of todo?.agents ?? []) if (!a.released_at && a.source === "spawned" && a.via) parentOf.set(`${a.machine_id}/${a.session_id}`, a.via);
  const listed = new Set(sessions.map(keyOf));
  const nodes = new Map<string, TreeAgent>();
  for (const s of sessions) nodes.set(keyOf(s), { key: `s:${keyOf(s)}`, session: s, state: agentTreeState(s), children: [], folded: false });
  const roots: TreeAgent[] = [];
  for (const s of sessions) {
    const key = keyOf(s);
    let p = parentOf.get(key);
    // a parent outside the list, or a loop (a → b → a), leaves it at the top
    if (p && (!listed.has(p) || cycles(key, parentOf))) p = undefined;
    if (p) nodes.get(p)!.children.push(nodes.get(key)!);
    else roots.push(nodes.get(key)!);
  }
  const settle = (n: TreeAgent): Session[] => {
    const all = [n.session, ...n.children.flatMap(settle)];
    n.state = worst([n.state, ...n.children.map((c) => c.state)]);
    n.folded = n.children.length > 0 && stale(n.state, all.slice(1), now);
    return all;
  };
  roots.forEach(settle);
  return roots;
}

function cycles(start: string, parentOf: Map<string, string>): boolean {
  const seen = new Set<string>();
  for (let q: string | undefined = start; q; q = parentOf.get(q)) {
    if (seen.has(q)) return true;
    seen.add(q);
  }
  return false;
}

function projectKey(name: string) {
  return name || "\u0000";
}

function projectName(name: string) {
  return name || t("lib-todo-tree.no_project");
}

/**
 * Agents "By to-do" as a tree (#2526): project › to-do › agent, a spawned agent under the one that spawned it, agents on no
 * todo last under "Unassigned" (by project). Each node says "Needs you" / "Working" / "Done" from what is under it; a done or day-quiet
 * branch starts folded. The todo groups and their order come from buildTodoAgentGroups; projects keep the order of
 * their first todo (the most urgent first), the project without a repository last.
 */
export function buildTodoTree(all: Session[], machines: Machine[], todos: Todo[], mutes?: AgentMutes, now = Date.now()): TodoTree {
  const groups = buildTodoAgentGroups(all, machines, todos, mutes);
  const byId = new Map(todos.map((td) => [td.id, td]));
  const projects = new Map<string, TreeProject>();
  const loose: AgentGroup[] = [];
  for (const g of groups) {
    if (!g.todoId) {
      loose.push(g);
      continue;
    }
    const todo = byId.get(g.todoId);
    const name = projectOf(g.sessions.map((s) => s.cwd));
    const key = projectKey(name);
    let p = projects.get(key);
    if (!p) {
      p = { key: `todo-tree:project:${key}`, name: projectName(name), state: "done", todos: [], sessions: [], folded: false };
      projects.set(key, p);
    }
    const status = g.todoStatus as TodoStatus | undefined;
    const state = status ? todoTreeState(status, g.sessions) : worst(g.sessions.map(agentTreeState));
    p.todos.push({
      key: `todo-tree:todo:${g.todoId}`,
      todoId: g.todoId,
      name: g.name,
      status,
      state,
      agents: nestAgents(g.sessions, todo, now),
      sessions: g.sessions,
      folded: status === "done" || stale(state, g.sessions, now),
    });
    p.sessions.push(...g.sessions);
  }
  const list = [...projects.values()];
  for (const p of list) {
    p.state = worst(p.todos.map((td) => td.state));
    p.folded = p.todos.every((td) => td.folded);
  }
  list.sort((a, b) => Number(a.key.endsWith("\u0000")) - Number(b.key.endsWith("\u0000")) || TREE_RANK[a.state] - TREE_RANK[b.state]);
  return { projects: list, loose: loose.map((g) => looseTree(g, now)) };
}

/** `next`, with every node (project, to-do, agent) and list that comes out the same as in `prev` (sameValue) replaced
 *  by `prev`'s object, matched by key; `prev` itself when nothing changed. The tree is built afresh on every push, and
 *  its rows are memoized: shared nodes keep the rows under them from rendering again (a push of one agent, or of the
 *  machines' load, re-rendered the whole tree). Sessions are leaves: kept when equal, never merged. */
export function shareTree(prev: TodoTree | undefined, next: TodoTree): TodoTree {
  return prev ? shareNode(prev, next) : next;
}

/** The fields that hold lists of tree nodes (objects with a `key`): shared node by node; any other field is a leaf. */
const NODE_LISTS = new Set(["projects", "loose", "todos", "agents", "children"]);

function shareNode<T extends object>(prev: T, next: T): T {
  const before = prev as Record<string, unknown>;
  const out: Record<string, unknown> = {};
  let same = Object.keys(before).length === Object.keys(next).length;
  for (const [k, v] of Object.entries(next)) {
    const old = before[k];
    const had = Object.prototype.hasOwnProperty.call(before, k);
    const kept = !had
      ? v
      : NODE_LISTS.has(k) && Array.isArray(old) && Array.isArray(v)
        ? shareNodes(old as { key: string }[], v as { key: string }[])
        : sameValue(old, v) ? old : v;
    if (!had || kept !== old) same = false;
    out[k] = kept;
  }
  return same ? prev : (out as T);
}

function shareNodes<T extends { key: string }>(prev: T[], next: T[]): T[] {
  const byKey = new Map(prev.map((n) => [n.key, n]));
  let same = prev.length === next.length;
  const out = next.map((n, i) => {
    const old = byKey.get(n.key);
    const kept = old ? shareNode(old, n) : n;
    if (kept !== prev[i]) same = false;
    return kept;
  });
  return same ? prev : out;
}

function looseTree(g: AgentGroup, now: number): LooseGroup {
  const byProject = new Map<string, Session[]>();
  for (const s of g.sessions) {
    const key = projectKey(repoOf(s.cwd ?? "").name);
    const list = byProject.get(key);
    if (list) list.push(s);
    else byProject.set(key, [s]);
  }
  const projects: TreeTodo[] = [...byProject.entries()].map(([key, sessions]) => {
    const state = worst(sessions.map(agentTreeState));
    return {
      key: `todo-tree:none:${key}`,
      name: projectName(key === "\u0000" ? "" : key),
      state,
      agents: nestAgents(sessions, undefined, now),
      sessions,
      folded: stale(state, sessions, now),
    };
  });
  projects.sort((a, b) => Number(a.key.endsWith("\u0000")) - Number(b.key.endsWith("\u0000")) || TREE_RANK[a.state] - TREE_RANK[b.state] || a.name.localeCompare(b.name));
  const state = worst(projects.map((p) => p.state));
  // folded below the todos (buildTodoAgentGroups), unless it says "Needs you": an interrupted agent counts here too
  return { key: g.key, name: g.name, hint: g.hint, state, projects, sessions: g.sessions, folded: Boolean(g.folded) && state !== "needs_you" };
}

/** The todos page's list by project (#2526): open todos under the project their agents work in, the ones with no agent
 *  in one group last. Each group says "Needs you" / "Working" / "Done" and starts folded when every todo in it is done-state and quiet
 *  for a day. Todos keep the order they come in (sortTodos). */
export interface TodoProjectGroup {
  key: string;
  name: string;
  state: TreeState;
  todos: Todo[];
  folded: boolean;
  /** A knowledge-base project (an area of work, #2581): open to-dos the user keeps there, so it never folds for being
   *  quiet, and "Done" (no agent at work) is not said of it. */
  area?: boolean;
}

/** A todo's agents that are still on it, as the lookup has them. */
function liveAgents(td: Todo, lookup: SessionLookup): Session[] {
  return (td.agents ?? []).filter((a) => !a.released_at).map((a) => lookup(a.machine_id, a.session_id)).filter((s): s is Session => Boolean(s));
}

/** Open to-dos by project (#2526, #2581): the knowledge-base project a to-do was put in first (named by projectName),
 *  else the repository its agents work in; those with neither last ("Unsorted"). Groups that need the user come
 *  first within each of the three; the to-dos keep their order. */
export function groupTodosByProject(todos: Todo[], lookup: SessionLookup, now = Date.now(), projectName: (slug: string) => string = (slug) => slug): TodoProjectGroup[] {
  const map = new Map<string, { name: string; tier: number; list: Todo[] }>();
  for (const td of todos) {
    const repo = td.project ? "" : projectOfTodo(td, lookup);
    const [key, name, tier] = td.project
      ? [`todos-kb:${td.project}`, projectName(td.project), 0]
      : repo
        ? [`todos-project:${repo}`, repo, 1]
        : ["todos-project:\u0000", t("lib-todo-tree.unsorted"), 2];
    const g = map.get(key) ?? { name, tier, list: [] };
    g.list.push(td);
    map.set(key, g);
  }
  const groups = [...map.entries()].map(([key, { name, tier, list }]) => {
    const states = list.map((td) => todoTreeState(todoStatus(td, lookup), liveAgents(td, lookup)));
    const state = worst(states);
    const sessions = list.flatMap((td) => (td.agents ?? []).filter((a) => !a.released_at).map((a) => lookup(a.machine_id, a.session_id)).filter((s): s is Session => Boolean(s)));
    const touched = Math.max(latest(sessions), ...list.map((td) => Date.parse(td.updated_at) || 0));
    const area = tier === 0;
    return { group: { key, name, state, todos: list, folded: !area && state === "done" && now - touched > STALE_MS, ...(area && { area }) }, tier };
  });
  return groups.sort((a, b) => a.tier - b.tier || TREE_RANK[a.group.state] - TREE_RANK[b.group.state]).map((g) => g.group);
}
