// The direction tree (#4352, lib/direction-tree.ts) as the Agents list's "By pinned" grouping draws it: pinned direction
// › task › agent › sub-agent in lib/todo-tree.ts's nodes, with live session rows.
import type { AgentMutes } from "./agent-groups";
import { directionTitle, agentRef, type DirectionAgent, type DirectionTask, type DirectionTaskState, type DirectionTree } from "./direction-tree";
import { agentTreeState, buildTodoTree, type TodoTree, type TreeAgent, type TreeProject, type TreeState, type TreeTodo, worstTreeState } from "./todo-tree";
import type { AgentState, Machine, Session } from "./types";
import { splitSubagentLink } from "./subagent-link";

/** The Agents tree's one-word state for a task's state: needs you, working, and waiting for stuck and review. */
const TREE_STATE: Record<DirectionTaskState, TreeState> = { needs_you: "needs_you", working: "working", stuck: "waiting", review: "waiting" };

/** What a session row needs of an agent the session list does not have (another assistant's, an in-session sub-agent):
 *  its title and the state the hub saw. */
function standIn(a: DirectionAgent): Session {
  const known: readonly string[] = ["needs_you", "working", "unread", "idle", "offline"];
  return { machine_id: a.machine_id, session_id: a.session_id, title: a.title, cwd: "", status: "", state: (known.includes(a.state) ? a.state : "offline") as AgentState, ...(a.agent ? { agent: a.agent } : {}) };
}

/**
 * A task's agents as tree nodes: each found in `sessions` (by "machine/session") once, nested as the hub nested them.
 * `standIns`: an agent the list does not have is drawn from what the tree says of it (the sidebar); otherwise it is
 * left out and its children move up (the Agents page, whose list is filtered). `placed` collects the agents drawn.
 * An in-session subagent ("<parent>@<agent>") is never a row, nor are the ones it launched (#5030, cindy): it is a card
 * in its parent's chat.
 */
export function directionAgentNodes(list: readonly DirectionAgent[] | undefined, sessions: ReadonlyMap<string, Session>, placed: Set<string>, standIns: boolean): TreeAgent[] {
  const out: TreeAgent[] = [];
  for (const a of list ?? []) {
    if (splitSubagentLink(a.session_id)) continue;
    const ref = agentRef(a);
    const children = directionAgentNodes(a.children, sessions, placed, standIns);
    const s = placed.has(ref) ? undefined : sessions.get(ref) ?? (standIns ? standIn(a) : undefined);
    if (!s) {
      out.push(...children);
      continue;
    }
    placed.add(ref);
    out.push({ key: `s:${ref}`, session: s, state: agentTreeState(s), children, folded: false });
  }
  return out;
}

/** A task as the Agents tree's to-do node, under `keyPrefix` (its fold is kept per key). */
export function directionTaskNode(td: DirectionTask, sessions: ReadonlyMap<string, Session>, placed: Set<string>, { keyPrefix, folded, standIns }: { keyPrefix: string; folded: boolean; standIns: boolean }): TreeTodo {
  const agents = directionAgentNodes(td.agents, sessions, placed, standIns);
  const flat = (n: TreeAgent): Session[] => [n.session, ...n.children.flatMap(flat)];
  return { key: `${keyPrefix}${td.id}`, todoId: td.id, name: td.title, state: TREE_STATE[td.state], taskState: td.state, agents, sessions: agents.flatMap(flat), folded };
}

const keyOf = (s: Session) => `${s.machine_id}/${s.session_id}`;

/**
 * Agents "By pinned" (#4352): pinned direction › task › agent › sub-agent, from the hub's tree, for the sessions listed
 * (live, filtered, unpinned). A task none of whose agents is listed is left out, and so is a direction left with no task.
 * The listed agents the tree does not hold go last, in the "By to-do" tree's own groups for agents on no to-do.
 */
export function buildDirectionAgentTree(tree: DirectionTree, all: Session[], machines: Machine[], mutes?: AgentMutes, now = Date.now()): TodoTree {
  const sessions = new Map(all.map((s) => [keyOf(s), s]));
  const placed = new Set<string>();
  const projects: TreeProject[] = [];
  for (const d of tree.directions) {
    const todos = (d.tasks ?? []).map((td) => directionTaskNode(td, sessions, placed, { keyPrefix: `direction-agents:task:${d.id}:`, folded: false, standIns: false })).filter((td) => td.agents.length > 0);
    if (todos.length === 0) continue;
    projects.push({
      key: `direction-agents:${d.id}`,
      name: directionTitle(d),
      state: worstTreeState(todos.map((td) => td.state)),
      todos,
      sessions: todos.flatMap((td) => td.sessions),
      folded: false,
      direction: { counts: d.counts, secretary: !!d.secretary },
    });
  }
  const rest = all.filter((s) => !placed.has(keyOf(s)));
  return { projects, loose: buildTodoTree(rest, machines, [], mutes, now).loose };
}
