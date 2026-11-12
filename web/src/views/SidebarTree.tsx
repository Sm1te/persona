// The Agents section's trees (by to-do, repository, space, machine, terminal) and the rows they hold. Imports only
// SidebarParts and SidebarRows among the sidebar's files.
import { memo, useCallback, useId, useLayoutEffect, useMemo, useRef } from "react";
import { t } from "../i18n";
import { isSpaceMuted } from "../lib/agent-filter";
import { isQuiet, sessionHost, type AgentMutes, type Space } from "../lib/agent-groups";
import { showsWorktreeLevel, type RepoMachine, type RepoTree, type RepoWorktree } from "../lib/agent-repos";
import { paneRoute, tabRowKeys, type ChildStates, type HerdrMachine, type HerdrSpace, type HerdrTab } from "../lib/herdr-tree";
import { machineWithHub } from "../lib/mesh";
import { agentLabel } from "../lib/mentions";
import { baseName } from "../lib/format";
import { href, navigate, useRoute, type Route } from "../lib/router";
import { terminalTarget } from "../lib/session-view";
import { useStore } from "../lib/store";
import { treeStateLabel, type TodoTree, type TreeProject, type TreeAgent, type TreeState, type TreeTodo } from "../lib/todo-tree";
import { findTodoForSpace, openTodoFromTag } from "../lib/todos";
import type { AgentState, Machine, Session } from "../lib/types";
import { Collapse, cx, sidebarFoldMotion } from "../ui/atoms";
import { StatusMark, StateTally } from "../ui/StatusMark";
import { foldedCounts, hasFoldedCounts } from "../lib/folded-counts";
import { Icon } from "../ui/icons";
import { machineNode, termPaneNode, termSpaceNode, termTabNode, type MenuNode } from "../lib/node-actions";
import { contextMenuProps } from "../ui/context-menu";
import type { MenuAt } from "../ui/menu-model";
import type { TermSpace } from "../lib/terminals";
import { useFolded } from "../lib/use-folded";
import { sameValue } from "../lib/stable-lines";
import { stateCount } from "../lib/state-labels";
import { neverConnected } from "../lib/add-computer";
import { type GoFn, GroupCount, holdsRow, type NodeMenuFn, type RegFn, rowKey, type RowMenuFn, Section, SidebarGroup } from "./SidebarParts";
import { NavRow, SessionRow } from "./SidebarRows";
import type { WorkspaceTarget } from "../lib/workspace-catalog";
import { selectWorkspace, useWorkspaceSelection } from "../lib/workspace-selection";
import { DirectionCountsText, DirectionStateText } from "./DirectionMarks";

export type TreeRowProps = { machineLabel: MachineLabel; showHost?: boolean; /** No space · computer at a row's end (the sidebar's 常驻 rows, nested deep, #4352). */ noContext?: boolean; /** Before each agent row's glide key and its children's fold key: a second copy of a row (the sidebar's 常驻 tree) keeps its own, apart from the Agents list's. */ keyPrefix?: string; reg: RegFn; hover: (k: string | null) => void; activeKey: string; go: GoFn; onRowMenu?: RowMenuFn; onNavigate?: () => void };
/** A computer's name as an agent row shows it ("" with a single computer). */
export type MachineLabel = (machineId: string) => string;

const TREE_TONE: Record<TreeState, string> = { needs_you: "text-attention", working: "text-orange", bg_waiting: "text-status-waiting", waiting: "text-ink-2", done: "text-ink-3" };

/** "Needs you" / "Working" / "Done" at the end of a tree node's line. */
const TreeStateText = memo(function TreeStateText({ state }: { state: TreeState }) {
  return <span className={cx("shrink-0 text-[11px] font-semibold whitespace-nowrap", TREE_TONE[state])}>{treeStateLabel(state)}</span>;
});

// The tree's rows are memoized, and a push hands them the nodes it did not change as the same objects (shareTree), so
// only the branches a push changed render again. The open row's key goes only to the branch that holds it ("" to the
// others, where holdsRow and `active` come out the same), so opening another agent re-renders two branches, not the
// whole tree; and the rows get the computers' names as a lookup that stays the same object while the names do, so a
// machines push (load, memory) re-renders none of them.

/** `activeKey` for a branch holding these sessions, else "". */
const within = (sessions: readonly Session[], activeKey: string) => (activeKey && holdsRow(sessions, activeKey) ? activeKey : "");
/** `activeKey` for an agent's branch (it and the agents it spawned), else "". */
const withinAgent = (a: TreeAgent, activeKey: string): string => (activeKey && (rowKey(a.session) === activeKey || a.children.some((c) => withinAgent(c, activeKey))) ? activeKey : "");

/** Each computer's row label, one lookup while the labels are the same. */
export function useMachineLabel(machines: Machine[]): MachineLabel {
  const last = useRef<Map<string, string> | null>(null);
  const names = useMemo(() => {
    const next = machines.length > 1 ? new Map(machines.map((m) => [m.id, machineWithHub(m.id, machines)])) : null;
    return sameValue(last.current, next) ? last.current : next;
  }, [machines]);
  last.current = names;
  return useCallback((machineId: string) => (names ? names.get(machineId) ?? machineId : ""), [names]);
}

/** Agents "By to-do" as project › to-do › agent (#2526): projects with their todos, a spawned agent under the one that
 *  spawned it, "Unassigned" last (by project). */
export function TodoTreeView({ tree, machines, activeKey, ...p }: { tree: TodoTree; machines: Machine[] } & Omit<TreeRowProps, "machineLabel">) {
  const machineLabel = useMachineLabel(machines);
  return (
    <>
      {tree.projects.map((pr) => (
        <TreeProjectSection key={pr.key} k={pr.key} name={pr.name} state={pr.state} folded={pr.folded} sessions={pr.sessions} todos={pr.todos} direction={pr.direction} machineLabel={machineLabel} activeKey={within(pr.sessions, activeKey)} {...p} />
      ))}
      {tree.loose.map((g) => (
        <TreeProjectSection key={g.key} k={g.key} name={g.name} state={g.state} folded={g.folded} sessions={g.sessions} todos={g.projects} hint={g.hint} loose machineLabel={machineLabel} activeKey={within(g.sessions, activeKey)} {...p} />
      ))}
    </>
  );
}

