// One browser for work sessions and plain terminals (#4754): a switch at the list's top between the sessions and
// **Terminals**, every plain terminal of every computer at full height (lib/terminal-list.ts). The session detail still
// uses the app's /s and /t routes; a terminal opens on the right by its `pane:` address.
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { api, enc, errorText } from "../../lib/api";
import { isStoppedAgent } from "../../lib/format";
import { useScopedMachines } from "../../lib/current-assistant";
import { href, navigate, onLinkClick, type Route } from "../../lib/router";
import { SESSIONS_LIST_FEATURE } from "../../lib/session-list";
import { openPanel } from "../../lib/panel";
import { getSession, refresh as refreshSessions, useStore } from "../../lib/store";
import { usePanes, type Listing } from "../../lib/terminal-panes";
import { buildTerminalTree, keepOnlyShown, keepRecent, paneCwd, paneTrail, sameTerminal, type OpenTerminal, type TermSpace } from "../../lib/terminals";
import type { Machine } from "../../lib/types";
import { NARROW, useMedia } from "../../lib/viewport";
import { finishedCount, readListView, rememberListView, resolveListView, type TerminalListView } from "../../lib/terminal-list";
import { Button, Collapse, cx, IconButton, toast } from "../../ui/atoms";
import { contextMenuProps } from "../../ui/context-menu";
import type { MenuAt } from "../../ui/menu-model";
import { machineNode, termPaneNode, termSpaceNode, termTabNode, type MenuNode } from "../../lib/node-actions";
import { useFolded } from "../../lib/use-folded";
import { NodeMenu } from "../NodeMenu";
import { onNodeChanged, setKnownTree } from "../../lib/node-palette";
import { Icon } from "../../ui/icons";
import { WorkSessionBrowser } from "../agents/WorkSessionBrowser";
import { AgentsList } from "../Sidebar";
import { Segmented } from "../assistant/parts";
import { Empty } from "../session/files/parts";
import { TerminalPanel } from "../session/TerminalPanel";
import { ShellHeaderSlots } from "../ShellSwitch";
import { useCloseFinished } from "./CloseFinished";
import { interactiveSessionPane, standaloneShells, type ShellSpace, type ShellTab } from "./work-terminal-layout";
import { t } from "../../i18n";

type TerminalRoute = Extract<Route, { name: "terminal" }>;

