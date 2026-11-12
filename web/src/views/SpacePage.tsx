// Space page (`/space/<machine>/<space name>`, docs/web/web.md "Space page", iOS SpaceView, Android #89): one space of one machine — its
// running agents (a row opens the chat), the todos it and its agents hang on, and what the space's ⋯ in the sidebar
// offers (mark all seen, rename, link to a todo, pin, mute, then "New agent" / Git / copy the path). Reached from a space's
// name on the machine page and from "Open space page" in a space's menu of the Agents list.
import { useEffect, useMemo, useRef, useState } from "react";
import { isSpaceMuted, unreadOf } from "../lib/agent-filter";
import { machineWithHub } from "../lib/mesh";
import { openNewAgent } from "../lib/new-agent";
import { isSpacePinned } from "../lib/pins";
import { href, navigate, onLinkClick, type Route } from "../lib/router";
import { spaceTodos, spaceView } from "../lib/space-page";
import { useStore } from "../lib/store";
import { foldRows } from "../lib/folded-rows";
import { makeSessionLookup, todoStatus, todoStatusLabel, TODO_STATUS_INFO } from "../lib/todos";
import type { Session } from "../lib/types";
import { shortPath } from "../lib/format";
import { Button, cx, EASE, IconButton, Loading } from "../ui/atoms";
import { Icon } from "../ui/icons";
import { MarkSeenDialog, SpaceMenu } from "./AgentMenus";
import { RenameSpaceDialog } from "./chat/SessionDialogs";
import { LinkTodoDialog, type LinkTargetSpace } from "./LinkTodoDialog";
import { AgentRow } from "./machines/AgentRow";
import { Empty } from "./session/files/parts";
import { t } from "../i18n";
import { markAllSeenLabel, stateCount } from "../lib/state-labels";

const NO_SESSIONS: Session[] = [];

const TONE: Record<(typeof TODO_STATUS_INFO)[keyof typeof TODO_STATUS_INFO]["tone"], string> = {
  red: "bg-red-tint text-red",
  green: "bg-green-tint text-green",
  amber: "bg-orange-tint text-orange",
  idle: "bg-field text-ink-2",
  off: "bg-field text-ink-3",
};