const TreeProjectSection = memo(function TreeProjectSection({ k, name, state, folded: initial, sessions, todos, loose, hint, direction, ...p }: { k: string; name: string; state: TreeState; folded: boolean; sessions: Session[]; todos: TreeTodo[]; loose?: boolean; hint?: string; direction?: TreeProject["direction"] } & TreeRowProps) {
  const [folded, toggle] = useFolded(k, initial, { follow: true, holds: holdsRow(sessions, p.activeKey) });
  return (
    <Section
      title={<span className="flex min-w-0 items-center gap-1.5"><Icon name={direction ? (direction.secretary ? "person" : "target") : loose ? "layers" : "folder"} size={12} stroke={2} className="shrink-0 text-ink-3" /><span className="truncate normal-case tracking-normal text-[11.5px] text-ink-2">{name}</span></span>}
      folded={folded}
      onToggle={toggle}
      // a pinned direction (#4352) says its two counts instead of one state word
      right={direction ? <DirectionCountsText counts={direction.counts} /> : <TreeStateText state={state} />}
      hint={hint}
    >
      {todos.map((td) => <TreeTodoRows key={td.key} td={td} {...p} activeKey={within(td.sessions, p.activeKey)} />)}
    </Section>
  );
});

/** One todo (or, under "Unassigned", one project) and its agents under its own folding line; a direction tree's task
 *  (#4352) says its task state. */
export const TreeTodoRows = memo(function TreeTodoRows({ td, ...p }: { td: TreeTodo } & TreeRowProps) {
  const [folded, toggle] = useFolded(td.key, td.folded, { follow: true, holds: holdsRow(td.sessions, p.activeKey) });
  return (
    <div className="mt-px">
      <div className="relative z-10 flex w-full min-w-0 items-center gap-1.5 rounded-control py-1 pl-2 pr-2 text-[12px] text-ink-2 hover:bg-hover">
        {td.agents.length > 0 || !td.taskState ? (
          <button type="button" aria-expanded={!folded} aria-label={t("sidebar.task_agents", { name: td.name })} onClick={toggle} className="touch-hit flex shrink-0 items-center">
            <Icon name="chevron" size={9} stroke={2.4} className="opacity-60 transition-transform" style={{ ...sidebarFoldMotion(!folded), transform: folded ? "rotate(-90deg)" : "none" }} />
          </button>
        ) : (
          // a direction's task no agent is on yet (#4352): nothing to unfold, the title stays in the column
          <span aria-hidden className="invisible flex shrink-0 items-center"><Icon name="chevron" size={9} stroke={2.4} /></span>
        )}
        {td.todoId && <Icon name="todo" size={11} stroke={2} className="shrink-0 text-ink-3" />}
        {td.todoId ? (
          <a href={href({ name: "todos", id: td.todoId })} onClick={(e) => { e.preventDefault(); openTodoFromTag(td.todoId!, navigate, p.onNavigate); }} className="min-w-0 flex-1 truncate font-medium text-ink hover:underline">{td.name}</a>
        ) : (
          <button type="button" onClick={toggle} className="min-w-0 flex-1 truncate text-left">{td.name}</button>
        )}
        {/* a direction's task no agent is on says only its state, no bare 0 beside it */}
        {(td.sessions.length > 0 || !td.taskState) && <GroupCount n={td.sessions.length} label={t("sidebar.agent_count", { count: td.sessions.length })} className="shrink-0" />}
        {td.taskState ? (
          <DirectionStateText state={td.taskState} />
        ) : td.status === "usage_limit" ? (
          <span className="shrink-0 text-[11px] font-semibold whitespace-nowrap text-orange">{t("lib-usage-limit.short")}</span>
        ) : (
          <TreeStateText state={td.state} />
        )}
      </div>
      <Collapse open={!folded} sidebarFold>
        <div className="flex flex-col gap-px pl-3" inert={folded || undefined}>
          {td.agents.map((a) => <TreeAgentRows key={a.key} a={a} depth={0} {...p} activeKey={withinAgent(a, p.activeKey)} />)}
        </div>
      </Collapse>
    </div>
  );
});

/** An agent and, folded under it, the agents it spawned for the same todo. */
const TreeAgentRows = memo(function TreeAgentRows({ a, depth, machineLabel, showHost, noContext, keyPrefix = "", reg, hover, activeKey, go, onRowMenu }: { a: TreeAgent; depth: number } & TreeRowProps) {
  const subtree = (n: TreeAgent): Session[] => n.children.flatMap((c) => [c.session, ...subtree(c)]);
  const [folded, toggle] = useFolded(keyPrefix + a.key + ":children", a.folded, { follow: true, holds: holdsRow(subtree(a), activeKey) });
  const s = a.session;
  const key = `${keyPrefix}s:${s.machine_id}/${s.session_id}`;
  const context = noContext ? undefined : [s.space_name || baseName(s.cwd), machineLabel(s.machine_id)].filter(Boolean).join(" · ");
  return (
    <div style={depth ? { paddingLeft: 12 } : undefined}>
      <div className="flex items-center">
        {a.children.length > 0 && (
          <button type="button" aria-expanded={!folded} aria-label={t("lib-todo-tree.spawned_agents", { count: a.children.length })} onClick={toggle} className="touch-hit relative z-10 -mr-1 flex size-4 shrink-0 items-center justify-center text-ink-3">
            <Icon name="chevron" size={9} stroke={2.4} className="opacity-60 transition-transform" style={{ ...sidebarFoldMotion(!folded), transform: folded ? "rotate(-90deg)" : "none" }} />
          </button>
        )}
        <div className="min-w-0 flex-1">
          <SessionRow s={s} k={key} reg={reg} hover={hover} active={activeKey === key} to={{ name: "session", mid: s.machine_id, sid: s.session_id }} go={go} hint={context} host={showHost && sessionHost(s) !== "herdr" && sessionHost(s) !== "botmux" ? sessionHost(s) : undefined} onMenu={onRowMenu} />
        </div>
      </div>
      {a.children.length > 0 && (
        <Collapse open={!folded} sidebarFold>
          <div className="flex flex-col gap-px border-l border-line ml-2" inert={folded || undefined}>
            {a.children.map((c) => <SpawnedAgentRows key={c.key} a={c} depth={depth + 1} machineLabel={machineLabel} showHost={showHost} noContext={noContext} keyPrefix={keyPrefix} reg={reg} hover={hover} activeKey={withinAgent(c, activeKey)} go={go} onRowMenu={onRowMenu} />)}
          </div>
        </Collapse>
      )}
    </div>
  );
});
// the memo again, for the spawned agents' rows: inside the function above, its own name is the bare function
const SpawnedAgentRows = TreeAgentRows;

/** Exact-target hooks for workspace menus and empty selections. The tree never infers action destinations from cwd. */
export interface WorkspaceTreeActions {
  onSelectWorkspace?: (target: WorkspaceTarget) => void;
  onWorkspaceMenu?: (target: WorkspaceTarget, anchor: HTMLElement, at?: MenuAt) => void;
}

