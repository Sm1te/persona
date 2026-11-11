import { memo, useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { api, enc, errorText } from "../lib/api";
import { sessionTitle, stateOf } from "../lib/format";
import { stateName } from "../lib/state-labels";
import { openNewAgent } from "../lib/new-agent";
import { openPanel } from "../lib/panel";
import { useAgentSessions } from "../lib/sessions";
import { onHubEvent, refreshTodos, useStore } from "../lib/store";
import { BoardModel, boundCard, columnCards, selectedColumn, type BoardCard, type BoardColumn, type BoardMutation, type BoardState, type Workspace } from "../lib/workspace-board";
import { autoFocusSession, sessionItem, todoItem, unboundSessions, workLanes, workspaceItem, type WorkItem, type WorkLane } from "../lib/workspace-board-auto";
import { makeSessionLookup } from "../lib/todos";
import { machinesByID, sessionsByCard, workspacesByCard } from "../lib/workspace-board-index";
import type { Machine, Session } from "../lib/types";
import { Button, cx, Dialog, IconButton, Loading, Menu, StatePill, toast, type MenuItem } from "../ui/atoms";
import { Icon } from "../ui/icons";
import { t } from "../i18n";
import { navigate } from "../lib/router";
import { useWorkspaceBoardSetting } from "../lib/workspace-board-setting";
const field = "w-full rounded-control bg-field px-3 py-2 text-sm text-ink outline-none focus:ring-2 focus:ring-accent";
const VIEW_KEY = "persona.workspace-board.view";
// The lanes that are an agent state take the one canonical state name; only To review and Idle are board-only words.
const laneTitle = (lane: WorkLane) => lane === "needs_you" || lane === "working" ? stateName(lane) : t(`workspaceboard.auto_lane_${lane}`);
type Edit = { kind: "column" | "card"; id?: string; title: string };

/** /workspace-board: the board only while it is turned on (#2475); off, nothing about it is loaded or polled. */
export function WorkspaceBoard({ onToggleSidebar }: { onToggleSidebar?: () => void }) {
  const { enabled } = useWorkspaceBoardSetting();
  if (enabled) return <Board onToggleSidebar={onToggleSidebar} />;
  return <div className="flex h-full flex-col bg-page">
    <header className="flex shrink-0 items-center gap-3 border-b border-line bg-surface px-4 py-3">
      {onToggleSidebar && <IconButton icon="sidebar" label={t("workspace-board.sidebar")} onClick={onToggleSidebar} className="lg:hidden" />}
      <h1 className="min-w-0 flex-1 truncate text-base font-semibold text-ink">{t("workspace-board.title")}</h1>
    </header>
    {enabled === null ? <div className="p-6"><Loading label={t("workspace-board.loading")} /></div> : <WorkspaceBoardOff />}
  </div>;
}

/** What /workspace-board says while the board is off: where to turn it on. */
export function WorkspaceBoardOff() {
  return <div className="flex flex-1 flex-col items-center justify-center gap-3 p-6 text-center">
    <Icon name="layers" size={28} className="text-ink-3" />
    <h2 className="text-base font-semibold text-ink">{t("workspace-board.off_title")}</h2>
    <p className="max-w-sm text-sm text-ink-3">{t("workspace-board.off_body")}</p>
    <Button tone="primary" icon="gear" className="whitespace-nowrap" onClick={() => navigate("/settings?tab=hub")}>{t("workspace-board.open_settings")}</Button>
  </div>;
}

function Board({ onToggleSidebar }: { onToggleSidebar?: () => void }) {
  const [model] = useState(() => new BoardModel());
  const state = useSyncExternalStore(model.subscribe, model.snapshot, model.snapshot);
  const machines = useStore((s) => s.machines), sessions = useAgentSessions();
  const [search, setSearch] = useState(""), [machine, setMachine] = useState("");
  const [archived, setArchived] = useState(false), [selected, setSelected] = useState<string>(), [adding, setAdding] = useState<string>();
  const [edit, setEdit] = useState<Edit | null>(null), [deleting, setDeleting] = useState<BoardColumn | null>(null), [moveTo, setMoveTo] = useState("");
  const [menu, setMenu] = useState<{ anchor: HTMLElement; card?: BoardCard; column?: BoardColumn } | null>(null);
  const dragging = useRef<string | null>(null);
  const refreshing = useRef(false);
  const [drop, setDrop] = useState("");
  const [columnSelection, setColumnSelection] = useState("");
  // Manual is the default and unchanged; the auto view is a per-browser choice (#2648).
  const [view, setView] = useState<"manual" | "auto">(() => (localStorage.getItem(VIEW_KEY) === "auto" ? "auto" : "manual"));
  const changeView = (v: "manual" | "auto") => { localStorage.setItem(VIEW_KEY, v); setView(v); setColumnSelection(""); };
  useEffect(() => {
    const refresh = () => {
      if (document.visibilityState === "hidden" || refreshing.current) return;
      refreshing.current = true;
      void model.refresh().finally(() => { refreshing.current = false; });
    };
    refresh(); void refreshTodos(); const timer = setInterval(refresh, 10_000);
    document.addEventListener("visibilitychange", refresh);
    const off = onHubEvent("workspace_board", (e) => { if (typeof e.revision !== "number" || e.revision > (model.snapshot().board?.revision ?? -1)) refresh(); });
    return () => { clearInterval(timer); document.removeEventListener("visibilitychange", refresh); off(); };
  }, [model]);
  const board = state.board, disabled = state.busy || state.readonly;
  const activeColumn = board ? selectedColumn(board, columnSelection) : "";
  const card = board?.cards.find((c) => c.id === selected);
  const names = useMemo(() => new Map(machines.map((m) => [m.id, m.name || m.id])), [machines]);
  // Built once per change of what they index; every card then looks itself up instead of scanning all of them (#2294).
  const machineOf = useMemo(() => machinesByID(machines), [machines]);
  const sessionsOf = useMemo(() => sessionsByCard(sessions), [sessions]);
  const workspaces = state.discovery.workspaces;
  const workspaceOf = useMemo(() => workspacesByCard(workspaces), [workspaces]);
  // Each column's cards once per filter change, for both the narrow-screen picker's counts and the columns.
  const shown = useMemo(() => new Map(board?.columns.map((c) => [c.id, columnCards(board, c.id, { search, machine, archived })])), [board, search, machine, archived]);
  const todos = useStore((s) => s.todos);
  const lookup = useMemo(() => (view === "auto" ? makeSessionLookup(sessions) : undefined), [view, sessions]);
  // The auto view filters exactly like the manual one (search, machine, archived), then groups every kind of work:
  // bound workspaces, agent sessions no card claims, and to-dos (#2648).
  const auto = useMemo(() => {
    if (view !== "auto" || !board) return [];
    const q = search.trim().toLocaleLowerCase();
    const match = (text: string) => !q || text.toLocaleLowerCase().includes(q);
    const filtered = board.columns.flatMap((c) => columnCards(board, c.id, { search, machine, archived }));
    const items: WorkItem[] = filtered.map((c) => workspaceItem(c, sessionsOf(c)));
    if (!archived) {
      items.push(...unboundSessions(sessions, filtered)
        .filter((s) => (!machine || s.machine_id === machine) && match(`${s.title} ${s.cwd} ${s.machine_id}`))
        .map(sessionItem));
      if (!machine) items.push(...todos.filter((todo) => match(`${todo.title} ${todo.notes ?? ""}`)).map((todo) => todoItem(todo, lookup)));
    }
    return workLanes(items);
  }, [view, board, search, machine, archived, sessionsOf, sessions, todos, lookup]);
  const activeLane = auto.some((l) => l.lane === columnSelection) ? columnSelection : "needs_you";
  const machineIDs = [...new Set([...machines.map((m) => m.id), ...(board?.cards.map((c) => c.machine_id) ?? [])])];
  const mutate = async (m: BoardMutation) => { const ok = await model.mutate(m); if (!ok && model.snapshot().error) toast(model.snapshot().error, "error"); return ok; };
  const dropCard = (e: React.DragEvent, column: string, before?: string) => {
    e.preventDefault(); e.stopPropagation(); const id = dragging.current || e.dataTransfer.getData("application/x-persona-workspace"); dragging.current = null; setDrop("");
    if (!disabled && id && id !== before && board?.cards.some((c) => c.id === id)) void mutate({ action: "move_card", id, column_id: column, before_id: before });
  };
  // Stable, so a memoized card renders again only when its own props change.
  const openCard = useCallback((c: BoardCard) => setSelected(c.id), []);
  const openCardMenu = useCallback((anchor: HTMLElement, c: BoardCard) => setMenu({ anchor, card: c }), []);
  const startDrag = useCallback((e: React.DragEvent, c: BoardCard) => { dragging.current = c.id; e.dataTransfer.effectAllowed = "move"; e.dataTransfer.setData("application/x-persona-workspace", c.id); }, []);
  const endDrag = useCallback(() => { dragging.current = null; setDrop(""); }, []);
  const cardMenu = (c: BoardCard): MenuItem[] => {
    const peers = columnCards(board!, c.column_id, { archived: c.archived }).filter((p) => p.pinned === c.pinned), i = peers.findIndex((p) => p.id === c.id);
    return [
      { key: "rename", label: t("workspaceboard.rename_card"), icon: "write", onSelect: () => setEdit({ kind: "card", id: c.id, title: c.title }) },
      { key: "pin", label: c.pinned ? t("workspaceboard.unpin") : t("workspaceboard.pin"), icon: "pin", onSelect: () => void mutate({ action: "update_card", id: c.id, pinned: !c.pinned }) },
      { key: "up", label: t("workspaceboard.move_up"), icon: "up", disabled: i <= 0, onSelect: () => void mutate({ action: "move_card", id: c.id, column_id: c.column_id, before_id: peers[i - 1]?.id }) },
      { key: "down", label: t("workspaceboard.move_down"), icon: "arrowDown", disabled: i >= peers.length - 1, onSelect: () => void mutate({ action: "move_card", id: c.id, column_id: c.column_id, before_id: peers[i + 2]?.id }) },
      ...board!.columns.filter((v) => v.id !== c.column_id).map((v) => ({ key: v.id, label: t("workspaceboard.move_to", { column: v.title }), icon: "layers" as const, onSelect: () => void mutate({ action: "move_card", id: c.id, column_id: v.id }) })),
      { key: "archive", label: c.archived ? t("workspaceboard.restore_card") : t("workspaceboard.archive_card"), icon: c.archived ? "undo" : "check", divider: true, onSelect: () => void mutate({ action: "update_card", id: c.id, archived: !c.archived }) },
    ];
  };
  const columnMenu = (c: BoardColumn): MenuItem[] => {
    const i = board!.columns.findIndex((v) => v.id === c.id);
    return [
      { key: "rename", label: t("workspaceboard.rename_column"), icon: "write", onSelect: () => setEdit({ kind: "column", id: c.id, title: c.title }) },
      { key: "left", label: t("workspaceboard.move_left"), icon: "chevronLeft", disabled: i === 0, onSelect: () => void mutate({ action: "move_column", id: c.id, before_id: board!.columns[i - 1]?.id }) },
      { key: "right", label: t("workspaceboard.move_right"), icon: "chevronRight", disabled: i === board!.columns.length - 1, onSelect: () => void mutate({ action: "move_column", id: c.id, before_id: board!.columns[i + 2]?.id }) },
      { key: "delete", label: t("workspaceboard.delete_column_menu"), icon: "x", danger: true, disabled: board!.columns.length <= 1, onSelect: () => { setDeleting(c); setMoveTo(""); } },
    ];
  };
  return <div className="@container flex h-full flex-col bg-page">
    <header className="flex shrink-0 items-center gap-3 border-b border-line bg-surface px-4 py-3">
      {onToggleSidebar && <IconButton icon="sidebar" label={t("agents-agentsview.sidebar")} onClick={onToggleSidebar} className="lg:hidden" />}
      <div className="min-w-0 flex-1"><h1 className="truncate text-base font-semibold text-ink">{t("workspaceboard.title")}</h1><p className="truncate text-xs text-ink-3">{t("workspaceboard.subtitle")}</p></div>
      <div className="flex shrink-0 items-center gap-1 rounded-control bg-field p-1" role="group" aria-label={t("workspaceboard.view_label")}>
        <Button tone={view === "manual" ? "primary" : "quiet"} className="!px-2 !py-1 text-xs" onClick={() => changeView("manual")}>{t("workspaceboard.view_manual")}</Button>
        <Button tone={view === "auto" ? "primary" : "quiet"} className="!px-2 !py-1 text-xs" onClick={() => changeView("auto")}>{t("workspaceboard.view_auto")}</Button>
      </div>
      {view === "manual" && <Button icon="plus" disabled={!board || disabled} onClick={() => setEdit({ kind: "column", title: "" })}>{t("workspaceboard.add_column")}</Button>}
    </header>
    <div className="flex flex-wrap items-center gap-2 border-b border-line bg-surface px-4 py-3">
      <label className="flex min-w-0 flex-1 items-center gap-2 rounded-control bg-field px-3 py-2"><Icon name="search" size={14} className="text-ink-3" /><input aria-label={t("workspaceboard.search_label")} placeholder={t("workspaceboard.search_placeholder")} className="min-w-0 flex-1 bg-transparent text-sm text-ink outline-none" value={search} onChange={(e) => setSearch(e.target.value)} /></label>
      <select aria-label={t("workspaceboard.filter_machine")} className="max-w-full rounded-control bg-field px-3 py-2 text-sm text-ink" value={machine} onChange={(e) => setMachine(e.target.value)}><option value="">{t("workspaceboard.all_machines")}</option>{machineIDs.map((id) => <option key={id} value={id}>{names.get(id) || id}</option>)}</select>
      <Button tone={archived ? "primary" : "quiet"} onClick={() => setArchived(!archived)}>{archived ? t("workspaceboard.back_to_board") : t("workspaceboard.archived")}</Button><IconButton icon="retry" label={t("workspaceboard.refresh")} onClick={() => void model.refresh()} disabled={state.busy} />
    </div>
    {board && view === "manual" && <div className="border-b border-line bg-surface px-4 pb-3 @3xl:hidden"><select aria-label={t("workspaceboard.pick_column")} className={field} value={activeColumn} onChange={(e) => setColumnSelection(e.target.value)}>{board.columns.map((c) => <option key={c.id} value={c.id}>{c.title} · {shown.get(c.id)?.length ?? 0}</option>)}</select></div>}
    {board && view === "auto" && <div className="border-b border-line bg-surface px-4 pb-3 @3xl:hidden"><select aria-label={t("workspaceboard.pick_column")} className={field} value={activeColumn} onChange={(e) => setColumnSelection(e.target.value)}>{auto.map((l) => <option key={l.lane} value={l.lane}>{laneTitle(l.lane)} · {l.items.length}</option>)}</select></div>}
    {view === "manual" ? <button type="button" onClick={() => changeView("auto")} className="border-b border-line bg-accent-tint px-4 py-2 text-left text-xs text-accent">{t("workspaceboard.auto_promo")}</button>
      : <p className="border-b border-line bg-inset px-4 py-2 text-xs text-ink-2">{t("workspaceboard.auto_hint")}</p>}
    {state.readonly && <p className="border-b border-line bg-inset px-4 py-2 text-xs text-ink-2">{t("workspaceboard.readonly")}</p>}
    {state.error && <p role="alert" className="border-b border-line bg-red-tint px-4 py-2 text-sm text-red">{state.error}</p>}
    {(state.discoveryError || state.discovery.errors?.length) ? <div role="status" className="border-b border-line bg-orange-tint px-4 py-2 text-xs text-orange">{state.discoveryError && <p>{t("workspaceboard.discovery_failed", { error: state.discoveryError })}</p>}{state.discovery.errors?.map((e, index) => <p key={`${e.machine_id}:${index}`}>{t("workspaceboard.machine_error", { machine: names.get(e.machine_id) || e.machine_id, error: e.error })}</p>)}<p>{t("workspaceboard.discovery_kept")}</p></div> : null}
    {state.loading && !board ? <div className="p-6"><Loading label={t("workspaceboard.loading")} /></div> : board && view === "auto" ? <AutoBoard lanes={auto} activeLane={activeLane} machineOf={machineOf} workspaceOf={workspaceOf} sessionsOf={sessionsOf} disabled={disabled} onOpen={openCard} onMenu={openCardMenu} onArchive={(c, archived) => void mutate({ action: "update_card", id: c.id, archived })} /> : board ? <div className="flex min-h-0 flex-1 gap-4 overflow-x-auto p-4" aria-label={archived ? t("workspaceboard.archived_label") : t("workspaceboard.columns_label")}>
      {board.columns.map((column) => { const cards = shown.get(column.id) ?? [];
        return <section key={column.id} className={cx("w-full shrink-0 flex-col rounded-card bg-canvas @3xl:flex @3xl:w-72 @3xl:min-w-64 @3xl:flex-1", column.id === activeColumn ? "flex" : "hidden")} aria-label={column.title}>
          <div className="flex items-center gap-2 px-3 py-3"><span className="size-2 rounded-full bg-accent" /><h2 className="min-w-0 flex-1 truncate text-sm font-semibold text-ink" title={column.title}>{column.title}</h2><span className="text-xs text-ink-3">{cards.length}</span><IconButton icon="more" label={t("workspaceboard.column_menu", { column: column.title })} disabled={disabled} onClick={(e) => setMenu({ anchor: e.currentTarget, column })} /></div>
          <div className="min-h-0 flex-1 space-y-3 overflow-y-auto px-3 pb-3">
            {cards.map((c) => <div key={c.id} className={cx("rounded-card", drop === c.id && "ring-2 ring-accent")} onDragOver={(e) => { if (!disabled && dragging.current) { e.preventDefault(); e.stopPropagation(); setDrop(c.id); } }} onDrop={(e) => dropCard(e, column.id, c.id)}>
              <WorkspaceCard card={c} machine={machineOf(c.machine_id)} workspace={workspaceOf(c)} sessions={sessionsOf(c)} disabled={disabled} onOpen={openCard} onMenu={openCardMenu} onDragStart={startDrag} onDragEnd={endDrag} />
            </div>)}
            <div className={cx("flex min-h-16 items-center justify-center rounded-control border border-dashed px-3 py-4 text-center text-xs text-ink-3", drop === column.id ? "border-accent bg-accent-tint" : "border-line-strong")} onDragOver={(e) => { if (!disabled && dragging.current) { e.preventDefault(); setDrop(column.id); } }} onDrop={(e) => dropCard(e, column.id)}>{cards.length ? <TouchHint text={t("workspaceboard.drop_here")} /> : search || machine ? t("workspaceboard.no_match") : <TouchHint text={t("workspaceboard.empty")} />}</div>
          </div>{!archived && <div className="border-t border-line px-3 py-2"><Button tone="quiet" icon="plus" className="w-full" disabled={disabled} onClick={() => setAdding(column.id)}>{t("workspaceboard.bind_workspace")}</Button></div>}
        </section>;
      })}
    </div> : <div className="p-6 text-sm text-ink-3">{t("workspaceboard.unavailable")}<Button onClick={() => void model.refresh()}>{t("workspaceboard.retry")}</Button></div>}
    {menu && <Menu anchor={menu.anchor} onClose={() => setMenu(null)} align="right" items={(menu.card ? cardMenu(menu.card) : columnMenu(menu.column!)).map((item) => ({ ...item, disabled: disabled || item.disabled }))} />}
    <Dialog open={Boolean(edit)} onClose={() => setEdit(null)} title={edit?.kind === "card" ? t("workspaceboard.rename_card") : edit?.id ? t("workspaceboard.rename_column") : t("workspaceboard.add_column")}>
      <form className="space-y-4" onSubmit={(e) => { e.preventDefault(); if (!edit?.title.trim()) return; void mutate({ action: edit.kind === "card" ? "update_card" : edit.id ? "update_column" : "add_column", id: edit.id, title: edit.title.trim() }).then((ok) => { if (ok) setEdit(null); }); }}><input autoFocus aria-label={t("workspaceboard.name")} maxLength={120} className={field} value={edit?.title ?? ""} onChange={(e) => setEdit((v) => v && { ...v, title: e.target.value })} /><div className="flex justify-end gap-2"><Button onClick={() => setEdit(null)}>{t("common.cancel")}</Button><Button type="submit" tone="primary" disabled={disabled || !edit?.title.trim()} busy={state.busy}>{t("common.save")}</Button></div></form>
    </Dialog>
    <Dialog open={Boolean(deleting)} onClose={() => setDeleting(null)} title={t("workspaceboard.delete_column_title", { column: deleting?.title ?? "" })}>
      <p className="mb-3 text-sm text-ink-2">{t("workspaceboard.delete_column_body")}</p>
      {board?.cards.some((c) => c.column_id === deleting?.id) && <select aria-label={t("workspaceboard.move_cards_to")} className={field} value={moveTo} onChange={(e) => setMoveTo(e.target.value)}><option value="">{t("workspaceboard.pick_target_column")}</option>{board.columns.filter((c) => c.id !== deleting?.id).map((c) => <option key={c.id} value={c.id}>{c.title}</option>)}</select>}
      <div className="mt-4 flex justify-end gap-2"><Button onClick={() => setDeleting(null)}>{t("common.cancel")}</Button><Button tone="danger" disabled={disabled || Boolean(board?.cards.some((c) => c.column_id === deleting?.id) && !moveTo)} busy={state.busy} onClick={() => void mutate({ action: "delete_column", id: deleting?.id, move_to: moveTo || undefined }).then((ok) => { if (ok) setDeleting(null); })}>{t("workspaceboard.delete_column")}</Button></div>
    </Dialog>
    {adding && board && <BindWorkspace column={adding} board={board} workspaces={state.discovery.workspaces} machines={machines} busy={state.busy} disabled={state.readonly} onClose={() => setAdding(undefined)} onBind={mutate} />}
    {card && <WorkspaceDetail card={card} column={board!.columns.find((c) => c.id === card.column_id)?.title ?? ""} machine={machineOf(card.machine_id)} sessions={sessionsOf(card)} readonly={state.readonly} onClose={() => setSelected(undefined)} />}
  </div>;
}

/** The auto view (#2648): lanes computed from each card's live agents, no dragging and no column editing. The manual
 *  columns keep their own state; archiving a card is still how a reviewed one leaves the board. */
function AutoBoard({ lanes, activeLane, machineOf, workspaceOf, sessionsOf, disabled, onOpen, onMenu, onArchive }: {
  lanes: { lane: WorkLane; items: WorkItem[] }[];
  activeLane: string;
  machineOf: (id: string) => Machine | undefined;
  workspaceOf: (card: BoardCard) => Workspace | undefined;
  sessionsOf: (card: BoardCard) => readonly Session[];
  disabled: boolean;
  onOpen: (card: BoardCard) => void;
  onMenu: (anchor: HTMLElement, card: BoardCard) => void;
  onArchive: (card: BoardCard, archived: boolean) => void;
}) {
  return <div className="flex min-h-0 flex-1 gap-4 overflow-x-auto p-4" aria-label={t("workspaceboard.auto_lanes_label")}>
    {lanes.map(({ lane, items }) => <section key={lane} className={cx("w-full shrink-0 flex-col rounded-card bg-canvas @3xl:flex @3xl:w-72 @3xl:min-w-64 @3xl:flex-1", lane === activeLane ? "flex" : "hidden")} aria-label={laneTitle(lane)}>
      <div className="flex items-center gap-2 px-3 py-3"><span className="size-2 rounded-full bg-accent" /><h2 className="min-w-0 flex-1 truncate text-sm font-semibold text-ink">{laneTitle(lane)}</h2><span className="text-xs text-ink-3">{items.length}</span></div>
      <div className="min-h-0 flex-1 space-y-3 overflow-y-auto px-3 pb-3">
        {items.map((item) => item.kind === "workspace" && item.card
          ? <AutoCard key={item.id} card={item.card} machine={machineOf(item.card.machine_id)} workspace={workspaceOf(item.card)} sessions={sessionsOf(item.card)} disabled={disabled} onOpen={onOpen} onMenu={onMenu} onArchive={onArchive} />
          : <WorkCard key={item.id} item={item} machine={item.session ? machineOf(item.session.machine_id) : undefined} />)}
        {items.length === 0 && <p className="rounded-control border border-dashed border-line-strong px-3 py-4 text-center text-xs text-ink-3">{t(`workspaceboard.auto_empty_${lane}`)}</p>}
      </div>
    </section>)}
  </div>;
}

/** An unbound agent session or a to-do in the auto view: it was never bound to a workspace, and its action opens it. */
function WorkCard({ item, machine }: { item: WorkItem; machine?: Machine }) {
  const s = item.session;
  return <article className="rounded-card bg-surface p-3 shadow-card" data-work-id={item.id}>
    <div className="flex items-start gap-2">
      <Icon name={item.kind === "todo" ? "check" : "agent"} size={14} className="mt-0.5 shrink-0 text-ink-3" />
      <div className="min-w-0 flex-1">
        <p className="truncate text-sm font-semibold text-ink" title={item.title}>{item.title}</p>
        <p className="mt-1 truncate text-xs text-ink-3">{item.kind === "todo" ? t("workspaceboard.work_todo") : `${machine?.name || s?.machine_id} · ${s?.cwd ?? ""}`}</p>
      </div>
      {s && <StatePill state={stateOf(s)} />}
    </div>
    <div className="mt-2">
      <Button tone="quiet" icon="chevronRight" className="!px-2 !py-1 text-xs" onClick={() => {
        if (s) navigate({ name: "session", mid: s.machine_id, sid: s.session_id });
        else navigate("/todos");
      }}>{item.kind === "todo" ? t("workspaceboard.work_open_todo") : t("workspaceboard.auto_open_session")}</Button>
    </div>
  </article>;
}

/** A card in the auto view: the manual card plus the actions its lane calls for, without opening the detail first. */
function AutoCard({ card, machine, workspace, sessions, disabled, onOpen, onMenu, onArchive }: {
  card: BoardCard; machine?: Machine; workspace?: Workspace; sessions: readonly Session[]; disabled: boolean;
  onOpen: (card: BoardCard) => void; onMenu: (anchor: HTMLElement, card: BoardCard) => void;
  onArchive: (card: BoardCard, archived: boolean) => void;
}) {
  const focus = autoFocusSession(sessions);
  const offline = machine?.online === false;
  return <div>
    <WorkspaceCard card={card} machine={machine} workspace={workspace} sessions={sessions} disabled={disabled} drag={false} onOpen={onOpen} onMenu={onMenu} />
    <div className="mt-1 flex flex-wrap gap-1">
      {focus ? <Button tone="quiet" icon="agent" className="!px-2 !py-1 text-xs" onClick={() => navigate({ name: "session", mid: focus.machine_id, sid: focus.session_id })}>{t("workspaceboard.auto_open_session")}</Button>
        : <Button tone="quiet" icon="plus" className="!px-2 !py-1 text-xs" disabled={disabled || offline} onClick={() => openNewAgent({ machineId: card.machine_id, cwd: card.cwd })}>{t("workspaceboard.new_agent")}</Button>}
      <Button tone="quiet" icon={card.archived ? "undo" : "check"} className="!px-2 !py-1 text-xs" disabled={disabled} onClick={() => onArchive(card, !card.archived)}>{card.archived ? t("workspaceboard.restore_card") : t("workspaceboard.archive_card")}</Button>
    </div>
  </div>;
}

/** Durable workflow binding; availability and Agent activity remain secondary facts. Memoized: the handlers get the card,
 *  so the board passes the same ones to every card and a search keystroke re-renders no card it keeps. */
export const WorkspaceCard = memo(function WorkspaceCard({ card, machine, workspace, sessions, disabled, drag = true, onOpen, onMenu, onDragStart, onDragEnd }: {
  card: BoardCard; machine?: Machine; workspace?: Workspace; sessions: readonly Session[]; disabled?: boolean; drag?: boolean;
  onOpen: (card: BoardCard) => void; onMenu: (anchor: HTMLElement, card: BoardCard) => void; onDragStart?: (e: React.DragEvent, card: BoardCard) => void; onDragEnd?: () => void;
}) {
  const availability = machine?.online === false || workspace?.online === false ? t("workspaceboard.machine_offline") : workspace?.available === false ? t("workspaceboard.space_unavailable") : !workspace ? t("workspaceboard.space_not_found") : "";
  return <article draggable={drag && !disabled} onDragStart={onDragStart && ((e) => onDragStart(e, card))} onDragEnd={onDragEnd} className="rounded-card bg-surface p-3 shadow-card" data-card-id={card.id}>
    <div className="flex items-start gap-2"><button type="button" className="min-w-0 flex-1 text-left" onClick={() => onOpen(card)}><span className="flex items-center gap-1.5">{card.pinned && <Icon name="pin" size={12} className="shrink-0 text-accent" />}<span className="truncate text-sm font-semibold text-ink" title={card.title}>{card.title}</span></span><span className="mt-2 block truncate font-mono text-xs text-ink-3" title={card.cwd}>{card.cwd}</span></button><IconButton icon="more" label={t("workspaceboard.card_menu", { card: card.title })} disabled={disabled} onClick={(e) => onMenu(e.currentTarget, card)} /></div>
    <div className="mt-3 flex flex-wrap items-center gap-2 text-xs text-ink-3"><span className="flex min-w-0 items-center gap-1"><Icon name="machine" size={12} />{machine?.name || card.machine_id}</span>{card.branch && <span className="flex min-w-0 items-center gap-1"><Icon name="git" size={12} /><span className="max-w-32 truncate">{card.branch}</span></span>}</div>
    <div className="mt-3 flex flex-wrap items-center gap-2"><span className="text-xs text-ink-2">{sessions.length ? t("workspaceboard.agent_count", { count: sessions.length }) : t("workspaceboard.no_agents")}</span>{availability && <span className="rounded-full bg-field px-2 py-1 text-xs text-ink-3">{availability}</span>}{sessions.some((s) => stateOf(s) === "needs_you") && <StatePill state="needs_you" />}{!sessions.some((s) => stateOf(s) === "needs_you") && sessions.some((s) => stateOf(s) === "working") && <StatePill state="working" />}</div>
  </article>;
});
function WorkspaceDetail({ card, column, machine, sessions, readonly, onClose }: { card: BoardCard; column: string; machine?: Machine; sessions: readonly Session[]; readonly: boolean; onClose: () => void }) {
  const open = (kind: "files" | "git") => { onClose(); openPanel({ kind, mid: card.machine_id, sid: sessions[0]?.session_id ?? "", cwd: card.cwd }); };
  return <Dialog open title={card.title} onClose={onClose}><div className="space-y-4"><p className="text-xs text-ink-3">{[column, card.archived && t("workspaceboard.archived"), machine?.name || card.machine_id, machine?.online === false && t("workspaceboard.offline")].filter(Boolean).join(" · ")}</p><p className="break-all rounded-control bg-inset p-3 font-mono text-xs text-ink-2">{card.cwd}</p>{card.branch && <p className="text-xs text-ink-2">{t("workspaceboard.branch", { branch: card.branch })}</p>}
    <div className="flex flex-wrap gap-2"><Button icon="folder" disabled={!machine?.online} onClick={() => open("files")}>{t("workspaceboard.files")}</Button><Button icon="git" disabled={!machine?.online} onClick={() => open("git")}>Git</Button><Button icon="plus" disabled={readonly || !machine?.online} onClick={() => { onClose(); openNewAgent({ machineId: card.machine_id, cwd: card.cwd }); }}>{t("workspaceboard.new_agent")}</Button></div>
    <div className="border-t border-line pt-3"><h3 className="mb-2 text-xs font-medium text-ink-3">{t("workspaceboard.agents_here")}</h3>{sessions.length ? <div className="space-y-1">{sessions.map((s) => <button type="button" key={s.session_id} className="flex w-full items-center gap-2 rounded-control px-2 py-2 text-left hover:bg-hover" onClick={() => { onClose(); navigate({ name: "session", mid: s.machine_id, sid: s.session_id }); }}><Icon name="agent" size={14} className="text-ink-3" /><span className="min-w-0 flex-1 truncate text-sm text-ink">{sessionTitle(s)}</span><StatePill state={stateOf(s)} /></button>)}</div> : <p className="text-sm text-ink-3">{t("workspaceboard.no_agents_detail")}</p>}</div></div></Dialog>;
}
interface Dirs { path: string; parent?: string; entries: { name: string; path: string }[] }
function BindWorkspace({ column, board, workspaces, machines, busy, disabled, onClose, onBind }: {
  column: string; board: BoardState; workspaces: Workspace[]; machines: Machine[]; busy: boolean; disabled: boolean; onClose: () => void; onBind: (m: BoardMutation) => Promise<boolean>;
}) {
  const [mode, setMode] = useState<"existing" | "directory">("existing"), [mid, setMid] = useState(machines.find((m) => m.online)?.id ?? "");
  const [path, setPath] = useState(""), [browse, setBrowse] = useState(""), [dirs, setDirs] = useState<Dirs | null>(null);
  const [error, setError] = useState(""), [loading, setLoading] = useState(false), [query, setQuery] = useState("");
  useEffect(() => {
    if (mode !== "directory" || !mid) return;
    const controller = new AbortController(); setLoading(true); setError(""); setDirs(null);
    void api<Dirs>(`/machines/${enc(mid)}/dirs?path=${enc(browse)}`, { signal: controller.signal }).then((d) => { if (!controller.signal.aborted) setDirs(d); }).catch((e) => { if (!controller.signal.aborted) setError(errorText(e)); }).finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [mid, mode, browse]);
  const bind = async (w?: Workspace) => { if (await onBind({ action: "add_card", column_id: column, machine_id: w?.machine_id ?? mid, workspace_id: w?.id, cwd: w?.cwd ?? path.trim() })) onClose(); };
  return <Dialog open title={t("workspaceboard.bind_workspace")} onClose={onClose}>
    <div className="mb-4 flex gap-2"><Button tone={mode === "existing" ? "primary" : "quiet"} onClick={() => setMode("existing")}>{t("workspaceboard.mode_existing")}</Button><Button tone={mode === "directory" ? "primary" : "quiet"} onClick={() => setMode("directory")}>{t("workspaceboard.mode_directory")}</Button></div>
    {mode === "existing" ? <div className="space-y-3"><input aria-label={t("workspaceboard.search_bindable")} className={field} value={query} onChange={(e) => setQuery(e.target.value)} placeholder={t("workspaceboard.search_label")} /><div className="max-h-80 space-y-2 overflow-y-auto">{workspaces.filter((w) => `${w.title} ${w.cwd}`.toLocaleLowerCase().includes(query.toLocaleLowerCase())).map((w) => {
      const bound = boundCard(board, w), online = machines.find((m) => m.id === w.machine_id)?.online !== false && w.online !== false;
      return <div key={w.id} className="flex items-center gap-2 rounded-control bg-inset p-3"><div className="min-w-0 flex-1"><p className="truncate text-sm text-ink">{w.title}</p><p className="truncate text-xs text-ink-3" title={w.cwd}>{machines.find((m) => m.id === w.machine_id)?.name || w.machine_id} · {w.cwd || t("workspaceboard.no_directory")}</p></div>{bound ? <Button disabled={disabled || busy || !bound.archived} onClick={() => void onBind({ action: "update_card", id: bound.id, archived: false }).then((ok) => { if (ok) onClose(); })}>{bound.archived ? t("workspaceboard.restore") : t("workspaceboard.bound")}</Button> : <Button disabled={disabled || busy || !w.cwd || !online || w.available === false} onClick={() => void bind(w)}>{online ? t("workspaceboard.bind") : t("workspaceboard.offline")}</Button>}</div>;
    })}{!workspaces.length && <p className="text-sm text-ink-3">{t("workspaceboard.none_discovered")}</p>}</div></div> : <form className="space-y-3" onSubmit={(e) => { e.preventDefault(); void bind(); }}>
      <select aria-label={t("workspaceboard.directory_machine")} className={field} value={mid} onChange={(e) => { setMid(e.target.value); setBrowse(""); setPath(""); }}><option value="">{t("workspaceboard.pick_online_machine")}</option>{machines.filter((m) => m.online).map((m) => <option key={m.id} value={m.id}>{m.name || m.id}</option>)}</select><input aria-label={t("workspaceboard.directory_label")} className={field} value={path} onChange={(e) => setPath(e.target.value)} placeholder={t("workspaceboard.directory_placeholder")} />
      {error && <p role="alert" className="text-xs text-red">{error}</p>}{loading && <Loading label={t("workspaceboard.reading_directory")} />}
      {dirs && <div className="max-h-64 overflow-y-auto rounded-control border border-line"><div className="flex items-center gap-2 border-b border-line p-2"><IconButton icon="chevronLeft" label={t("workspaceboard.parent_directory")} disabled={dirs.parent === undefined} onClick={() => setBrowse(dirs.parent!)} /><button type="button" className="min-w-0 flex-1 truncate text-left font-mono text-xs text-ink-2" onClick={() => setPath(dirs.path)} title={dirs.path}>{dirs.path}</button><Button tone="quiet" onClick={() => setPath(dirs.path)}>{t("workspaceboard.choose_this")}</Button></div>{dirs.entries.map((d) => <div key={d.path} className="flex items-center gap-2 px-3 py-1"><button type="button" className="flex min-w-0 flex-1 items-center gap-2 py-1 text-left text-sm text-ink" onClick={() => setBrowse(d.path)}><Icon name="folder" size={14} className="text-ink-3" /><span className="truncate">{d.name}</span></button><Button tone="quiet" onClick={() => setPath(d.path)}>{t("workspaceboard.choose")}</Button></div>)}</div>}
      <p className="text-xs text-ink-3">{t("workspaceboard.bind_directory_hint")}</p><div className="flex justify-end"><Button type="submit" tone="primary" disabled={disabled || busy || !mid || !path.trim()} busy={busy}>{t("workspaceboard.bind_directory")}</Button></div>
    </form>}
  </Dialog>;
}

// A finger can't drag a card: on a touch screen the drop zone says how to move one here instead.
function TouchHint({ text }: { text: string }) {
  return <><span className="pointer-coarse:hidden">{text}</span><span className="hidden pointer-coarse:inline">{t("workspaceboard.touch_hint")}</span></>;
}