export function SpacePage({ mid, space, onToggleSidebar }: { mid: string; space: string; onToggleSidebar: () => void }) {
  const machines = useStore((s) => s.machines);
  const loaded = useStore((s) => s.loaded);
  // a child session folds into its parent (#5120): never a row of the space of its own
  const sessions = useStore((s) => foldRows(s.sessions[mid] ?? NO_SESSIONS));
  const pins = useStore((s) => s.pins);
  const mutes = useStore((s) => s.mutes);
  const todos = useStore((s) => s.todos);
  const todosLoaded = useStore((s) => s.todosLoaded);
  const todosSupported = useStore((s) => s.todosSupported);
  const spacesSupported = useStore((s) => s.spacesSupported);
  const m = machines.find((x) => x.id === mid);
  const online = m?.online !== false;
  const machineName = m ? machineWithHub(mid, machines) : mid;

  const view = useMemo(() => spaceView(sessions, mid, space), [sessions, mid, space]);
  const linked = useMemo(() => spaceTodos(todos, view.live, mid, space), [todos, view.live, mid, space]);
  const lookup = useMemo(() => makeSessionLookup(sessions), [sessions]);
  const sp = useMemo(() => ({ key: `${mid}/${space}`, machineId: mid, name: space, sessions: view.live.length ? view.live : view.recent }), [mid, space, view]);
  const pinned = isSpacePinned(mid, space, pins);
  const muted = isSpaceMuted(sp, mutes);

  // Renamed (here or on another device): the workspace this page showed has a new name, and the page follows it.
  const workspace = useRef<string | undefined>(undefined);
  if (view.first) workspace.current = view.first.workspace_id ?? view.first.space_id;
  const renamedTo = !view.first && workspace.current ? sessions.find((s) => (s.workspace_id ?? s.space_id) === workspace.current && s.space_id)?.space_name : undefined;
  useEffect(() => {
    if (renamedTo && renamedTo !== space) navigate({ name: "space", mid, space: renamedTo }, true);
  }, [renamedTo, space, mid]);

  const [menu, setMenu] = useState<HTMLElement | null>(null);
  const [renaming, setRenaming] = useState<Session | null>(null);
  const [confirmSeen, setConfirmSeen] = useState<Session[] | null>(null);
  const [linkTarget, setLinkTarget] = useState<LinkTargetSpace | undefined>();

  const machineRoute: Route = { name: "machine", mid };
  const header = (
    <header className="flex h-14 shrink-0 items-center gap-2 border-b border-line px-3 sm:px-4">
      <IconButton icon="sidebar" label={t("space-page.sidebar")} onClick={onToggleSidebar} className="lg:hidden" />
      <a
        href={href(machineRoute)}
        onClick={(e) => onLinkClick(e, machineRoute)}
        className="flex h-7 min-w-0 max-w-[40%] shrink-0 items-center gap-0.5 whitespace-nowrap rounded-[7px] pr-2 pl-1 text-[12.5px] text-ink-3 transition-colors hover:bg-hover hover:text-ink"
        title={t("space-page.back_to_machine", { name: machineName })}
      >
        <Icon name="chevronLeft" size={14} stroke={2.2} className="shrink-0" />
        <span className="truncate">{machineName}</span>
      </a>
      <h1 className="flex min-w-0 flex-1 items-center gap-1.5 text-[14px] font-semibold text-ink">
        <span className="truncate">{space}</span>
        {pinned && <span title={t("space-page.pinned")} className="shrink-0 text-accent"><Icon name="pin" size={12} stroke={2.2} /><span className="sr-only">{t("space-page.pinned")}</span></span>}
        {muted && <span title={t("space-page.muted")} className="shrink-0 text-ink-3"><Icon name="bellOff" size={12} stroke={2.2} /><span className="sr-only">{t("space-page.muted")}</span></span>}
      </h1>
      {view.first && <IconButton icon="more" label={t("space-page.actions")} onClick={(e) => setMenu((x) => (x ? null : e.currentTarget))} />}
    </header>
  );

  if (!view.first) {
    return (
      <div className="flex h-full flex-col">
        {header}
        {!loaded || renamedTo ? (
          <div className="flex flex-1 items-center justify-center"><Loading /></div>
        ) : (
          <div className="flex min-h-0 flex-1 flex-col pb-[10vh]">
            <Empty
              icon="layers"
              title={t("space-page.gone_title")}
              detail={t("space-page.gone_detail", { name: space, machine: machineName })}
              action={<Button tone="secondary" icon="machine" onClick={() => navigate(machineRoute)}>{t("space-page.back_to_machine_button")}</Button>}
            />
          </div>
        )}
      </div>
    );
  }

  const first = view.first;
  const supportSpaces = todosLoaded && todosSupported !== false && spacesSupported !== false;
  const spaceTodo = linked.find((x) => x.via === "space");
  const counts = [
    view.counts.needs && stateCount("needs_you", view.counts.needs),
    view.counts.working && stateCount("working", view.counts.working),
    view.counts.unread && stateCount("unread", view.counts.unread),
  ].filter(Boolean).join(" · ");
  const unread = unreadOf(view.live);

  return (
    <div className="flex h-full flex-col">
      {header}
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto flex max-w-[880px] flex-col gap-7 px-4 py-6 sm:px-8">
          {/* the space: where it is, how many agents, what they are doing */}
          <section className="flex items-start gap-3 rounded-card bg-surface px-4 py-3.5 shadow-card" style={{ animation: `fade-up 420ms ${EASE} both` }}>
            <span className="flex size-9 shrink-0 items-center justify-center rounded-control bg-accent-tint text-accent-ink">
              <Icon name="layers" size={16} />
            </span>
            <div className="min-w-0 flex-1">
              <div className="flex min-w-0 items-center gap-2">
                <span aria-hidden className={cx("size-1.5 shrink-0 rounded-full", online ? "bg-green" : "bg-line-strong")} />
                <span className="truncate text-[13px] font-medium text-ink">{machineName}</span>
                <span className="shrink-0 text-[12px] whitespace-nowrap text-ink-3">{t("space-page.summary_agents", { count: view.live.length })}</span>
              </div>
              {view.cwd && <p className="mt-0.5 truncate font-mono text-[11.5px] text-ink-3" title={view.cwd}>{shortPath(view.cwd)}</p>}
              {counts && <p className="mt-1 truncate text-[12px] text-ink-2">{counts}</p>}
              {!online && <p className="mt-1 text-[12px] text-ink-3">{t("space-page.machine_offline")}</p>}
            </div>
          </section>

          {/* the agents */}
          <section>
            <Heading
              title={t("space-page.agents")}
              count={view.live.length}
              right={
                <span className="flex items-center gap-1">
                  {unread.length > 0 && <Button tone="quiet" icon="check" onClick={() => setConfirmSeen(unread)}>{markAllSeenLabel()}</Button>}
                  {online && first.cwd && <Button tone="secondary" icon="plus" onClick={() => openNewAgent({ machineId: mid, cwd: first.cwd })}>{t("space-page.new_agent")}</Button>}
                </span>
              }
            />
            {view.live.length === 0 ? (
              <p className="rounded-card bg-surface px-4 py-3 text-[12.5px] text-ink-3 shadow-card">{online ? t("space-page.no_agents") : t("space-page.machine_offline")}</p>
            ) : (
              <div className="divide-y divide-line overflow-hidden rounded-card bg-surface shadow-card" style={{ animation: `fade-up 420ms ${EASE} 50ms both` }}>
                {view.live.map((s) => <AgentRow key={s.session_id} s={s} />)}
              </div>
            )}
          </section>

          {/* the todos it hangs on (docs/assistant/todos.md): the space's own, then its agents' */}
          {todosSupported !== false && (
            <section>
              <Heading
                title={t("space-page.todos")}
                count={linked.length || undefined}
                right={supportSpaces ? (
                  <Button tone="quiet" icon="todo" onClick={() => setLinkTarget({ machineId: mid, space })}>{spaceTodo ? t("space-page.relink_todo") : t("space-page.link_todo")}</Button>
                ) : undefined}
              />
              {linked.length === 0 ? (
                <p className="rounded-card bg-surface px-4 py-3 text-[12.5px] text-ink-3 shadow-card">{spacesSupported === false ? t("space-page.todos_unsupported") : t("space-page.no_todo")}</p>
              ) : (
                <div className="divide-y divide-line overflow-hidden rounded-card bg-surface shadow-card">
                  {linked.map((x) => {
                    const to: Route = { name: "todos", id: x.id };
                    const status = x.todo ? todoStatus(x.todo, lookup) : undefined;
                    return (
                      <a key={x.id} href={href(to)} onClick={(e) => onLinkClick(e, to)} className="group flex items-center gap-3 px-4 py-2.5 transition-colors hover:bg-hover">
                        <Icon name="todo" size={14} className="shrink-0 text-ink-3" />
                        <span className="min-w-0 flex-1 truncate text-[13px] font-medium text-ink">{x.title}</span>
                        <span className="shrink-0 text-[11.5px] whitespace-nowrap text-ink-3">{x.via === "space" ? t("space-page.todo_via_space") : t("space-page.todo_via_agent")}</span>
                        {status && x.todo && (
                          <span className={cx("inline-flex h-5 shrink-0 items-center whitespace-nowrap rounded-full px-1.5 text-[11px] font-medium", TONE[TODO_STATUS_INFO[status].tone])}>{todoStatusLabel(x.todo, status)}</span>
                        )}
                        <Icon name="chevronRight" size={13} stroke={2.2} className="shrink-0 text-ink-3 transition-transform duration-150 group-hover:translate-x-0.5" />
                      </a>
                    );
                  })}
                </div>
              )}
            </section>
          )}

          {/* agents of this space that are no longer running */}
          {view.recent.length > 0 && (
            <section>
              <Heading title={t("space-page.recent")} count={view.recent.length} />
              <div className="divide-y divide-line overflow-hidden rounded-card bg-surface shadow-card">
                {view.recent.map((s) => <AgentRow key={s.session_id} s={s} />)}
              </div>
            </section>
          )}
        </div>
      </div>

      {menu && (
        <SpaceMenu
          sp={sp}
          anchor={menu}
          openPage={false}
          onClose={() => setMenu(null)}
          onRename={setRenaming}
          onMarkSeen={setConfirmSeen}
          onLinkTodo={() => setLinkTarget({ machineId: mid, space })}
        />
      )}
      {renaming && <RenameSpaceDialog open session={renaming} onClose={() => setRenaming(null)} />}
      <MarkSeenDialog list={confirmSeen} onClose={() => setConfirmSeen(null)} />
      <LinkTodoDialog open={Boolean(linkTarget)} space={linkTarget} onClose={() => setLinkTarget(undefined)} />
    </div>
  );
}

function Heading({ title, count, right }: { title: string; count?: number; right?: React.ReactNode }) {
  return (
    <div className="mb-2.5 flex min-h-7 items-center gap-2 px-1">
      <h2 className="text-[13px] font-medium whitespace-nowrap text-ink">{title}</h2>
      {count !== undefined && <span className="text-[12px] text-ink-3 tabular-nums">{count}</span>}
      <span className="flex-1" />
      {right}
    </div>
  );
}