const holdsWorkspace = (w: RepoWorktree, key?: string): boolean => Boolean(key && (w.key === key || w.children.some((child) => holdsWorkspace(child, key))));

/** One computer in the repository tree (#2108 / #2126): header folds the whole computer; flat when only one computer has agents. */
export function RepoMachineSection({ m, flat, machines, reg, hover, activeKey, go, onRowMenu, ...actions }: { m: RepoMachine; flat: boolean; machines: Machine[]; reg: RegFn; hover: (k: string | null) => void; activeKey: string; go: GoFn; onRowMenu: RowMenuFn } & WorkspaceTreeActions) {
  const online = m.machine.online;
  const selected = useWorkspaceSelection();
  const onSelectWorkspace = (target: WorkspaceTarget) => { selectWorkspace(target); actions.onSelectWorkspace?.(target); };
  const [folded, toggle] = useFolded(m.foldKey, !online, { holds: m.repos.some((r) => r.worktrees.some((w) => holdsRow(w.navigationTree ?? w.tree, activeKey) || holdsWorkspace(w, selected))) });
  const repos = m.repos.map((tree) => <RepoSection key={tree.key} tree={tree} reg={reg} hover={hover} activeKey={activeKey} go={go} onRowMenu={onRowMenu} selected={selected} {...actions} onSelectWorkspace={onSelectWorkspace} />);
  if (flat) return <>{repos}</>;
  return (
    <SidebarGroup className="mt-3" heading={
      <div className="group/sec flex items-center gap-1 px-2 pb-0.5">
        <button type="button" aria-expanded={!folded} onClick={toggle} className="relative z-10 flex min-h-7 min-w-0 flex-1 items-center gap-1.5 whitespace-nowrap text-left [@media(pointer:coarse)]:min-h-11">
          <span aria-hidden className={cx("size-1.5 shrink-0 rounded-full", online ? "bg-green" : "bg-line-strong")} />
          <span className={cx("min-w-0 truncate text-[12.5px] font-semibold", online ? "text-ink" : "text-ink-3")}>{machineWithHub(m.machine.id, machines)}</span>
          <span className="sr-only">{online ? t("sidebar.online") : neverConnected(m.machine) ? t("sidebar.not_connected_yet") : t("sidebar.offline")}</span>
          <Icon name="chevron" size={10} stroke={2.4} className="shrink-0 text-ink-3 transition-transform" style={{ ...sidebarFoldMotion(!folded), transform: folded ? "rotate(-90deg)" : "none" }} />
        </button>
        {m.needs > 0 && !(folded && online) ? (
          <span title={stateCount("needs_you", m.needs)} className="flex h-4 min-w-4 shrink-0 items-center justify-center rounded-full bg-field px-1 text-[10.5px] font-semibold text-attention tabular-nums">
            {m.needs}<span className="sr-only"> {t("sidebar.needs_you_suffix", { count: m.needs })}</span>
          </span>
        ) : !online ? (
          <span className="shrink-0 text-[10.5px] whitespace-nowrap text-ink-3">{neverConnected(m.machine) ? t("sidebar.not_connected_yet") : t("sidebar.offline")}</span>
        ) : (
          <SessionTally sessions={m.sessions} folded={folded} />
        )}
      </div>
      }>
      <Collapse open={!folded} sidebarFold unclipped>
        <div className="flex flex-col gap-px" inert={folded || undefined}>
          {repos}
        </div>
      </Collapse>
    </SidebarGroup>
  );
}

/** One repository on one machine ("By repo"): its working trees under it, the main checkout marked and first. */
function RepoSection({ tree, reg, hover, activeKey, go, onRowMenu, selected, ...actions }: { tree: RepoTree; reg: RegFn; hover: (k: string | null) => void; activeKey: string; go: GoFn; onRowMenu: RowMenuFn; selected?: string } & WorkspaceTreeActions) {
  const [folded, toggle] = useFolded(tree.key, false, { holds: tree.worktrees.some((w) => holdsRow(w.navigationTree ?? w.tree, activeKey) || holdsWorkspace(w, selected)) });
  const row = (s: Session, hint: string) => {
    const key = `s:${s.machine_id}/${s.session_id}`;
    return <SessionRow key={key} s={s} k={key} reg={reg} hover={hover} active={activeKey === key} to={{ name: "session", mid: s.machine_id, sid: s.session_id }} go={go} hint={hint} onMenu={onRowMenu} />;
  };
  // A lone main checkout, or an Orca worktree whose folder names its project, needs no extra level.
  const flat = !showsWorktreeLevel(tree);
  return (
    <Section
      title={<span className="flex min-w-0 items-center gap-1.5"><Icon name="folder" size={12} stroke={2} className="shrink-0 text-ink-3" /><span className="truncate">{tree.name}</span></span>}
      folded={folded}
      onToggle={toggle}
      right={<GroupCount n={tree.count} label={t("sidebar.agent_count", { count: tree.count })} />}
    >
      {flat ? tree.worktrees[0]!.sessions.map((s) => row(s, tree.name)) : tree.worktrees.map((w) => <RepoWorktreeRows key={w.key} w={w} repo={tree.name} row={row} activeKey={activeKey} go={go} selected={selected} {...actions} />)}
    </Section>
  );
}

/** One working tree's agents under its own folding row: the main checkout wears a home mark, and a tree with nothing
 *  running says so. Orca child worktrees have their own remembered, initially expanded chip after the parent's agents. */