export function TerminalPage({ route, onToggleSidebar, onNewAgent, detail, onWorkPane }: {
  route?: TerminalRoute;
  onToggleSidebar: () => void;
  onNewAgent: () => void;
  detail?: { selected?: { mid: string; sid: string }; render: (terminalAvailable: boolean) => ReactNode };
  onWorkPane?: (pane?: { mid: string; sid: string }) => void;
}) {
  const machines = useScopedMachines();
  const allMachines = useStore((s) => s.machines);
  const sessions = useStore((s) => s.sessions);
  const panesOn = useStore((s) => s.features.includes("terminal_panes"));
  const legacyList = useStore((s) => s.loaded && !s.features.includes(SESSIONS_LIST_FEATURE) && !s.features.includes("sessions_dimensions"));
  const addressed = route?.mid ?? detail?.selected?.mid;
  // A bookmarked terminal or session may name another assistant's computer. Only the current one's are listed.
  const targets = addressed && !machines.some((m) => m.id === addressed) ? allMachines.filter((m) => machines.includes(m) || m.id === addressed) : machines;
  const { listing, busy, refresh, reload } = usePanes(targets, panesOn);
  const [menu, setMenu] = useState<{ node: MenuNode; at: MenuAt; header?: string } | null>(null);
  const shown: OpenTerminal | undefined = route?.mid && route.id ? { mid: route.mid, id: route.id } : undefined;
  const selected = detail?.selected;
  // a stopped Agent (#4754) has its terminal too: the shell it left, or Resume when that is gone
  const stoppedSelected = useStore(() => Boolean(selected && isStoppedAgent(getSession(selected.mid, selected.sid))));
  const terminalAvailable = Boolean(panesOn && selected && targets.some((m) => m.id === selected.mid && m.online) &&
    (interactiveSessionPane(listing[selected.mid], selected.sid) || stoppedSelected));
  const mid = selected?.mid, sid = selected?.sid;
  useEffect(() => {
    onWorkPane?.(terminalAvailable && mid && sid ? { mid, sid } : undefined);
    return () => onWorkPane?.();
  }, [mid, sid, onWorkPane, terminalAvailable]);
  const hasDetail = Boolean(detail || shown);
  const narrow = useMedia(NARROW);
  const hideList = hasDetail && narrow;

  // Sessions | Terminals: the address's, else this browser's last choice; the sessions beside an open session
  const [remembered, setRemembered] = useState(readListView);
  const listView = resolveListView(route?.view, remembered, { panes: panesOn && machines.length > 0, session: Boolean(detail) });
  const browse: TerminalRoute = listView === "terminals" ? { name: "terminal", view: "terminals" } : { name: "terminal" };
  const chooseView = (view: TerminalListView) => {
    rememberListView(view);
    setRemembered(view);
    // an open terminal stays open beside the other list; from a session's page the list is the page's own
    if (detail) navigate({ name: "terminal", view });
    else navigate({ name: "terminal", ...(shown ? { mid: shown.mid, id: shown.id } : {}), view }, true);
  };

  const trees = useMemo(() => {
    const out: Record<string, TermSpace[]> = {};
    for (const m of targets) out[m.id] = buildTerminalTree(listing[m.id]?.data, sessions[m.id] ?? []);
    return out;
  }, [targets, listing, sessions]);

  // Node commands in the palette still see all panes, even when the browser shows only their associated sessions.
  useEffect(() => {
    if (!panesOn) return;
    for (const m of targets) setKnownTree(m.id, trees[m.id] ?? []);
  }, [panesOn, targets, trees]);
  useEffect(() => panesOn ? onNodeChanged(() => void refresh()) : undefined, [panesOn, refresh]);

  // Keep standalone terminals mounted as before; switching a session's content is handled by ChatView itself.
  const [open, setOpen] = useState<OpenTerminal[]>([]);
  const shownKey = shown ? `${shown.mid}\n${shown.id}` : "";
  useEffect(() => {
    if (shown) setOpen((list) => keepRecent(list, shown));
    // eslint-disable-next-line react-hooks/exhaustive-deps -- the key describes the open terminal
  }, [shownKey]);
  const current = useRef(shown);
  current.current = shown;
  useEffect(() => {
    const hide = () => document.visibilityState === "hidden" && setOpen((list) => keepOnlyShown(list, current.current));
    document.addEventListener("visibilitychange", hide);
    return () => document.removeEventListener("visibilitychange", hide);
  }, []);
  const opened = useRef(new Map<string, number>());
  const stable = [...open].sort((a, b) => {
    for (const item of [a, b]) if (!opened.current.has(`${item.mid}\n${item.id}`)) opened.current.set(`${item.mid}\n${item.id}`, opened.current.size);
    return opened.current.get(`${a.mid}\n${a.id}`)! - opened.current.get(`${b.mid}\n${b.id}`)!;
  });
  const close = (item: OpenTerminal) => {
    setOpen((list) => list.filter((x) => !sameTerminal(x, item)));
    if (sameTerminal(item, shown)) navigate(browse);
  };
  const openShell = async (mid: string, space: TermSpace) => {
    try {
      const r = await api<{ terminal: string }>(`/machines/${enc(mid)}/panes`, { method: "POST", json: { kind: space.host, workspace_id: space.id } });
      void reload([mid]);
      navigate({ name: "terminal", mid, id: r.terminal, view: "terminals" }); // "New terminal" is the Terminals list's
    } catch (e) {
      toast(t("terminal-terminalpage.new_failed", { error: errorText(e) }), "error");
    }
  };

  // "Close finished terminals" (#4754): previewed, confirmed, then exactly those (CloseFinished.tsx)
  const finished = useCloseFinished({ spacesOf: (id) => trees[id] ?? [], reload: (id) => void reload([id]) });
  const listsTerminals = listView === "terminals";

  return (
    <div className="flex h-full min-w-0 flex-col">
      <header className={cx("flex h-14 shrink-0 items-center gap-2 border-b border-line px-3 sm:px-4", hasDetail && "max-lg:hidden")}>
        <h1 className="hidden min-w-0 flex-1 truncate text-[14px] font-semibold text-ink lg:block">{t("work-entry.title")}</h1>
        <ShellHeaderSlots page="agents" onToggleSidebar={onToggleSidebar} endWide end={<IconButton icon="plus" label={t("agentspage.new_agent")} onClick={onNewAgent} />} />
      </header>
      {hasDetail && (
        <a href={href(browse)} onClick={(e) => onLinkClick(e, browse)} className="touch-hit flex h-14 shrink-0 items-center gap-2 border-b border-line px-3 text-[13px] text-accent-ink lg:hidden">
          <Icon name="arrowLeft" size={14} />{t(listsTerminals ? "terminal-terminalpage.back_terminals" : "work-entry.back")}
        </a>
      )}
      <div className="relative flex min-h-0 flex-1">
        <section aria-label={t(listsTerminals ? "terminal-terminalpage.terminals" : "work-entry.sessions")} inert={hideList || undefined} className={cx("flex min-h-0 w-full min-w-0 flex-col lg:w-[320px] lg:shrink-0 lg:border-r lg:border-line", hasDetail && "max-lg:invisible max-lg:absolute max-lg:inset-0")}>
          {panesOn && machines.length > 0 && (
            <div className="flex shrink-0 items-center gap-1 px-2 pt-2 pb-1">
              <div className="min-w-0 flex-1">
                <Segmented<TerminalListView>
                  label={t("terminal-terminalpage.view_label")}
                  value={listView}
                  options={[
                    { v: "sessions", label: t("work-entry.sessions") },
                    { v: "terminals", label: t("terminal-terminalpage.terminals") },
                  ]}
                  onChange={chooseView}
                />
              </div>
              {listsTerminals && <IconButton icon="retry" label={t("terminal-terminalpage.refresh")} onClick={() => void refresh()} disabled={busy} />}
            </div>
          )}
          {listsTerminals ? (
            <div data-terminals-list className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-2 pb-3">
              {!machines.some((m) => m.online) && <p className="px-2.5 py-2 text-[12px] text-ink-3">{t("terminal-terminalpage.none_online")}</p>}
              {machines.filter((m) => m.online).map((m) => (
                <MachineBlock
                  key={m.id}
                  machine={m}
                  named={machines.length > 1}
                  spaces={listing[m.id]?.error ? [] : standaloneShells(trees[m.id] ?? [])}
                  listing={listing[m.id]}
                  loading={busy}
                  shown={shown}
                  finished={listing[m.id]?.error ? 0 : finishedCount(trees[m.id] ?? [])}
                  closing={finished.previewing === m.id}
                  onCloseFinished={() => void finished.start(m.id)}
                  onOpenShell={(sp) => void openShell(m.id, sp)}
                  onRetry={() => void refresh()}
                  open={(node, at, header, toggle) => setMenu((prev) => (toggle && at.anchor && prev?.at.anchor === at.anchor ? null : { node, at, header }))}
                />
              ))}
            </div>
          ) : (
            <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-2 pb-3">
              {legacyList ? <AgentsList route={route ?? browse} onNewAgent={onNewAgent} /> : <WorkSessionBrowser
                activeSession={selected ? { machineId: selected.mid, sessionId: selected.sid } : undefined}
                onSelect={(s) => navigate({ name: "session", mid: s.machine_id, sid: s.session_id, work: true })}
              />}
            </div>
          )}
        </section>
        {menu && (
          <NodeMenu node={menu.node} at={menu.at} header={menu.header} onClose={() => setMenu(null)}
            changed={() => { void refresh(); void refreshSessions().catch(() => {}); }} />
        )}
        <section aria-label={t("work-entry.content")} className={cx("relative flex min-h-0 min-w-0 flex-1 flex-col", !hasDetail && "max-lg:hidden")}>
          {panesOn && stable.map((item) => {
            const on = sameTerminal(item, shown);
            const spaces = trees[item.mid] ?? [];
            const cwd = paneCwd(spaces, item.id);
            return (
              <div key={`${item.mid}\n${item.id}`} inert={!on || undefined} className={cx("absolute inset-0 flex flex-col", !on && "pointer-events-none invisible")}>
                <TerminalPanel mid={item.mid} sid={item.id} label={paneTrail(spaces, item.id) || undefined} cwd={cwd}
                  onFiles={() => openPanel({ kind: "files", mid: item.mid, sid: item.id.startsWith("pane:") ? "" : item.id, cwd })}
                  onClose={() => close(item)} />
              </div>
            );
          })}
          {detail ? detail.render(terminalAvailable)
            : shown ? !panesOn && <Empty icon="terminal" title={t("work-entry.terminal_unavailable")} />
            : listsTerminals ? <Empty icon="terminal" title={t("terminal-terminalpage.pick_terminal")} detail={t("terminal-terminalpage.pick_terminal_detail")} />
            : <Empty icon="chat" title={t("work-entry.pick_session")} detail={t("work-entry.pick_session_detail")} />}
        </section>
      </div>
      {finished.dialog}
    </div>
  );
}