function RepoWorktreeRows({ w, repo, row, activeKey, go, selected, ...actions }: { w: RepoWorktree; repo: string; row: (s: Session, hint: string) => React.ReactNode; activeKey: string; go: GoFn; selected?: string } & WorkspaceTreeActions) {
  const [folded, toggle] = useFolded(w.key, !w.workspace && w.sessions.length === 0, { holds: holdsRow(w.navigationTree ?? w.tree, activeKey) || w.children.some((child) => holdsWorkspace(child, selected)) });
  const descendants = w.navigationTree ? w.children.flatMap((c) => c.navigationTree ?? c.tree) : w.tree.slice(w.sessions.length);
  const [childrenFolded, toggleChildren] = useFolded(`${w.key}/children`, false, { holds: holdsRow(descendants, activeKey) || w.children.some((child) => holdsWorkspace(child, selected)) });
  const selectedRow = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const row = selectedRow.current;
    if (selected !== w.key || !row || row.closest("[inert]") || !row.getClientRects().length) return;
    row.scrollIntoView?.({ block: "nearest" });
  }, [selected, w.key]);
  const mutes = useStore((st) => st.mutes);
  const running = w.sessions.filter((s) => !isQuiet(s, mutes)).length;
  const title = [w.workspace?.project_name, w.name, w.branch, w.path, w.workspace?.source].filter(Boolean).join(" · ");
  const label = <><span className="min-w-0 truncate">{w.name}</span>{w.workspace?.project_name && w.workspace.project_name !== repo && <span className="min-w-0 max-w-24 truncate text-ink-3">{w.workspace.project_name}</span>}</>;
  return (
    <div ref={selectedRow} className="mt-px" data-workspace-key={w.workspace ? w.key : undefined}>
      <div className={cx("group/sec relative z-10 flex w-full min-w-0 items-center gap-1.5 whitespace-nowrap rounded-control py-1 pl-3 pr-2 text-left text-[11.5px] text-ink-2 hover:bg-hover", selected === w.key && "bg-hover")} {...(w.target && actions.onWorkspaceMenu ? contextMenuProps((at, anchor) => actions.onWorkspaceMenu!(w.target!, anchor, at)) : {})}>
        <button type="button" aria-label={t("workspace-tree.disclosure", { name: w.name })} aria-expanded={!folded} onClick={toggle} className="touch-hit relative flex min-h-7 shrink-0 items-center [@media(pointer:coarse)]:min-h-11">
          <Icon name="chevron" size={9} stroke={2.4} className="shrink-0 opacity-60 transition-transform" style={{ ...sidebarFoldMotion(!folded), transform: folded ? "rotate(-90deg)" : "none" }} />
        </button>
        {w.main && <Icon name="home" size={11} stroke={2} className="shrink-0 text-ink-3" />}
        <button type="button" title={title} aria-pressed={selected === w.key} aria-expanded={!folded} onClick={() => { if (w.target) actions.onSelectWorkspace?.(w.target); toggle(); }} className="flex min-h-7 min-w-0 flex-1 items-center gap-1.5 text-left hover:text-ink [@media(pointer:coarse)]:min-h-11">{label}</button>
        {w.workspace?.available === false && <Icon name="warning" size={11} className="shrink-0 text-ink-3" aria-label={t("workspace-tree.unavailable")} />}
        <span className="ml-auto shrink-0 text-[10.5px] text-ink-3 tabular-nums">{running > 0 ? t("sidebar.worktree_agents", { count: w.sessions.length }) : t("sidebar.idle")}</span>
        {w.target && actions.onWorkspaceMenu && <button type="button" data-row-menu aria-label={t("workspace-tree.actions", { name: w.name })} aria-haspopup="menu" onClick={(e) => actions.onWorkspaceMenu!(w.target!, e.currentTarget)} className="touch-hit relative flex size-5 shrink-0 items-center justify-center rounded-control text-ink-3 hover:bg-hover-2 focus-visible:text-ink [@media(hover:none)]:size-8"><Icon name="more" size={12} /></button>}
      </div>
      <Collapse open={!folded} sidebarFold>
        <div inert={folded || undefined}>
          <div className="flex flex-col gap-px pl-2">{w.sessions.map((s) => row(s, `${repo} › ${w.name}`))}</div>
          {w.children.length > 0 && (
            <>
              <RepoChildToggle count={w.children.length} counts={w.childStates} folded={childrenFolded} onToggle={toggleChildren} />
              <Collapse open={!childrenFolded} sidebarFold>
                <div className="ml-3.5 flex w-[calc(100%-0.875rem)] flex-col gap-px" inert={childrenFolded || undefined}>
                  {w.children.map((c) => <RepoWorktreeRows key={c.key} w={c} repo={repo} row={row} activeKey={activeKey} go={go} selected={selected} {...actions} />)}
                </div>
              </Collapse>
            </>
          )}
        </div>
      </Collapse>
    </div>
  );
}

/** Orca's child-workspace chip: one status per direct child, summarized from that child's whole subtree. */
function RepoChildToggle({ count, counts, folded, onToggle }: { count: number; counts: ChildStates; folded: boolean; onToggle: () => void }) {
  const descriptionId = useId();
  const parts = (["needs_you", "working", "done"] as const).filter((k) => counts[k] > 0);
  const description = parts.map((k) => stateCount(k, counts[k])).join(t("sidebar.list_separator"));
  const action = t(folded ? "sidebar.show_child_workspaces" : "sidebar.hide_child_workspaces", { count });
  return (
    <div className="@container relative mt-1 flex min-w-0 justify-start pl-7">
      <button
        type="button"
        aria-expanded={!folded}
        aria-label={action}
        aria-describedby={description ? descriptionId : undefined}
        title={description ? `${action}\n${description}` : action}
        onClick={(e) => {
          e.preventDefault();
          e.stopPropagation();
          onToggle();
        }}
        className="relative z-10 flex min-h-4 max-w-full items-center whitespace-nowrap rounded-[4px] border border-line bg-surface px-0.5 text-[10px] leading-none text-ink-3 shadow-hairline transition-colors hover:bg-hover hover:text-ink-2 focus-visible:bg-hover focus-visible:ring-2 focus-visible:ring-canvas"
      >
        <span className="inline-flex min-w-0 max-w-full flex-wrap items-center gap-x-1 gap-y-0.5 py-0.5">
          <span className="inline-flex shrink-0 items-center gap-1">
            <Icon name="workflow" size={10} stroke={2.2} className="shrink-0" />
            <span className={cx("tabular-nums", parts.length > 0 && "hidden @min-[13rem]:inline")}>{t("sidebar.child_worktree_count", { count })}</span>
            {parts.length > 0 && <span className="tabular-nums @min-[13rem]:hidden">{count}</span>}
          </span>
          <span className="inline-flex min-w-0 max-w-full items-center gap-0.5">
            {parts.length > 0 && (
              <span className="inline-flex min-w-0 flex-wrap items-center gap-0.5 border-l border-line pl-1 text-ink-3 @max-[13rem]:pl-0.5">
                {parts.map((k) => (
                  <span key={k} aria-hidden className="inline-flex shrink-0 items-center gap-0.5 tabular-nums @max-[13rem]:gap-0">
                    <StatusMark state={k === "done" ? "unread" : k} size="sm" label={false} />
                    {counts[k]}
                  </span>
                ))}
              </span>
            )}
            <Icon name="chevron" size={10} stroke={2.4} className="size-2.5 shrink-0 text-ink-3 transition-transform motion-reduce:transition-none" style={{ ...sidebarFoldMotion(!folded), transform: folded ? "rotate(-90deg)" : "none" }} />
          </span>
        </span>
      </button>
      {description && <span id={descriptionId} className="sr-only">{description}</span>}
    </div>
  );
}

export function SpaceSection({ sp, machineName, reg, hover, activeKey, go, pinned, muted, onMenu, onRowMenu, onNavigate }: { sp: Space; machineName?: string; reg: RegFn; hover: (k: string | null) => void; activeKey: string; go: GoFn; pinned: boolean; muted: boolean; onMenu: SpaceMenuFn; onRowMenu: RowMenuFn; onNavigate?: () => void }) {
  const mutes = useStore((st) => st.mutes);
  const allIdle = sp.sessions.every((s) => isQuiet(s, mutes));
  const [folded, toggle] = useFolded(`space:${sp.key}`, allIdle && sp.sessions.length > 3, { holds: holdsRow(sp.sessions, activeKey) });

  return (
    <Section
      folded={folded}
      onToggle={toggle}
      onMenu={(at, el) => onMenu(sp, el, at, { folded, toggle })}
      title={
        <span className="flex items-center gap-1.5">
          <span className="truncate normal-case tracking-normal text-[11.5px] text-ink-2">{sp.name}</span>
          <SpaceMarks pinned={pinned} muted={muted} />
          {machineName && <span className="truncate normal-case tracking-normal text-[11px] font-normal text-ink-3">{machineName}</span>}
        </span>
      }
      right={
        <>
          <span className={cx("flex items-center gap-1", MENU_STEP_ASIDE, MENU_TOUCH_ROOM)}>
            <SpaceTodoTag sp={sp} onNavigate={onNavigate} />
            <span className={MENU_HIDES}><SessionTally sessions={sp.sessions} folded={folded} /></span>
          </span>
          <SpaceMenuButton sp={sp} onMenu={onMenu} />
        </>
      }
    >
      {sp.sessions.map((s) => (
        <SessionRow key={s.session_id} s={s} k={`s:${s.machine_id}/${s.session_id}`} reg={reg} hover={hover} active={activeKey === `s:${s.machine_id}/${s.session_id}`} to={{ name: "session", mid: s.machine_id, sid: s.session_id }} go={go} tab showTodoTag onMenu={onRowMenu} />
      ))}
    </Section>
  );
}

function SpaceMarks({ pinned, muted }: { pinned: boolean; muted: boolean }) {
  return (
    <>
      {pinned && <span title={t("sidebar.pinned")} className="shrink-0 text-accent"><Icon name="pin" size={10} stroke={2.2} /><span className="sr-only">{t("sidebar.pinned")}</span></span>}
      {muted && <span title={t("sidebar.muted")} className="shrink-0 text-ink-3"><Icon name="bellOff" size={10} stroke={2.2} /><span className="sr-only">{t("sidebar.muted")}</span></span>}
    </>
  );
}

// The todo a space hangs on (docs/assistant/todos.md), when the hub supports both: a tag that opens it.
function SpaceTodoTag({ sp, onNavigate }: { sp: Space; onNavigate?: () => void }) {
  const todos = useStore((st) => st.todos);
  const todosSupported = useStore((st) => st.todosSupported);
  const todosLoaded = useStore((st) => st.todosLoaded);
  const spacesSupported = useStore((st) => st.spacesSupported);
  const supportSpaces = todosLoaded && todosSupported !== false && spacesSupported !== false;
  const linkedTodo = supportSpaces ? findTodoForSpace(todos, sp.machineId, sp.name) : undefined;
  if (!linkedTodo) return null;
  return (
    <button
      type="button"
      onClick={(e) => {
        e.stopPropagation();
        openTodoFromTag(linkedTodo.id, navigate, onNavigate);
      }}
      className="relative z-10 flex shrink-0 items-center justify-center rounded bg-hover px-1 py-0.5 text-ink-3 hover:bg-hover-2 hover:text-ink transition-colors cursor-pointer"
      title={t("sidebar.todo_tag", { title: linkedTodo.title })}
      aria-label={t("sidebar.todo_tag", { title: linkedTodo.title })}
    >
      <Icon name="todo" size={10} stroke={2} />
    </button>
  );
}

// A header row's ⋯ lies over the row's right end (absolute, so it reserves no width and every row's count keeps the
// same right edge, the machine row's, which has none). With a pointer it shows on hover or keyboard focus and the count
// under it steps aside (MENU_HIDES) with whatever sits beside the count (MENU_STEP_ASIDE); on a touch screen it is
// always there and the row keeps room for it (MENU_TOUCH_ROOM here, the 44px the tree's headers reserve).
// (Written out in full: Tailwind finds class names by scanning the source for them.)
const MENU_HIDES = "group-hover/sec:invisible group-has-[[data-row-menu]:focus-visible]/sec:invisible";
const MENU_STEP_ASIDE = "group-hover/sec:-translate-x-5 group-has-[[data-row-menu]:focus-visible]/sec:-translate-x-5";
const MENU_TOUCH_ROOM = "[@media(hover:none)]:mr-9";

// A space's ⋯ (rename, pin, mute, mark seen, link a todo): on hover with a pointer, always on a touch screen.
function SpaceMenuButton({ sp, onMenu }: { sp: Space; onMenu: (sp: Space, anchor: HTMLElement) => void }) {
  return (
    <span className="absolute top-1/2 right-2 z-10 -translate-y-1/2">
      <button
        type="button"
        data-row-menu
        aria-label={t("sidebar.space_actions_named", { name: sp.name })}
        aria-haspopup="menu"
        title={t("sidebar.space_actions")}
        onClick={(e) => onMenu(sp, e.currentTarget)}
        className="touch-hit relative flex size-5 items-center justify-center rounded-[5px] text-ink-3 opacity-0 transition-opacity duration-150 group-hover/sec:opacity-100 hover:bg-hover-2 hover:text-ink focus-visible:opacity-100 [@media(hover:none)]:size-8 [@media(hover:none)]:opacity-100"
      >
        <Icon name="more" size={12} />
      </button>
    </span>
  );
}

export type SpaceMenuFn = (sp: Space, anchor: HTMLElement, at?: MenuAt, fold?: MenuNode["fold"]) => void;
type TreeProps = { machines: Machine[]; reg: RegFn; hover: (k: string | null) => void; activeKey: string; go: GoFn; mutes: AgentMutes; onSpaceMenu: SpaceMenuFn; onRowMenu: RowMenuFn; onNodeMenu: NodeMenuFn; onNavigate?: () => void };