/** A fold's chevron, in the room the row leaves on its left (no row moves for it): on hover, and on a touch screen; a
 *  folded row shows it. */
function FoldMark({ folded, className }: { folded: boolean; className: string }) {
  return (
    <Icon
      name="chevron"
      size={9}
      stroke={2.4}
      className={cx("pointer-events-none absolute text-ink-3 transition-[opacity,transform] duration-200 group-hover/fold:opacity-100 [@media(hover:none)]:opacity-100", folded ? "opacity-100" : "opacity-0", className)}
      style={{ transform: folded ? "rotate(-90deg)" : "none" }}
    />
  );
}

/** A row's ⋯ (the menu for a touch screen and for a mouse that has not right clicked): on hover, always on a touch screen. */
function MoreButton({ label, onOpen, className = "" }: { label: string; onOpen: (el: HTMLElement) => void; className?: string }) {
  return (
    <button
      type="button"
      aria-label={label}
      aria-haspopup="menu"
      title={t("terminal-terminalpage.more")}
      onClick={(e) => onOpen(e.currentTarget.parentElement ?? e.currentTarget)}
      className={cx(
        "touch-hit absolute top-1/2 right-1 z-10 hidden size-6 -translate-y-1/2 items-center justify-center rounded-[6px] bg-hover text-ink-3 hover:bg-hover-2 hover:text-ink focus-visible:flex group-hover/fold:flex [@media(hover:none)]:flex [@media(hover:none)]:bg-transparent",
        className,
      )}
    >
      <Icon name="more" size={14} />
    </button>
  );
}