// "By machine" (lib/herdr-tree.ts): one machine as herdr lays it out, its workspaces, their tabs, then "Other". A machine that
// is away starts folded; its agents are under "Recent (not running)".
export function MachineSection({ m, shells, ...p }: { m: HerdrMachine; shells?: TermSpace[] } & TreeProps) {
  const online = m.machine.online;
  const [folded, toggle] = useFolded(m.foldKey, !online, { holds: holdsRow(m.sessions, p.activeKey) });
  const name = machineWithHub(m.machine.id, p.machines);
  return (
    <SidebarGroup className="mt-3" heading={
      <div className="group/sec flex items-center gap-1 px-2 pb-0.5 [@media(hover:none)]:pr-11" {...contextMenuProps((at) => p.onNodeMenu(machineNode(m.machine, { fold: { folded, toggle } }), at, name))}>
        <button type="button" aria-expanded={!folded} onClick={toggle} className="relative z-10 flex min-h-7 min-w-0 flex-1 items-center gap-1.5 whitespace-nowrap text-left [@media(pointer:coarse)]:min-h-11">
          <span aria-hidden className={cx("size-1.5 shrink-0 rounded-full", online ? "bg-green" : "bg-line-strong")} />
          <span className={cx("min-w-0 truncate text-[12.5px] font-semibold", online ? "text-ink" : "text-ink-3")}>{machineWithHub(m.machine.id, p.machines)}</span>
          <span className="sr-only">{online ? t("sidebar.online") : neverConnected(m.machine) ? t("sidebar.not_connected_yet") : t("sidebar.offline")}</span>
          <Icon name="chevron" size={10} stroke={2.4} className="shrink-0 text-ink-3 transition-transform" style={{ ...sidebarFoldMotion(!folded), transform: folded ? "rotate(-90deg)" : "none" }} />
        </button>
        {m.needs > 0 && !(folded && online) ? (
          <span title={stateCount("needs_you", m.needs)} className="flex h-4 min-w-4 shrink-0 items-center justify-center rounded-full bg-field px-1 text-[10.5px] font-semibold text-attention tabular-nums">
            {m.needs}<span className="sr-only"> {t("sidebar.needs_you_suffix", { count: m.needs })}</span>
          </span>
        ) : !online ? (
          <span className="shrink-0 text-[10.5px] whitespace-nowrap text-ink-3">{neverConnected(m.machine) ? t("sidebar.not_connected_yet") : t("sidebar.offline")}</span>
        ) : (
          <SessionTally sessions={m.sessions} folded={folded} />
        )}
      </div>
      }>
      <Collapse open={!folded} sidebarFold unclipped>
        <div className="flex flex-col gap-px" inert={folded || undefined}>
          {m.spaces.map((sp) => <WorkspaceBlock key={sp.key} sp={sp} {...p} />)}
          {shells && shells.length > 0 && <ShellTree mid={m.machine.id} online={online} spaces={shells} {...p} />}
          {m.other.length > 0 && (
            // "Other": the machine's agents outside any workspace (Codex threads, runtimes without spaces)
            <TreeBlock foldKey={m.otherFoldKey} sessions={m.other} activeKey={p.activeKey} title={<span className="min-w-0 truncate font-medium">{t("sidebar.other")}</span>} right={(folded) => <SessionTally sessions={m.other} folded={folded} />}>
              {m.other.map((s) => <TreeSessionRow key={s.session_id} s={s} {...p} />)}
            </TreeBlock>
          )}
          {!m.sessions.length && <p className="truncate px-3 py-1 text-[12px] text-ink-3">{online ? t("sidebar.none_running") : t("sidebar.agents_in_recent")}</p>}
        </div>
      </Collapse>
    </SidebarGroup>
  );
}

// A workspace or "Other" in the tree: its title row folds what is under it, the fold kept by machine/space name.
function TreeBlock({ foldKey, sessions, activeKey, state, title, right, children, onMenu }: { foldKey: string; sessions: Session[]; activeKey: string; state?: AgentState; title: React.ReactNode; right: (folded: boolean) => React.ReactNode; children: React.ReactNode; onMenu?: (at: MenuAt, el: HTMLElement, fold: { folded: boolean; toggle: () => void }) => void }) {
  const mutes = useStore((st) => st.mutes);
  const [folded, toggle] = useFolded(foldKey, sessions.every((s) => isQuiet(s, mutes)) && sessions.length > 3, { holds: holdsRow(sessions, activeKey) });
  return (
    <div>
      <div className="group/sec relative flex items-center gap-1 pr-2 pl-2 [@media(hover:none)]:pr-11" {...(onMenu ? contextMenuProps((at, el) => onMenu(at, el, { folded, toggle })) : {})}>
        <button type="button" aria-expanded={!folded} onClick={toggle} className="relative z-10 flex min-h-7 min-w-0 flex-1 items-center gap-1.5 whitespace-nowrap text-left text-[12px] text-ink-2 hover:text-ink [@media(pointer:coarse)]:min-h-11">
          <Icon name="chevron" size={9} stroke={2.4} className="shrink-0 text-ink-3 transition-transform" style={{ ...sidebarFoldMotion(!folded), transform: folded ? "rotate(-90deg)" : "none" }} />
          {state ? <StatusMark state={state} size="sm" /> : <span className="size-1.5 shrink-0" />}
          {title}
        </button>
        {right(folded)}
      </div>
      <Collapse open={!folded} sidebarFold>
        <div className="flex flex-col gap-px pl-3" inert={folded || undefined}>{children}</div>
      </Collapse>
    </div>
  );
}

function WorkspaceBlock({ sp, ...p }: { sp: HerdrSpace } & TreeProps) {
  const tabs = sp.tabs.length;
  return (
    <TreeBlock
      foldKey={sp.foldKey}
      sessions={sp.tree}
      activeKey={p.activeKey}
      state={sp.state}
      onMenu={(at, el, fold) => p.onSpaceMenu(sp, el, at, fold)}
      title={<><span className="min-w-0 truncate font-medium">{sp.name}</span><SpaceMarks pinned={sp.pinned} muted={isSpaceMuted(sp, p.mutes)} /></>}
      right={(folded) => (
        <>
          <span className={cx("flex shrink-0 items-center gap-1", MENU_STEP_ASIDE)}>
            <SpaceTodoTag sp={sp} onNavigate={p.onNavigate} />
            {sp.children.length > 0 && <ChildSummary counts={sp.childStates} />}
            <span className={MENU_HIDES}>
              {folded ? <SessionTally sessions={sp.tree} folded /> : tabs > 0 && !sp.children.length && (
                // agents, as the machine and the quick filter count them: a tab with several agents is one row (#2320)
                <span title={t("sidebar.space_tally", { count: sp.sessions.length, tabs })} className="text-[10.5px] text-ink-3 tabular-nums">{sp.sessions.length}<span className="sr-only"> {t("sidebar.agent_suffix", { count: sp.sessions.length })}</span></span>
              )}
            </span>
          </span>
          <SpaceMenuButton sp={sp} onMenu={p.onSpaceMenu} />
        </>
      )}
    >
      {sp.tabs.map((t) => <TabRow key={t.key} t={t} {...p} />)}
      {sp.loose.map((s) => <TreeSessionRow key={s.session_id} s={s} {...p} />)}
      {sp.children.length > 0 && <ChildSpaces sp={sp} {...p} />}
    </TreeBlock>
  );
}

// A space's child spaces (Orca's "N child spaces"), folded at first like Orca's sidebar: the parent's row already sums up
// what they are doing (ChildSummary), and the fold is kept by machine/space name like the space's own.
function ChildSpaces({ sp, ...p }: { sp: HerdrSpace } & TreeProps) {
  // i18n-ignore: a stored fold key, not interface text
  const [folded, toggle] = useFolded(`${sp.foldKey}/子项`, true, { holds: holdsRow(sp.tree.slice(sp.sessions.length), p.activeKey) });
  const n = sp.children.length;
  return (
    <div>
      <button type="button" aria-expanded={!folded} onClick={toggle} className="relative z-10 flex min-h-7 w-full min-w-0 items-center gap-1.5 pr-2 pl-2 text-left text-[11.5px] whitespace-nowrap text-ink-3 hover:text-ink [@media(pointer:coarse)]:min-h-11">
        <Icon name="chevron" size={9} stroke={2.4} className="shrink-0 transition-transform" style={{ ...sidebarFoldMotion(!folded), transform: folded ? "rotate(-90deg)" : "none" }} />
        <span className="min-w-0 flex-1 truncate">{t("sidebar.child_count", { count: n })}</span>
        {folded && <SessionTally sessions={sp.tree.slice(sp.sessions.length)} folded />}
      </button>
      <Collapse open={!folded} sidebarFold>
        <div className="flex flex-col gap-px" inert={folded || undefined}>
          {sp.children.map((c) => <WorkspaceBlock key={c.key} sp={c} {...p} />)}
        </div>
      </Collapse>
    </div>
  );
}

// What a parent space's children are doing, as Orca's parent row shows it: how many need you, work, are done.
function ChildSummary({ counts }: { counts: ChildStates }) {
  const parts = (["needs_you", "working", "done"] as const).filter((k) => counts[k] > 0);
  if (!parts.length) return null;
  return (
    <span title={t("sidebar.children_title", { list: parts.map((k) => stateCount(k, counts[k])).join(t("sidebar.list_separator")) })} className="flex items-center gap-1.5 text-[10.5px] text-ink-3 tabular-nums">
      {parts.map((k) => (
        <span key={k} className="flex items-center gap-0.5">
          <span aria-hidden className="flex"><StatusMark state={k === "done" ? "unread" : k} size="sm" /></span>
          {counts[k]}
          <span className="sr-only"> {t(`sidebar.child_sr_${k}`, { count: counts[k] })}</span>
        </span>
      ))}
    </span>
  );
}

function TreeSessionRow({ s, reg, hover, activeKey, go, onRowMenu }: { s: Session } & TreeProps) {
  const key = rowKey(s);
  return <SessionRow s={s} k={key} reg={reg} hover={hover} active={activeKey === key} to={{ name: "session", mid: s.machine_id, sid: s.session_id }} go={go} showTodoTag onMenu={onRowMenu} />;
}

/** The menu node of a tab of the sidebar's tree: its most urgent pane stands for it. */
function tabNode(t: HerdrTab, machines: Machine[]): MenuNode {
  const s = t.lead;
  return {
    kind: "tab",
    mid: s.machine_id,
    online: machines.find((m) => m.id === s.machine_id)?.online !== false,
    host: s.space_kind,
    label: s.slot_name,
    workspaceId: t.spaceId,
    tabId: t.slotId,
    paneId: s.pane_id,
    tabSessionId: s.session_id,
    cwd: s.cwd,
  };
}