interface Menus {
  /** `toggle`: from a ⋯, which closes its own menu again. */
  open: (node: MenuNode, at: MenuAt, header?: string, toggle?: boolean) => void;
}

function MachineBlock({
  machine,
  named,
  spaces,
  listing,
  loading,
  shown,
  finished,
  closing,
  onCloseFinished,
  onOpenShell,
  onRetry,
  open,
}: {
  machine: Machine;
  named: boolean;
  spaces: ShellSpace[];
  listing?: Listing;
  loading: boolean;
  shown?: OpenTerminal;
  /** How many of its terminals stopped Agents left (#4754): "Close finished terminals" shows when there are any. */
  finished: number;
  closing: boolean;
  onCloseFinished: () => void;
  onOpenShell: (space: TermSpace) => void;
  onRetry: () => void;
} & Menus) {
  const mid = machine.id;
  const [folded, toggle] = useFolded(`term:m:${mid}`);
  const hide = named && folded;
  const name = machine.name;
  const node = machineNode(machine, { fold: { folded, toggle }, onRefresh: onRetry });
  return (
    <div className="mt-2">
      {named && (
        <div className="group/fold relative flex h-7 items-center" {...contextMenuProps((at) => open(node, at, name))}>
          <button type="button" aria-expanded={!hide} onClick={toggle} className="flex h-7 min-w-0 flex-1 items-center gap-2 px-2.5 text-left">
            <Icon name="machine" size={12} className={machine.online ? "text-ink-2" : "text-ink-3"} />
            <span className={cx("min-w-0 flex-1 truncate text-[12.5px] font-semibold", machine.online ? "text-ink" : "text-ink-3")}>{machine.name}</span>
            {!machine.online && <span className="shrink-0 whitespace-nowrap text-[11px] text-ink-3">{t("terminal-terminalpage.offline")}</span>}
          </button>
          <FoldMark folded={hide} className="top-1/2 left-0.5 -translate-y-1/2" />
          <MoreButton label={t("terminal-terminalpage.actions_for", { name })} onOpen={(el) => open(node, { anchor: el }, name, true)} />
        </div>
      )}
      <Collapse open={!hide}>
        <div inert={hide || undefined}>
          {machine.online && !listing && loading && <p className="px-2.5 py-2 text-[12px] text-ink-3">{t("terminal-terminalpage.loading")}</p>}
          {machine.online && listing?.error && (
            <p className="flex items-center gap-2 px-2.5 py-2 text-[12px] text-red">
              <span className="min-w-0 flex-1 truncate" title={listing.error}>{listing.error}</span>
              <button type="button" onClick={onRetry} className="shrink-0 whitespace-nowrap rounded-control px-2 py-0.5 text-ink-2 hover:bg-hover hover:text-ink">
                {t("terminal-terminalpage.retry")}
              </button>
            </p>
          )}
          {machine.online && listing?.data && !listing.error && spaces.length === 0 && <p className="px-2.5 py-2 text-[12px] text-ink-3">{t("terminal-terminalpage.no_panes")}</p>}
          {finished > 0 && (
            <div className="flex px-1 pt-1">
              <Button tone="quiet" icon="x" busy={closing} onClick={onCloseFinished}>{t("terminal-terminalpage.close_finished")}</Button>
            </div>
          )}
          {spaces.map((sp) => (
            <SpaceBlock key={`${sp.host}/${sp.id}`} machine={machine} sp={sp} shown={shown} onOpenShell={onOpenShell} open={open} />
          ))}
        </div>
      </Collapse>
    </div>
  );
}