// One herdr tab: `#N` (or its name) and what its most urgent pane is doing; it opens on that pane. A lead's workers are not
// among its panes (the hub folds them under the lead, lib/team.ts): they are in the lead's Team panel. A tab with one pane has that agent's ⋯, one with several the tab's own. A tab with several
// agents (#2320) opens up to them, in pane order: the "Lead" (the first pane, the others split from it) on top, its
// branches under it; the choice is kept per tab like the sidebar's other folds, and folded it is the one row it always was.
function TabRow({ t: tab, ...p }: { t: HerdrTab } & TreeProps) {
  const { reg, hover, activeKey, go, onRowMenu, onNodeMenu, machines } = p;
  const n = tab.sessions.length;
  const single = n === 1;
  // open at first: the point is to see which agents belong together (#2320); a tab folded by hand stays folded
  const [folded, toggle] = useFolded(tab.foldKey, false);
  const shut = single || folded;
  const open = tab.sessions.find((x) => rowKey(x) === activeKey);
  // opened up, the open agent's own row is the one lit; folded, the tab's row is, and the hidden pane rows (still
  // mounted in the collapse) register under no key, so the highlight and the scroll to the open row find the tab's
  const keys = tabRowKeys(`t:${tab.key}`, tab.sessions.map(rowKey), activeKey, shut);
  const k = keys.row;
  const to: Route = { name: "tab", mid: tab.lead.machine_id, space: tab.spaceId, slot: tab.slotId };
  // a tab with several panes has a menu of its own: rename, split, git, close it. One function per tab (a new one each
  // render would re-render the lead row on every push, SessionRow's memo); a click reads the tab as it is now.
  const latest = useRef({ tab, machines });
  useLayoutEffect(() => {
    latest.current = { tab, machines };
  });
  const onTabMenu = useCallback<RowMenuFn>(
    (_s, anchor, at) => {
      const { tab: now, machines: ms } = latest.current;
      onNodeMenu(tabNode(now, ms), at ?? { anchor }, t("sidebar.tab_named", { label: now.label || "" }).trim(), !at);
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps -- the tab is read from `latest`; its key names the tab
    [onNodeMenu, tab.key],
  );
  const row = (
    <SessionRow
      s={tab.lead}
      k={k}
      reg={reg}
      hover={hover}
      active={Boolean(open) && shut}
      to={to}
      go={go}
      // a long tab name gives way: it would run under the "N agents" button (endRoom) and hide the agent's title;
      // squeezed, it shrinks (truncated) before the title goes under its minimum (SessionRow)
      prefix={tab.label}
      prefixClass="min-w-0 max-w-[50%] shrink truncate text-[11.5px] text-ink-3 tabular-nums"
      showTodoTag={single}
      onMenu={single ? onRowMenu : onTabMenu}
      menuLabel={single ? undefined : t("sidebar.tab_actions")}
      endRoom={!single}
    />
  );
  if (single) return row;
  return (
    <div>
      <div className="relative">
        {row}
        <button
          type="button"
          aria-expanded={!folded}
          aria-label={tab.label ? t("sidebar.tab_agents_named", { label: tab.label, count: n }) : t("sidebar.agent_count", { count: n })}
          title={folded ? t("sidebar.tab_expand") : t("sidebar.show_less")}
          onClick={toggle}
          // left of the tab's own ⋯ (24 px, 44 on touch), which sits at the row's end
          className="touch-hit absolute top-1/2 right-8 z-20 flex h-6 -translate-y-1/2 items-center gap-0.5 whitespace-nowrap rounded-[6px] pr-0.5 pl-1 text-[11px] text-ink-3 tabular-nums hover:bg-hover hover:text-ink-2 [@media(hover:none)]:right-11"
        >
          {t("sidebar.agent_count", { count: n })}
          <Icon name="chevron" size={10} stroke={2.4} className="shrink-0 transition-transform" style={{ ...sidebarFoldMotion(!folded), transform: folded ? "rotate(-90deg)" : "none" }} />
        </button>
      </div>
      <Collapse open={!folded} sidebarFold>
        <div className="flex flex-col gap-px pl-3" inert={folded || undefined}>
          {tab.sessions.map((s, i) => {
            const key = rowKey(s);
            const branch = Boolean(tab.launcher) && s !== tab.launcher;
            const paneKey = keys.panes[i];
            const pane = (
              <SessionRow
                key={s.session_id}
                s={s}
                k={paneKey ?? key}
                reg={paneKey ? reg : unregistered}
                hover={hover}
                active={activeKey === key}
                to={paneRoute(s)}
                go={go}
                prefix={agentLabel(s)}
                prefixClass="min-w-0 shrink truncate text-[11px] text-ink-3"
                host={s === tab.launcher ? t("sidebar.launcher") : undefined}
                showTodoTag
                onMenu={onRowMenu}
              />
            );
            // the branches hang off the "Lead" on a thin line
            return branch ? <div key={s.session_id} className="ml-[13px] border-l border-line pl-1">{pane}</div> : pane;
          })}
        </div>
      </Collapse>
    </div>
  );
}

// "Terminal" under a machine (Settings › Appearance › Show terminals in sidebar): its terminal host's workspaces and the plain shells in them, each
// opening the "Terminal" page on that pane. Folds are kept by machine/workspace name like the Agents' above.
function ShellTree({ mid, online, spaces, reg, hover, go, activeKey, onNodeMenu }: { mid: string; online: boolean; spaces: TermSpace[] } & TreeProps) {
  const route = useRoute();
  const shown = route.name === "terminal" && route.mid === mid ? route.id : undefined;
  const sessions = useStore((s) => s.sessions[mid]);
  const count = spaces.reduce((n, sp) => n + sp.tabs.reduce((m, t) => m + t.panes.length, 0), 0);
  return (
    <TreeBlock foldKey={`term:${mid}`} sessions={[]} activeKey={activeKey} title={<span className="min-w-0 truncate font-medium">{t("sidebar.nav_terminal")}</span>} right={() => <GroupCount n={count} label={t("sidebar.pane_count", { count })} />}>
      {spaces.map((sp) => (
        <TreeBlock
          key={`${sp.host}/${sp.id}`}
          foldKey={`term:${mid}/${sp.host}:${sp.label}`}
          sessions={[]}
          activeKey={activeKey}
          title={<span className="min-w-0 truncate font-medium">{sp.label}</span>}
          right={() => null}
          onMenu={(at) => onNodeMenu(termSpaceNode(mid, sp, online), at, t("sidebar.space_named", { name: sp.label }))}
        >
          {sp.tabs.flatMap((tab) =>
            tab.panes.map((p) => {
              const k = `term:${mid}/${p.id}`;
              const node = tab.panes.length === 1 ? termTabNode(mid, sp, tab, online) : termPaneNode(mid, sp, tab, p, online);
              return (
                <NavRow
                  key={k}
                  k={k}
                  reg={reg}
                  hover={hover}
                  active={shown === p.id}
                  to={terminalTarget(mid, p.id, sessions ?? [])}
                  go={go}
                  icon="terminal"
                  label={tab.panes.length > 1 ? `${tab.label} · ${p.title}` : p.title}
                  title={p.detail ? `${p.title}\n${p.detail}` : p.title}
                  menuLabel={t("sidebar.terminal_actions")}
                  onMenu={(at, _el, button) => onNodeMenu(node, at, tab.label, button)}
                />
              );
            }),
          )}
        </TreeBlock>
      ))}
    </TreeBlock>
  );
}

// Agents outside any space (Codex sessions and the like): folds like a space, folded at first when it is long and
// nothing in it wants a look. The app lists them as a space of their machine.
export function LooseSection({ sessions, reg, hover, activeKey, go, onRowMenu }: { sessions: Session[]; reg: RegFn; hover: (k: string | null) => void; activeKey: string; go: GoFn; onRowMenu: RowMenuFn }) {
  const mutes = useStore((st) => st.mutes);
  const [folded, toggle] = useFolded("loose", sessions.every((s) => isQuiet(s, mutes)) && sessions.length > 3, { holds: holdsRow(sessions, activeKey) });
  return (
    <Section title={t("sidebar.other_agents")} folded={folded} onToggle={toggle} right={<SessionTally sessions={sessions} folded={folded} />}>
      {sessions.map((s) => (
        <SessionRow key={s.session_id} s={s} k={`s:${s.machine_id}/${s.session_id}`} reg={reg} hover={hover} active={activeKey === `s:${s.machine_id}/${s.session_id}`} to={{ name: "session", mid: s.machine_id, sid: s.session_id }} go={go} onMenu={onRowMenu} />
      ))}
    </Section>
  );
}

// A group's count; folded, the states that want a look instead (a mark and a count each, needs you first, #3559), so a
// folded group doesn't hide them.
export function SessionTally({ sessions, folded }: { sessions: Session[]; folded: boolean }) {
  const counts = foldedCounts(sessions);
  if (folded && hasFoldedCounts(counts)) return <StateTally counts={counts} />;
  return <GroupCount n={sessions.length} label={t("sidebar.agent_count", { count: sessions.length })} />;
}

/** For a row that is there but hidden (a folded tab's panes): it registers nowhere. */
const unregistered: RegFn = () => () => {};