function SpaceBlock({ machine, sp, shown, onOpenShell, open }: { machine: Machine; sp: ShellSpace; shown?: OpenTerminal; onOpenShell: (space: TermSpace) => void } & Menus) {
  const mid = machine.id;
  const [folded, toggle] = useFolded(`term:w:${mid}/${sp.host}:${sp.label}`);
  const hide = folded;
  const node = termSpaceNode(mid, sp, machine.online, { fold: { folded, toggle }, onOpenShell: sp.canOpen ? () => onOpenShell(sp) : undefined });
  return (
    <div className="mt-1">
      <div className="group/fold relative flex h-7 items-center gap-1.5 pr-1 pl-2.5" {...contextMenuProps((at) => open(node, at, t("terminal-terminalpage.space_header", { name: sp.label })))}>
        <button type="button" aria-expanded={!hide} onClick={toggle} className="flex h-7 min-w-0 flex-1 items-center gap-1.5 text-left">
          <Icon name="layers" size={12} className="shrink-0 text-ink-3" />
          <span className="min-w-0 truncate text-[12px] font-medium text-ink-2">{sp.label}</span>
          {sp.branch && <span className="min-w-0 shrink truncate rounded-chip bg-field px-1.5 text-[10.5px] text-ink-3">{sp.branch}</span>}
          <span className="flex-1" />
        </button>
        <FoldMark folded={hide} className="top-1/2 left-0.5 -translate-y-1/2" />
        {sp.canOpen && <IconButton icon="plus" label={t("terminal-terminalpage.new_in_space", { name: sp.label })} size={24} iconSize={13} onClick={() => onOpenShell(sp)} />}
        <MoreButton label={t("terminal-terminalpage.actions_for", { name: sp.label })} onOpen={(el) => open(node, { anchor: el }, t("terminal-terminalpage.space_header", { name: sp.label }), true)} className="right-8" />
      </div>
      <Collapse open={!hide}>
        <div inert={hide || undefined}>
          {sp.tabs.map((t) => (
            <TabBlock key={t.id} machine={machine} sp={sp} t={t} shown={shown} open={open} />
          ))}
        </div>
      </Collapse>
    </div>
  );
}

function TabBlock({ machine, sp, t: tab, shown, open }: { machine: Machine; sp: ShellSpace; t: ShellTab; shown?: OpenTerminal } & Menus) {
  const mid = machine.id;
  const labelled = sp.tabs.length > 1 || tab.source.panes.length > 1; // the tab's name is shown (and folds) when it tells tabs or panes apart
  const [folded, toggle] = useFolded(`term:t:${mid}/${sp.host}:${sp.label}/${tab.label}`);
  const hide = labelled && folded;
  const tabNode = termTabNode(mid, sp, tab.source, machine.online, { fold: labelled ? { folded, toggle } : undefined });
  const tabHeader = t("terminal-terminalpage.tab_header", { name: tab.label });
  return (
    <div>
      {labelled && (
        <div className="group/fold relative" {...contextMenuProps((at) => open(tabNode, at, tabHeader))}>
          <button type="button" aria-expanded={!hide} onClick={toggle} className="block w-full truncate px-2.5 pt-1 pl-7 text-left text-[11px] text-ink-3">
            {tab.label}
          </button>
          <FoldMark folded={hide} className="top-[calc(50%+2px)] left-4 -translate-y-1/2" />
          {tab.source.panes.length > 1 && <MoreButton label={t("terminal-terminalpage.actions_for", { name: tab.label })} onOpen={(el) => open(tabNode, { anchor: el }, tabHeader, true)} />}
        </div>
      )}
      <Collapse open={!hide}>
        <div inert={hide || undefined}>
          {tab.panes.map((p) => {
            const to: Route = { name: "terminal", mid, id: p.id, view: "terminals" };
            const on = sameTerminal(shown, { mid, id: p.id });
            // a tab with one pane is that pane: its menu has the tab's rename and close too
            const node = tab.source.panes.length === 1 ? termTabNode(mid, sp, tab.source, machine.online) : termPaneNode(mid, sp, tab.source, p, machine.online);
            const header = tab.source.panes.length === 1 ? tabHeader : p.title;
            return (
              <div key={p.id} className="group/fold relative">
                <a
                  href={href(to)}
                  onClick={(e) => onLinkClick(e, to)}
                  aria-current={on ? "page" : undefined}
                  {...contextMenuProps((at) => open(node, at, header))}
                  className={cx("flex min-h-9 items-center gap-2.5 rounded-[7px] py-1 pr-2.5 pl-7 transition-colors", on ? "bg-hover" : "hover:bg-hover")}
                >
                  <span className="flex size-4 shrink-0 items-center justify-center text-ink-3">
                    <Icon name="terminal" size={13} />
                  </span>
                  <span className="min-w-0 flex-1">
                    <span className={cx("block truncate text-[12.5px]", on ? "font-medium text-ink" : "text-ink")}>{p.title}</span>
                    {p.detail && <span className="block truncate text-[11px] text-ink-3">{p.detail}</span>}
                  </span>
                </a>
                <MoreButton label={t("terminal-terminalpage.actions_for", { name: p.title })} onOpen={(el) => open(node, { anchor: el }, header, true)} />
              </div>
            );
          })}
        </div>
      </Collapse>
    </div>
  );
}
