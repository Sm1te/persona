import { removeStoredValue, storedValue, storeValue } from "../lib/compatibility";
// Beautiful UI's Sidebar Nav: workspace row, quick search, sections, one gliding highlight. Persona's main conversation
// is the first entry (the app opens on the assistant); a new conversation and a new agent are the "+" of their sections (#1239).
import { memo, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { t } from "../i18n";
import { NARROW, useMedia } from "../lib/viewport";
import { AGENT_FILTERS, agentFilter, applyFilter, arrangeSpaces, BOTMUX_HIDDEN_KEY, botmuxAndFilter, filterCounts, isBotmux, isSpaceMuted, matchingKeys, shownFilters, unreadOf, withoutBotmux, type AgentFilter, type FilterCounts } from "../lib/agent-filter";
import { applyDimensionFilters, buildAgentFacets, DEFAULT_DIMENSION_FILTERS, hasActiveDimensionFilters, type AgentDimensionFilters } from "../lib/agent-chatlist-filter";
import { AGENT_SORT_KEY, AGENT_SORTS, agentSort, byUpdated, machineTreeByUpdate, projectsByUpdate, repoMachinesByUpdate, todoTreeByUpdate, type AgentSort } from "../lib/agent-sort";
import { AgentChatListRow, AgentDimensionFilterBar } from "./agents/AgentChatListControls";
import { agentGrouping, AGENT_GROUPINGS, type AgentGrouping, buildAgentGroups, buildSpaces, hostsOf, isQuiet, sessionHost, splitAgentSessions, type AgentGroup, type Space } from "../lib/agent-groups";
import { buildRepoMachineTree } from "../lib/agent-repos";
import { useWorkspaceCatalog } from "../lib/workspace-catalog";
import { keyedRefs } from "../lib/keyed-refs";
import { buildMachineTree } from "../lib/herdr-tree";
import { selectSidebarMachines } from "../lib/sidebar-machines";
import { machineWithHub } from "../lib/mesh";
import { baseName, sessionTitle, tabLabel } from "../lib/format";
import { cardState } from "../lib/card-focus";
import { agentKey, isSpacePinned, pinnedSessions } from "../lib/pins";
import { firstAndCurrent } from "../lib/persona-nav";
import { countsLabel, directionLabel, totalCounts, useDirectionNames, useLeaderCounts } from "../lib/direction-names";
import { groupsByDirection, useDirectionTree } from "../lib/direction-tree";
import { buildDirectionAgentTree } from "../lib/direction-agent-tree";
import { ConversationRowMenu } from "./ConversationMenu";
import { conversationPage } from "../lib/conversation-page";
import { href, navigate, onLinkClick, showsSession, type Route } from "../lib/router";
import { useAgentSessions, useFullAgentSessions } from "../lib/sessions";
import { refresh, useStore } from "../lib/store";
import { conversationTitle } from "./assistant/conversations";
import { buildTodoTree, shareTree, type TodoTree } from "../lib/todo-tree";
import { openTodoFromTag, TODO_STATUS_INFO, type TodoStatus } from "../lib/todos";
import type { Machine, Session } from "../lib/types";
import { Collapse, cx, EASE, Kbd, Menu, sidebarFoldMotion, useMenu, type MenuItem } from "../ui/atoms";
import { StatusMark } from "../ui/StatusMark";
import { Icon, type IconName } from "../ui/icons";
import { HerdrNotices as HerdrNoticesContent } from "./agents/HerdrNotices";
import { HubNoticeLine as HubNoticeLineContent } from "./agents/HubNoticeRow";
import { ArchiveCloseDialog, CloseAgentDialog, DeleteSessionDialog, RenameDialog, RenameSpaceDialog } from "./chat/SessionDialogs";
import { AGENT_ARCHIVES_FEATURE } from "../lib/agent-archives";
import { AgentRowMenu, MarkSeenDialog as MarkSeenDialogContent, SpaceMenu } from "./AgentMenus";
import { NodeMenu } from "./NodeMenu";
import { NodePrompt as NodePromptContent } from "./NodePrompt";
import { SidebarUsage as SidebarUsageContent } from "./UsageBars";
import "../lib/node-palette";
import type { MenuNode } from "../lib/node-actions";
import type { MenuAt } from "../ui/menu-model";
import { useSidebarTerminals } from "../lib/sidebar-terminals";
import { usePanes } from "../lib/terminal-panes";
import { buildTerminalTree, shellsOnly, type TermSpace } from "../lib/terminals";
import { LinkTodoDialog as LinkTodoDialogContent, type LinkTargetSpace } from "./LinkTodoDialog";
import { FoldScope, useFolded } from "../lib/use-folded";
import { clipsOverflow, glideBox, glideStyle, sameBox, type GlideBox, type Span } from "../lib/glide";
import { AGENTS_FOLD, onGrouping, setAgentsFolded } from "../lib/sidebar-api";
import { jumpHint, useBindings } from "../lib/shortcuts";
import { markAllSeenLabel, stateCount } from "../lib/state-labels";
import { PagedSessionList } from "./agents/PagedSessionList";
import { SESSION_LIST_STYLE } from "../ui/list-metrics";
import { runtimeActiveAt, runtimeIdentity, runtimeNeedsYou, runtimeOrder, runtimeRemoval, runtimeSessions } from "../lib/runtime-list";
import { SESSIONS_LIST_FEATURE } from "../lib/session-list";
import { NoComputers, useAddComputer } from "./machines/AddComputerEntry";
import { BADGE_CLASS, DOT_CLASS, type GoFn, GroupCount, holdsRow, morePagesMenu, type NavPage, type NodeMenuFn, type RegFn, rowKey, type RowMenuFn, Section, SidebarGroup, GroupFoldButton, useActiveKey, useGo, useNavPages } from "./SidebarParts";
import { NavRow, PersonalAgents, PersonaRow, SessionRow, SubHeading } from "./SidebarRows";
import { LooseSection, MachineSection, RepoMachineSection, SessionTally, type SpaceMenuFn, type WorkspaceTreeActions, SpaceSection, TodoTreeView } from "./SidebarTree";
import { CollapseButton, PagesTally } from "./SidebarRail";
import { scoped, useScope, useScoped, useScopedInteractions, useScopedMachines, useScopedTodos } from "../lib/current-assistant";
import { AssistantSwitcher } from "./AssistantSwitcher";
import { sidebarPending } from "./sidebar-pending";
import { usePendingWorkSessions } from "./sidebar-pending-sessions";
// Declared in the files split out of this one; re-exported so the files that import them from here stay as they are.
export { useNavPages, type NavPage } from "./SidebarParts";
export { AgentRow, PersonaRow, SessionRow } from "./SidebarRows";
export { SidebarRail } from "./SidebarRail";

// Skip parent pushes with unchanged props; each leaf still receives its own state and subscription updates.
const HerdrNotices = memo(HerdrNoticesContent);
const HubNoticeLine = memo(HubNoticeLineContent);
const NodePrompt = memo(NodePromptContent);
const SidebarUsage = memo(SidebarUsageContent);
const LinkTodoDialog = memo(LinkTodoDialogContent);
const MarkSeenDialog = memo(MarkSeenDialogContent);

/** Brings `el` to the middle of its nearest scrolling ancestor when it is not fully in view (rows loading above it
 *  later still leave it on screen). Not `scrollIntoView`: that also scrolls the clipped (overflow-hidden) box of a
 *  Collapse that is still opening, and it stays scrolled. */
function reveal(el: HTMLElement): boolean {
  for (let p = el.parentElement; p; p = p.parentElement) {
    const o = getComputedStyle(p).overflowY;
    if (o !== "auto" && o !== "scroll") continue;
    const r = el.getBoundingClientRect(),
      b = p.getBoundingClientRect();
    // a row under the pinned Persona row (#5581) is not in view; that row itself always is
    const pinned = p.querySelector<HTMLElement>(":scope > [data-sidebar-sticky]");
    let top = pinned && !pinned.contains(el) ? pinned.getBoundingClientRect().bottom : b.top;
    for (let group = el.closest("[data-sidebar-group]"); group; group = group.parentElement?.closest("[data-sidebar-group]") ?? null) {
      const heading = group.querySelector<HTMLElement>(":scope > [data-sidebar-group-heading]");
      if (heading && !heading.contains(el)) top = Math.max(top, heading.getBoundingClientRect().bottom);
    }
    if (r.top >= top && r.bottom <= b.bottom) return false;
    p.scrollTop += r.top + r.height / 2 - (b.top + b.height / 2);
    return true;
  }
  return false;
}

/** Keep group headings below Persona at every text size, without doing layout work on scroll. */
function useStickyTop(ref: React.RefObject<HTMLElement | null>) {
  useLayoutEffect(() => {
    const nav = ref.current;
    const pinned = nav?.querySelector<HTMLElement>(":scope > [data-sidebar-sticky]");
    if (!nav || !pinned) return;
    const measure = () => nav.style.setProperty("--sidebar-sticky-top", `${pinned.getBoundingClientRect().height}px`);
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(pinned);
    return () => { observer.disconnect(); nav.style.removeProperty("--sidebar-sticky-top"); };
  }, [ref]);
}

/** The gliding highlight under a list of rows (Beautiful UI's Sidebar Nav): rows register under their keys, and the
 *  one under the pointer gets the box; the open row has its own fill (CURRENT_ROW), so the box doesn't take it away.
 *  The list is the box's scroll container (`nav`). The open row is kept in view from when it opens until the list is
 *  touched (wheel, pointer, keys): it may come later, and rows loading above it push it down.
 *  The box is measured after each render and again whenever the list's layout moves without one (#2476): a group
 *  folding or opening (its Collapse animates, then ends), rows arriving or changing height, scrolling. It covers only
 *  the part of its row on show and goes away when the row is folded away or gone (lib/glide.ts).
 *  The row under the pointer is kept in a ref and the box in a small store only GlideHighlight reads: hovering
 *  re-measures and moves the box without re-rendering the list that holds the rows. */
function useGlide(activeKey: string) {
  const nav = useRef<HTMLDivElement>(null);
  const rows = useRef(new Map<string, HTMLElement>());
  const lastKey = useRef<string | null>(null);
  const following = useRef(false);
  const hovered = useRef<string | null>(null);
  const [box] = useState(glideStore);
  const active = useRef(activeKey);
  active.current = activeKey;
  const sizes = useRef<ResizeObserver | null>(null);
  const measure = useRef(() => {});
  measure.current = () => {
    const n = nav.current;
    // the box rests under the open row when the pointer is away, so it glides from there; that row's fill covers it
    const el = rows.current.get(hovered.current ?? active.current);
    let next: GlideBox | null = null;
    if (n && el?.isConnected) {
      const clips: Span[] = [];
      for (let p = el.parentElement; p && p !== n; p = p.parentElement) if (clipsOverflow(getComputedStyle(p).overflowY)) clips.push(p.getBoundingClientRect());
      next = glideBox(el.getBoundingClientRect(), clips, n.getBoundingClientRect(), n.scrollTop);
    }
    // only a real move updates
    box.set((b) => (next ? (b.on && sameBox(b.at, next) ? b : { at: next, on: true }) : b.on ? { at: b.at, on: false } : b));
  };
  useEffect(() => {
    const n = nav.current;
    if (!n) return;
    const stop = () => (following.current = false);
    const events = ["wheel", "touchstart", "pointerdown", "keydown"] as const;
    for (const e of events) n.addEventListener(e, stop, { passive: true });
    // layout moves the list doesn't re-render for, measured once per frame however many come in it
    let frame = 0;
    const soon = () => {
      if (!frame) frame = requestAnimationFrame(() => ((frame = 0), measure.current()));
    };
    const ro = new ResizeObserver(soon);
    sizes.current = ro;
    // the list and its blocks (sections) change height as a group inside folds or opens, and as rows come and go;
    // the rows themselves are observed as they register (one growing moves the rest)
    const watch = () => {
      ro.observe(n);
      for (const c of n.children) ro.observe(c);
      for (const el of rows.current.values()) ro.observe(el);
    };
    watch();
    const mo = new MutationObserver(() => (ro.disconnect(), watch(), soon()));
    mo.observe(n, { childList: true });
    // a Collapse's transition ending (it may end without a size change, e.g. its opacity) and scrolling inside the list
    n.addEventListener("transitionend", soon);
    n.addEventListener("scroll", soon, { passive: true, capture: true });
    return () => {
      events.forEach((e) => n.removeEventListener(e, stop));
      n.removeEventListener("transitionend", soon);
      n.removeEventListener("scroll", soon, { capture: true });
      mo.disconnect();
      ro.disconnect();
      sizes.current = null;
      cancelAnimationFrame(frame);
    };
  }, []);
  useLayoutEffect(() => {
    if (lastKey.current !== activeKey) {
      lastKey.current = activeKey;
      following.current = true;
    }
    const open = following.current ? rows.current.get(activeKey) : undefined;
    // a group that opened for it is still growing, and the list can't scroll past its end yet: again once it has
    if (open?.offsetHeight && reveal(open)) setTimeout(() => following.current && open.isConnected && reveal(open), 350);
    // runs after every render (rows fold, lists change)
    measure.current();
  });
  // keyedRefs keeps a key's entry for the element that holds it (a folded tab's row and its hidden pane row may share a
  // key, #2320); on top, each row's size is watched while it is registered, so a row growing or folding away moves the
  // glide (#2476)
  const [reg] = useState<RegFn>(() => {
    const keyed = keyedRefs(rows.current);
    return (k: string) => {
      const set = keyed(k);
      let mine: HTMLElement | null = null;
      return (el: HTMLElement | null) => {
        if (mine && mine !== el) sizes.current?.unobserve(mine);
        set(el);
        mine = el;
        if (el) sizes.current?.observe(el);
      };
    };
  });
  // one function for the list's life: it moves the box straight away (as a render's layout effect did) and changes
  // nothing the rows or the list render from
  const [hover] = useState(() => (k: string | null) => {
    hovered.current = k;
    measure.current();
  });
  const highlight = <GlideHighlight box={box} />;
  return { nav, reg, hover, highlight };
}

/** Where the box is: `at` is the last place it was on show; a box that goes away (`on` false) fades out there, and
 *  glides from there back. */
type GlideState = { at: GlideBox | null; on: boolean };

/** The box's place outside React state, so moving it re-renders only GlideHighlight. */
function glideStore() {
  let state: GlideState = { at: null, on: false };
  const subs = new Set<() => void>();
  return {
    get: () => state,
    set(f: (b: GlideState) => GlideState) {
      const next = f(state);
      if (next === state) return;
      state = next;
      subs.forEach((s) => s());
    },
    subscribe(s: () => void) {
      subs.add(s);
      return () => void subs.delete(s);
    },
  };
}

function GlideHighlight({ box }: { box: ReturnType<typeof glideStore> }) {
  // box.get is the server snapshot too: the shell views are also rendered to a string (shell-views.test)
  const { at, on } = useSyncExternalStore(box.subscribe, box.get, box.get);
  return <span aria-hidden data-glide className="pointer-events-none absolute inset-x-2.5 rounded-[7px] bg-hover" style={glideStyle(at, on, EASE)} />;
}

export function Sidebar({ route, onSearch, onNewAgent, onNavigate, workspaceActions }: { route: Route; onSearch: () => void; onNewAgent: () => void; onNavigate?: () => void; workspaceActions?: WorkspaceTreeActions }) {
  const bindings = useBindings();
  const searchHint = jumpHint(bindings);
  const assistant = useStore((s) => s.assistant);
  const { phase, online, total, main: mainConversation, pages, more } = useNavPages();
  // a phone's drawer has the pages as two rows of icons, so its first screen reaches the Agents (#1996)
  const narrow = useMedia(NARROW);
  const activeKey = useActiveKey(route);
  const workBrowserReady = useStore((s) => s.features.includes("sessions_dimensions"));
  // Persona chats are the main conversation (its row above) and the leaders, "常驻" (#4331): making a chat one and ending
  // it is the menu's 设为常驻 / 取消常驻, on the hub for every device; every other chat is under 全部对话. Each says what
  // it has open, Working and Waiting on you, and an open sidebar follows the hub's `directions` (lib/store.ts).
  const leaders = (assistant?.leaders ?? []).filter((id) => id !== assistant?.main);
  const isLeader = (id: string) => leaders.includes(id);
  const directions = leaders.map((id) => assistant?.conversations.find((c) => c.session_id === id)).filter((c): c is Session => !!c);
  const directionNames = useDirectionNames(directions.map((c) => `${c.session_id}\u0000${c.title ?? ""}`).join("\u0001"));
  const counts = useLeaderCounts();
  const leaderTotal = totalCounts(counts, leaders);
  // the main row sits above the pages, so"Persona chats"waits for the assistant (a hub without it has none)
  const chats = !!assistant;
  // a conversation row's ⋯ / right click (iOS: its long press)
  const [convMenu, setConvMenu] = useState<{ s: Session; anchor: HTMLElement; at?: MenuAt } | null>(null);
  // one function for the sidebar's life: the conversation rows and the main row keep their memo across pushes
  const onConvMenu = useCallback<RowMenuFn>((s, anchor, at) => setConvMenu((m) => (!at && m?.anchor === anchor ? null : { s, anchor, at })), []);
  const [personaFolded, togglePersona] = useFolded("persona", false, { holds: activeKey === "history" || activeKey === "j:new" || directions.some((c) => activeKey === `j:${c.session_id}`) });
  // the pages fold under their own title (#2524); the drawer's grid of icons doesn't
  const [pagesFolded, togglePages] = useFolded("pages", false, { all: false, holds: more.some((p) => p.k === activeKey) });
  const { nav, reg, hover, highlight } = useGlide(activeKey);
  useStickyTop(nav);
  const go = useGo(onNavigate);

  const directionRow = (c: Session) => (
    <SessionRow key={c.session_id} s={c} k={`j:${c.session_id}`} reg={reg} hover={hover} active={activeKey === `j:${c.session_id}`} to={conversationPage(c.session_id, c.machine_id, c.manager)} go={go} label={directionLabel(directionNames, c.session_id, conversationTitle(c))} hint={countsLabel(counts.get(c.session_id), t) || undefined} hintFull showState={false} icon="pin" onMenu={onConvMenu} menuLabel={t("conversation-menu.actions")} />
  );

  return (
    <aside className="flex h-full w-full flex-col bg-canvas">
      <div className="px-2.5 pt-2.5">
        <div className="mb-2 flex items-center gap-1">
          <div className="min-w-0 flex-1"><AssistantSwitcher phase={phase} online={online} total={total} route={route} /></div>
          {/* folds the column to the rail of icons (a wide screen; the drawer closes by its scrim) */}
          {!narrow && <CollapseButton collapsed={false} chord={bindings["left-sidebar"]} />}
        </div>
        <button type="button" onClick={onSearch} className="touch-hit mb-1 flex h-8 w-full items-center gap-2 rounded-control bg-inset px-2.5 text-left shadow-hairline transition-colors hover:bg-hover">
          <Icon name="search" size={12} stroke={2} className="text-ink-3" />
          <span className="min-w-0 flex-1 truncate text-[12.5px] text-ink-3">{t("sidebar.search_sessions")}</span>
          <span className="hidden sm:inline-flex"><Kbd>{searchHint}</Kbd></span>
        </button>
      </div>

      {/* scroll-pt: a row reached by Tab doesn't stop under the pinned Persona row — about 62px with its 主对话 line
          (4 + 48 + 1 gap + 1 line + 8 margins), 58px on a coarse pointer (44px touch row): 64px covers both */}
      {/* isolate: the rows' own layers (the sticky Persona row and group headers at z-30, chips, segments) stay inside this
          column, so the usage popover that opens upward over it from the bottom is never drawn under them */}
      <div ref={nav} data-sidebar-rows onMouseLeave={() => hover(null)} className="relative isolate min-h-0 flex-1 scroll-pt-16 overflow-y-auto overscroll-contain px-2.5 pb-3">
        {highlight}
        {/* the main conversation is the column's first row, Persona with the app's mark, and only there (#2004, #5109); it
            stays at the top while the rest scrolls (#5581), with a solid divider above the sticky groups */}
        <div data-sidebar-sticky className="sticky top-0 z-30 flex flex-col gap-px bg-canvas pt-1">
          <PersonaRow reg={reg} hover={hover} active={activeKey === "persona"} go={go} main={mainConversation} chord={bindings.persona} onMenu={onConvMenu} />
          <span aria-hidden data-persona-divider className="mx-2 my-1 h-px shrink-0 bg-line" />
        </div>
        {narrow && <NavGrid pages={pages} more={more} activeKey={activeKey} go={go} onNavigate={onNavigate} />}
        {!narrow && (
          <Section title={t("sidebar.pages")} folded={pagesFolded} onToggle={togglePages} right={pagesFolded ? <PagesTally pages={[...pages, ...more]} /> : undefined} first>
            {pages.map((p) => <NavRow key={p.k} {...p} reg={reg} hover={hover} active={activeKey === p.k} go={go} />)}
            {more.length > 0 && <MorePages more={more} reg={reg} hover={hover} activeKey={activeKey} go={go} />}
          </Section>
        )}

        {chats && (
          <Section
            title={t("sidebar.persona_chats")}
            folded={personaFolded}
            onToggle={togglePersona}
            // New chat lives with the chats it makes (#3231, after #2303 moved it above the main chat): the compose icon at
            // the end of the section's title, as on the phone's sidebar, folded or not
            right={
              <>
                {/* folded, only the two numbers stay: what the leaders have Working and Waiting on you */}
                {personaFolded && countsLabel(leaderTotal, t) && <span className="truncate text-[11px] tabular-nums text-ink-3">{countsLabel(leaderTotal, t)}</span>}
                <SectionAdd icon="compose" label={t("sidebar.new_chat")} to={{ name: "persona-new" }} go={go} active={activeKey === "j:new"} />
              </>
            }
          >
            {directions.length > 0 && <SubHeading label={t("sidebar.directions")} />}
            {directions.map(directionRow)}
            {assistant && <NavRow k="history" reg={reg} hover={hover} active={activeKey === "history"} to={{ name: "history" }} go={go} icon="list" label={t("sidebar.all_chats")} hint={assistant.conversations.length > 0 ? String(assistant.conversations.length) : undefined} />}
          </Section>
        )}

        <PersonalAgents reg={reg} hover={hover} activeKey={activeKey} go={go} />

        {workBrowserReady ? <PendingAgents route={route} reg={reg} hover={hover} go={go} onNewAgent={onNewAgent} onNavigate={onNavigate} />
          : !(narrow && route.name === "agent-list") && <AgentsBlock reg={reg} hover={hover} activeKey={activeKey} go={go} onNewAgent={onNewAgent} onNavigate={onNavigate} workspaceActions={workspaceActions} foldable />}
      </div>
      {convMenu && <ConversationRowMenu s={convMenu.s} main={convMenu.s.session_id === assistant?.main} leader={isLeader(convMenu.s.session_id)} anchor={convMenu.anchor} at={convMenu.at} onClose={() => setConvMenu(null)} />}
      {/* subscription rate-limit windows (#2432): nothing is drawn without data */}
      <SidebarUsage />
      <NodePrompt />
    </aside>
  );
}

function PendingAgents({ route, reg, hover, go, onNewAgent, onNavigate }: {
  route: Route; reg: RegFn; hover: (key: string | null) => void; go: GoFn; onNewAgent: () => void; onNavigate?: () => void;
}) {
  const scope = useScope();
  const { sessions, loaded } = usePendingWorkSessions(scope.several ? scope.id : undefined);
  const interactions = useScopedInteractions();
  const answered = useStore((s) => s.answered);
  const machines = useScopedMachines();
  const rows = useMemo(() => sidebarPending(sessions, interactions, answered), [sessions, interactions, answered]);
  const [folded, toggle] = useFolded("work-pending", false, { all: false });
  return (
    <Section title={t("work-entry.pending")} folded={folded} onToggle={toggle} right={<SectionAdd label={t("sidebar.new_agent")} onClick={onNewAgent} />}>
      {loaded && !rows.length && <p className="px-2 py-1.5 text-[12px] text-ink-3">{t("work-entry.pending_empty")}</p>}
      {rows.map((row) => {
        const card = row.cards[0];
        const label = card?.session_title || (row.session && sessionTitle(row.session)) || baseName(card?.cwd) || t("work-entry.pending_request");
        const active = row.to.name === "request" ? route.name === "request" && route.id === row.to.id
          : row.to.name === "session" && showsSession(route, row.to.mid, row.to.sid);
        return <NavRow key={row.key} k={row.key} reg={reg} hover={hover} active={active} to={row.to}
          go={(to) => (e) => { onLinkClick(e, to, card ? cardState(card.id) : null); if (e.defaultPrevented) onNavigate?.(); }}
          label={label} title={[label, machineWithHub(row.machineId, machines)].filter(Boolean).join(" · ")} icon="chat" dot="needs_you"
          badge={row.cards.length > 1 ? row.cards.length : undefined} badgeTone="attention" />;
      })}
      <NavRow k="work:all" reg={reg} hover={hover} active={false} to={{ name: "terminal" }} go={go} label={t("work-entry.all")} icon="list" />
    </Section>
  );
}

/** The phone's "Agents" page (#1996): the sidebar's Agents block, the same components, filling the page. */
export function AgentsList({ route, onNewAgent, workspaceActions }: { route: Route; onNewAgent: () => void; workspaceActions?: WorkspaceTreeActions }) {
  const activeKey = useActiveKey(route);
  const { nav, reg, hover, highlight } = useGlide(activeKey);
  const go = useGo();
  // no computer and nothing to list (docs/assistant/onboarding.md 4.5): the machines page's empty state, not a bare title
  const bare = useStore((s) => s.loaded && s.machines.length === 0 && Object.keys(s.launches).length === 0);
  const none = useAgentSessions().length === 0 && bare;
  const adding = useAddComputer();
  return (
    <div className="h-full overflow-y-auto overscroll-contain">
      <div ref={nav} onMouseLeave={() => hover(null)} className="relative mx-auto max-w-[640px] px-2.5 pb-6">
        {highlight}
        {none ? <NoComputers body={t("agentspage.no_computers_body")} onAdd={adding.open} /> : <AgentsBlock reg={reg} hover={hover} activeKey={activeKey} go={go} onNewAgent={onNewAgent} workspaceActions={workspaceActions} add={false} />}
      </div>
      {adding.dialog}
    </div>
  );
}

/** How many columns the phone's page grid has for `n` pages: two even rows (8 → 4 + 4), at most 5 to a row. */
export function navGridColumns(n: number): number {
  return Math.max(1, Math.min(5, Math.ceil(n / 2)));
}

/** The pages as two rows of icons in a phone's drawer: one tap away like the list, a third of its height. */
export function NavGrid({ pages, more = [], activeKey, go, onNavigate }: { pages: NavPage[]; more?: NavPage[]; activeKey: string; go: GoFn; onNavigate?: () => void }) {
  const [menu, setMenu] = useState<HTMLElement | null>(null);
  const moreActive = more.some((p) => p.k === activeKey);
  const moreBadge = more.find((p) => p.badge !== undefined);
  return (
    <nav aria-label={t("sidebar.pages")} className="mb-1 grid gap-0.5" style={{ gridTemplateColumns: `repeat(${navGridColumns(pages.length + (more.length ? 1 : 0))}, minmax(0, 1fr))` }}>
      {pages.map((p) => {
        const active = activeKey === p.k;
        return (
          <a
            key={p.k}
            href={href(p.to)}
            onClick={go(p.to)}
            title={p.title ?? p.label}
            aria-label={p.badge !== undefined ? `${p.label} ${p.badge}` : p.label}
            aria-current={active ? "page" : undefined}
            className={cx("relative z-10 flex min-h-12 min-w-0 flex-col items-center justify-center gap-1 rounded-[9px] px-0.5 py-1.5 transition-[background-color,transform] duration-150 active:scale-[0.96]", active ? "bg-hover" : "hover:bg-hover")}
          >
            <span className={cx("relative", active ? "text-ink" : "text-ink-3")}>
              <Icon name={p.icon} size={17} stroke={1.8} />
              {p.badge !== undefined && (
                <span className={cx("absolute -top-1.5 left-2.5 flex h-4 min-w-4 items-center justify-center whitespace-nowrap rounded-full px-1 text-[10px] font-semibold tabular-nums", BADGE_CLASS[p.badgeTone ?? "accent"])}>{p.badge}</span>
              )}
            </span>
            <span className={cx("w-full truncate text-center text-[11px] leading-tight", active ? "font-medium text-ink" : "text-ink-2")}>{p.short ?? p.label}</span>
          </a>
        );
      })}
      {more.length > 0 && (
        <button
          type="button"
          onClick={(e) => setMenu(menu ? null : e.currentTarget)}
          aria-haspopup="menu"
          aria-expanded={!!menu}
          aria-label={t("sidebar.more_pages_count", { count: more.length })}
          className={cx("relative z-10 flex min-h-12 min-w-0 flex-col items-center justify-center gap-1 rounded-[9px] px-0.5 py-1.5 transition-[background-color,transform] duration-150 active:scale-[0.96]", moreActive || menu ? "bg-hover" : "hover:bg-hover")}
        >
          <span className={cx("relative", moreActive ? "text-ink" : "text-ink-3")}>
            <Icon name="more" size={17} stroke={1.8} />
            {moreBadge && <span aria-hidden className={cx("absolute -top-0.5 -right-1 size-2 rounded-full ring-2 ring-canvas", DOT_CLASS[moreBadge.badgeTone ?? "accent"])} />}
          </span>
          <span className={cx("w-full truncate text-center text-[11px] leading-tight", moreActive ? "font-medium text-ink" : "text-ink-2")}>{t("sidebar.more_pages")}</span>
        </button>
      )}
      {menu && <Menu anchor={menu} onClose={() => setMenu(null)} align="right" width={220} header={t("sidebar.more_pages")} items={morePagesMenu(more, activeKey, onNavigate)} />}
    </nav>
  );
}

/**
 * "More" at the end of the pages (#5026): the ones put away in Settings › General › Sidebar, and the extensions' pages past
 * the list's share of them. Folded by default; it opens by itself while one of its pages is open, and a count on one
 * of them shows as a dot on the row, so nothing waiting there goes unseen.
 */
function MorePages({ more, reg, hover, activeKey, go }: { more: NavPage[]; reg: RegFn; hover: (k: string | null) => void; activeKey: string; go: GoFn }) {
  const [folded, toggle] = useFolded("pages-more", true, { all: false, holds: more.some((p) => p.k === activeKey) });
  const counted = more.filter((p) => p.badge !== undefined);
  return (
    <>
      <button
        type="button"
        onClick={toggle}
        aria-expanded={!folded}
        title={counted.map((p) => `${p.label} ${p.badge}`).join(t("sidebar.list_separator")) || undefined}
        className="touch-hit relative z-10 flex w-full items-center gap-2 rounded-[7px] px-2 sidebar-row text-left transition-[background-color,transform] duration-150 hover:bg-hover active:scale-[0.98] [@media(pointer:coarse)]:min-h-11"
      >
        <span className="text-ink-3"><Icon name="more" size={14} stroke={1.8} /></span>
        <span className="min-w-0 flex-1 truncate text-[13px] text-ink-2">{t("sidebar.more_pages")}</span>
        {folded && counted.length > 0 && <span aria-hidden className={cx("size-1.5 rounded-full", DOT_CLASS[counted[0].badgeTone ?? "accent"])} />}
        <span className="text-[11px] text-ink-3 tabular-nums">{more.length}</span>
        <Icon name="chevron" size={10} stroke={2.4} className="shrink-0 text-ink-3 transition-transform" style={{ ...sidebarFoldMotion(!folded), transform: folded ? "rotate(-90deg)" : "none" }} />
      </button>
      <Collapse open={!folded} sidebarFold>
        <div className="flex flex-col gap-px pl-3" inert={folded || undefined}>
          {more.map((p) => <NavRow key={p.k} {...p} reg={reg} hover={hover} active={activeKey === p.k} go={go} />)}
        </div>
      </Collapse>
    </>
  );
}

/** The sidebar's "Agents": grouping, the quick filter, "Pinned", "Starting", the groups (spaces, machines, repositories,
 *  states or todos) and "Recent". The drawer and the phone's Agents page both show it. `add`: the title row's "+". */
type AgentsBlockProps = { workspaceActions?: WorkspaceTreeActions; reg: RegFn; hover: (k: string | null) => void; activeKey: string; go: GoFn; onNewAgent: () => void; onNavigate?: () => void; add?: boolean; foldable?: boolean; chooseGrouping?: (g: AgentGrouping) => void };

function AgentsBlock(props: AgentsBlockProps) {
  const bounded = useStore((s) => s.features.includes(SESSIONS_LIST_FEATURE));
  const pins = useStore((s) => s.pins);
  const [grouping, setGrouping] = useState(() => storedValue("persona.agentGrouping"));
  useEffect(() => onGrouping(setGrouping), []);
  // A saved grouping (including an explicitly selected default) is a view preference the paged API cannot express.
  const grouped = AGENT_GROUPINGS.some((g) => g.key === grouping);
  // several assistants: the list is the current one's (docs/assistant/managers.md "Clients"), which the hub's paged list
  // cannot ask for, so the complete list is read and filtered here
  const several = useScope().several;
  const full = pins.sessions.length > 0 || pins.spaces.length > 0 || grouped || several;
  const chooseGrouping = useCallback((g: AgentGrouping) => {
    storeValue("persona.agentGrouping", g);
    setGrouping(g);
    props.hover(null);
  }, [props.hover]);
  const returnToPagedList = useCallback(() => {
    removeStoredValue("persona.agentGrouping");
    setGrouping(null);
    props.hover(null);
  }, [props.hover]);
  // Back to the paged list: forget the saved grouping. Offered only where the paged list exists and nothing is pinned
  // (pins keep the full list either way).
  const pagedList = bounded && grouped && !several && pins.sessions.length === 0 && pins.spaces.length === 0
    ? returnToPagedList
    : undefined;
  return bounded && !full ? <BoundedAgentsBlock {...props} chooseGrouping={chooseGrouping} /> : <LegacyAgentsBlock {...props} grouping={agentGrouping(grouping)} chooseGrouping={chooseGrouping} pagedList={pagedList} />;
}

function BoundedAgentsBlock({ reg, hover, activeKey, go, onNewAgent, add = true, chooseGrouping }: AgentsBlockProps) {
  const machines = useStore(selectSidebarMachines);
  const archives = useStore((s) => s.features.includes(AGENT_ARCHIVES_FEATURE));
  const [rowMenu, setRowMenu] = useState<{ s: Session; anchor: HTMLElement; at?: MenuAt } | null>(null);
  const [renaming, setRenaming] = useState<Session | null>(null);
  const [deleting, setDeleting] = useState<Session | null>(null);
  const [closing, setClosing] = useState<Session | null>(null);
  const [archiveClosing, setArchiveClosing] = useState<Session | null>(null);
  const [linkTarget, setLinkTarget] = useState<Session>();
  const closeLinkTarget = useCallback(() => setLinkTarget(undefined), []);
  // the ⋯ toggles the menu, as in the full list
  const onRowMenu = useCallback<RowMenuFn>((s, anchor, at) => setRowMenu((m) => (!at && m?.anchor === anchor ? null : { s, anchor, at })), []);
  return <>
    <div className={SESSION_LIST_STYLE.heading}><span className={SESSION_LIST_STYLE.headingText}>{t("agentspage.title")}</span>{archives && <SectionAdd icon="archive" label={t("agentmenus.archived_agents")} to={{ name: "agent-archives" }} go={go} />}{add && <SectionAdd label={t("sidebar.new_agent")} onClick={onNewAgent} />}</div>
    <HubNoticeLine />
    <HerdrNotices />
    <PagedSessionList root={runtimeSessions} path="/sessions" identity={runtimeIdentity} compare={runtimeOrder} removeIdentity={runtimeRemoval} event="sessions_list" onRepo={chooseGrouping ? () => chooseGrouping("repo") : undefined}
      activeAt={runtimeActiveAt} needsYou={runtimeNeedsYou} isOpen={(s) => activeKey === `s:${s.machine_id}/${s.session_id}`}
      label={(dimension, id) => dimension === "machine" ? machineWithHub(id, machines) : id}
      renderRow={(s) => {
        const k = `s:${s.machine_id}/${s.session_id}`;
        return <SessionRow s={s} k={k} reg={reg} hover={hover} active={activeKey === k} to={{ name: "session", mid: s.machine_id, sid: s.session_id }} go={go} onMenu={onRowMenu} hint={[machineWithHub(s.machine_id, machines), s.space_name || baseName(s.cwd)].filter(Boolean).join(" · ")} showTodoTag />;
      }} />
    {rowMenu && <AgentRowMenu s={rowMenu.s} anchor={rowMenu.anchor} at={rowMenu.at} onClose={() => setRowMenu(null)} onRename={setRenaming} onDelete={setDeleting} onLinkTodo={setLinkTarget} onCloseAgent={setClosing} onArchiveClose={setArchiveClosing} />}
    {renaming && <RenameDialog open session={renaming} onClose={() => setRenaming(null)} />}
    {deleting && <DeleteSessionDialog open session={deleting} machineName={machineWithHub(deleting.machine_id, machines)} onClose={() => setDeleting(null)} />}
    {closing && <CloseAgentDialog open session={closing} machineName={machineWithHub(closing.machine_id, machines)} onClose={() => setClosing(null)} />}
    {archiveClosing && <ArchiveCloseDialog open session={archiveClosing} machineName={machineWithHub(archiveClosing.machine_id, machines)} onClose={() => setArchiveClosing(null)} />}
    <LinkTodoDialog open={Boolean(linkTarget)} session={linkTarget} onClose={closeLinkTarget} />
  </>;
}

function LegacyAgentsBlock({ reg, hover, activeKey, go, onNewAgent, onNavigate, workspaceActions, add = true, foldable = false, grouping, chooseGrouping, pagedList }: AgentsBlockProps & { grouping: AgentGrouping; chooseGrouping: (g: AgentGrouping) => void; pagedList?: () => void }) {
  // the current assistant's computers, agents and to-dos (all of them with one assistant)
  const scope = useScope();
  const sidebarMachines = useStore(selectSidebarMachines);
  // the ids of the current assistant's computers (null with one assistant): strings, so a push of a computer's load or
  // memory leaves them equal and doesn't draw the block again
  const ownIds = useStore((s) => (scope.several ? scoped(s.machines, scope).map((m) => m.id) : null));
  const machines = useMemo(() => {
    if (!ownIds) return sidebarMachines;
    const own = new Set(ownIds);
    return sidebarMachines.filter((m) => own.has(m.id));
  }, [ownIds, sidebarMachines]);
  const { sessions: every, loaded } = useFullAgentSessions();
  const all = useScoped(every);
  const mutes = useStore((s) => s.mutes);
  const pins = useStore((s) => s.pins);
  const interactions = useStore((s) => s.interactions);
  const todos = useScopedTodos();
  const todosSupported = useStore((s) => s.todosSupported);
  const todosLoaded = useStore((s) => s.todosLoaded);
  const launches = useStore((s) => Object.values(s.launches).filter((launch) => launch.phase !== "ready"));
  const [dimFilters, setDimFilters] = useState<AgentDimensionFilters>(DEFAULT_DIMENSION_FILTERS);
  // chat-list rows only for someone who picked them in the Filter menu; the grouped list is the default (#4397)
  const [chatList, setChatList] = useState<boolean>(() => localStorage.getItem("persona.agentChatList") === "1");
  const toggleChatList = () => {
    setChatList((prev) => {
      const next = !prev;
      localStorage.setItem("persona.agentChatList", next ? "1" : "0");
      return next;
    });
    hover(null);
  };
  // Stable, so the memoized grouping bar skips store pushes that leave its props equal.
  const chooseGroupingOutOfChatList = useCallback((g: AgentGrouping) => {
    setChatList(false);
    localStorage.setItem("persona.agentChatList", "0");
    chooseGrouping(g);
  }, [chooseGrouping]);
  // "By pinned" (#4352) only where the hub sends the direction tree and there is a leader to group by
  const directionTree = useDirectionTree();
  const byDirection = groupsByDirection(directionTree);
  // a hub without todos shows by machine, a by-todo choice kept for when it has them; by pinned with no leaders (or an
  // older hub, or a first read that failed) shows by to-do, the choice kept for when there are
  const fallback = todosSupported === false ? "machine" : "todo";
  const shownGrouping = grouping === "direction" ? (byDirection || directionTree === undefined ? "direction" : fallback) : todosSupported === false && grouping === "todo" ? "machine" : grouping;
  // while the first read is out "By pinned" stays an option, so the control's value is always one of its options
  const offersDirection = byDirection || shownGrouping === "direction";
  const availableGroupings = useMemo(() => AGENT_GROUPINGS.filter((g) => (g.key !== "todo" || todosSupported !== false) && (g.key !== "direction" || offersDirection)), [todosSupported, offersDirection]);
  // "State first" / "Recently updated" (iOS AgentSort, G099): not under "By status", whose sections are the states already
  const [sortChoice, setSortChoice] = useState<AgentSort>(() => agentSort(localStorage.getItem(AGENT_SORT_KEY)));
  const byUpdate = sortChoice === "updated" && shownGrouping !== "state";
  // the title row's ⋯ (#2812): what the grouping dropdown held besides the groupings, the sort and "Fold all" / "Unfold all"
  const listMenu = useMenu();
  const listLabel = byUpdate ? t("sidebar.list_menu_sorted", { sort: AGENT_SORTS[1].label }) : t("sidebar.list_menu");
  // the quick filter ("All" / "Needs you" / "Unread" / "Working"), stacked on any grouping; what it showed when chosen stays (`kept`)
  const [filter, setFilter] = useState(() => agentFilter(storedValue("persona.agentFilter")));
  const [kept, setKept] = useState<ReadonlySet<string>>(() => new Set());
  // "botmux" beside the filters (#2786): botmux sessions shown or hidden in this browser
  const [botmuxHidden, setBotmuxHidden] = useState(() => botmuxAndFilter(filter, localStorage.getItem(BOTMUX_HIDDEN_KEY) === "1", "filter").hidden);
  const botmuxCount = useMemo(() => all.filter(isBotmux).length, [all]);
  const saveBotmuxHidden = useCallback((next: boolean) => {
    localStorage.setItem(BOTMUX_HIDDEN_KEY, next ? "1" : "0");
    setBotmuxHidden(next);
  }, []);
  const listed = useMemo(() => withoutBotmux(all, botmuxHidden), [all, botmuxHidden]);
  const facets = useMemo(() => buildAgentFacets(listed, machines, pins), [listed, machines, pins]);
  const evalCtx = useMemo(() => ({ machines, pins, interactions, now: Date.now() }), [machines, pins, interactions]);
  const { sessions: dimFiltered, plan: searchPlan } = useMemo(
    () => applyDimensionFilters(listed, dimFilters, evalCtx, facets),
    [listed, dimFilters, evalCtx, facets],
  );
  const dimActive = hasActiveDimensionFilters(dimFilters);
  const { live, recent: offline } = useMemo(() => splitAgentSessions(dimFiltered, machines), [dimFiltered, machines]);
  const seeded = useRef(false);
  useEffect(() => {
    // a filter saved in this browser starts from what it matches once the list has loaded
    if (!loaded || seeded.current) return;
    seeded.current = true;
    setKept(matchingKeys(live, filter, mutes));
  }, [loaded, live, filter, mutes]);
  const filtered = filter !== "all" || dimActive;
  const { needs, unread, working, bg_waiting, feishu, terminal } = useMemo(() => filterCounts(live, mutes), [live, mutes]);
  // A session can change its text or timestamp without changing any filter's count.
  const filterTotals = useMemo(() => ({ needs, unread, working, bg_waiting, feishu, terminal }), [needs, unread, working, bg_waiting, feishu, terminal]);
  const shown = useMemo(() => applyFilter(live, filter, mutes, kept), [live, filter, mutes, kept]);
  // a pinned agent shows once, under "Pinned" (an offline one too, unless a filter is on); the groups below are the rest
  const pinnedAgents = useMemo(() => pinnedSessions(filter !== "all" ? shown : [...shown, ...offline], pins), [filter, shown, offline, pins]);
  const unpinned = useMemo(() => {
    const keys = new Set(pinnedAgents.map(agentKey));
    return keys.size ? shown.filter((s) => !keys.has(agentKey(s))) : shown;
  }, [shown, pinnedAgents]);
  const chatListUnpinned = useMemo(() => [...unpinned].sort(byUpdated), [unpinned]);
  const recent = useMemo(() => (filter !== "all" ? [] : offline.filter((s) => !pinnedAgents.some((p) => agentKey(p) === agentKey(s)))), [filter, offline, pinnedAgents]);
  const { spaces, loose } = useMemo(() => {
    const built = buildSpaces(unpinned, machines);
    return byUpdate ? projectsByUpdate(built) : built;
  }, [unpinned, machines, byUpdate]);
  const groups = useMemo(() => {
    if (shownGrouping === "space" || shownGrouping === "repo" || shownGrouping === "machine") return [];
    if (shownGrouping === "todo" || shownGrouping === "direction") return [];
    return buildAgentGroups(unpinned, machines, "state", mutes);
  }, [unpinned, machines, shownGrouping, mutes, todos]);
  // by todo waits for the first GET /todos: the socket pushes the sessions before it answers, and a tree drawn without
  // the todos puts every agent under No to-do for a moment, then moves them under their todos (#2812)
  const todosPending = shownGrouping === "todo" && !todosLoaded;
  // Only inputs the tree reads rebuild it; unchanged nodes keep their objects so their rows' memo holds.
  const lastTodoTree = useRef<TodoTree | undefined>(undefined);
  const todoTree = useMemo(() => {
    if (shownGrouping !== "todo" || !todosLoaded) return undefined;
    const built = buildTodoTree(unpinned, machines, todos, mutes);
    return shareTree(lastTodoTree.current, byUpdate ? todoTreeByUpdate(built) : built);
  }, [unpinned, machines, shownGrouping, mutes, todos, todosLoaded, byUpdate]);
  lastTodoTree.current = todoTree;
  const catalog = useWorkspaceCatalog(shownGrouping === "repo");
  // by pinned: the hub's tree, the agents it does not hold last (lib/direction-tree.ts)
  const lastDirectionTree = useRef<TodoTree | undefined>(undefined);
  const directionAgents = useMemo(() => {
    if (shownGrouping !== "direction" || !byDirection) return undefined;
    return shareTree(lastDirectionTree.current, buildDirectionAgentTree(directionTree, unpinned, machines, mutes));
  }, [shownGrouping, byDirection, directionTree, unpinned, machines, mutes]);
  lastDirectionTree.current = directionAgents;
  const repoMachines = useMemo(() => {
    if (shownGrouping !== "repo") return [];
    const built = buildRepoMachineTree(unpinned, machines, { ...catalog, hiddenSources: botmuxHidden ? ["botmux"] : undefined, navigationSessions: [...shown, ...pinnedAgents.filter((p) => !shown.includes(p))], hideEmpty: filtered, compareSessions: byUpdate ? byUpdated : undefined });
    return byUpdate ? repoMachinesByUpdate(built) : built;
  }, [unpinned, machines, shownGrouping, byUpdate, catalog, shown, pinnedAgents, filtered, botmuxHidden]);
  const machineTree = useMemo(() => {
    if (shownGrouping !== "machine") return [];
    const built = buildMachineTree(machines, unpinned, { pins, hideEmpty: filtered });
    return byUpdate ? machineTreeByUpdate(built) : built;
  }, [shownGrouping, machines, unpinned, pins, filtered, byUpdate]);
  // by todo a group mixes hosts (herdr, botmux…): when the list has more than one, rows outside herdr name theirs (#1836)
  const mixedHosts = useMemo(() => (shownGrouping === "todo" || shownGrouping === "direction") && hostsOf(live).size > 1, [shownGrouping, live]);
  const unreadShown = filter === "unread" ? unreadOf(shown) : [];
  // Stable event handlers read the last committed list, including pushes received since the bar last rendered.
  const filterInputs = useRef({ live, mutes });
  useLayoutEffect(() => { filterInputs.current = { live, mutes }; }, [live, mutes]);
  const choose = useCallback((f: AgentFilter) => {
    if (botmuxAndFilter(f, botmuxHidden, "filter").hidden !== botmuxHidden) saveBotmuxHidden(false);
    storeValue("persona.agentFilter", f);
    setFilter(f);
    setKept(matchingKeys(filterInputs.current.live, f, filterInputs.current.mutes));
    hover(null);
  }, [botmuxHidden, hover, saveBotmuxHidden]);
  const toggleBotmux = useCallback(() => {
    const next = botmuxAndFilter(filter, !botmuxHidden, "botmux");
    saveBotmuxHidden(next.hidden);
    if (next.filter !== filter) choose(next.filter);
    hover(null);
  }, [filter, botmuxHidden, saveBotmuxHidden, choose, hover]);
  const botmux = useMemo(() => botmuxCount > 0 || botmuxHidden ? { hidden: botmuxHidden, onToggle: toggleBotmux } : undefined, [botmuxCount, botmuxHidden, toggleBotmux]);
  // Settings › Appearance › Show terminals in sidebar (off): the plain shells of each machine's terminal host, under its agents in "By machine"
  const showTerminals = useSidebarTerminals() && shownGrouping === "machine";
  const { listing: panes, refresh: refreshPanes } = usePanes(machines, showTerminals);
  const shellTrees = useMemo(() => {
    if (!showTerminals) return undefined;
    return Object.fromEntries(machines.map((m) => [m.id, shellsOnly(buildTerminalTree(panes[m.id]?.data, []))])) as Record<string, TermSpace[]>;
  }, [showTerminals, machines, panes]);
  const [rowMenu, setRowMenu] = useState<{ s: Session; anchor: HTMLElement; at?: MenuAt } | null>(null);
  const [spaceMenu, setSpaceMenu] = useState<{ sp: Space; anchor: HTMLElement; at?: MenuAt; fold?: MenuNode["fold"] } | null>(null);
  const [nodeMenu, setNodeMenu] = useState<{ node: MenuNode; at: MenuAt; header?: string } | null>(null);
  const [renaming, setRenaming] = useState<Session | null>(null);
  const [renamingSpace, setRenamingSpace] = useState<Session | null>(null);
  const [deleting, setDeleting] = useState<Session | null>(null);
  const [closingAgent, setClosingAgent] = useState<Session | null>(null);
  const [archiveClosing, setArchiveClosing] = useState<Session | null>(null);
  const archives = useStore((s) => s.features.includes(AGENT_ARCHIVES_FEATURE));
  const [confirmSeen, setConfirmSeen] = useState<Session[] | null>(null);
  // stable, like `go`: every row gets it, and a new one each render would re-render them all (SessionRow's memo)
  const onRowMenu = useCallback<RowMenuFn>((s, anchor, at) => setRowMenu((m) => (!at && m?.anchor === anchor ? null : { s, anchor, at })), []);
  // stable too: TabRow builds the menu of a tab with several panes on onNodeMenu, and that menu is its lead row's onMenu
  const onSpaceMenu = useCallback<SpaceMenuFn>((sp, anchor, at, fold) => setSpaceMenu((m) => (!at && m?.anchor === anchor ? null : { sp, anchor, at, fold })), []);
  const onNodeMenu = useCallback<NodeMenuFn>((node, at, header, toggle) => setNodeMenu((m) => (toggle && m?.at.anchor && m.at.anchor === at.anchor ? null : { node, at, header })), []);
  const [recentFolded, toggleRecent] = useFolded("recent", true, { holds: holdsRow(recent, activeKey), scope: AGENTS_FOLD });
  const [idleFolded, toggleIdle] = useFolded("idle-spaces", true, { scope: AGENTS_FOLD });
  const [pinnedFolded, togglePinned] = useFolded("agents-pinned", false, { holds: holdsRow(pinnedAgents, activeKey), scope: AGENTS_FOLD });
  const [launchesFolded, toggleLaunches] = useFolded("agents-launching", false, { holds: launches.some((l) => activeKey === `s:${l.machineId}/${l.id}`), scope: AGENTS_FOLD });
  // the whole block folds under its title in the sidebar (#2524); the phone's Agents page is the block, it doesn't
  const [blockFolded, toggleBlock] = useFolded("agents", false, { all: false });
  const folded = foldable && blockFolded;
  const groupsRef = useRef<HTMLDivElement>(null);
  const [showAllRecent, setShowAllRecent] = useState(false);
  const recentShown = showAllRecent ? recent : firstAndCurrent(recent, 8, (s) => rowKey(s) === activeKey);
  const [linkTargetSession, setLinkTargetSession] = useState<Session | undefined>();
  const [linkTargetSpace, setLinkTargetSpace] = useState<LinkTargetSpace | undefined>();
  const closeLinkSession = useCallback(() => setLinkTargetSession(undefined), []);
  const closeLinkSpace = useCallback(() => setLinkTargetSpace(undefined), []);
  const closeMarkSeen = useCallback(() => setConfirmSeen(null), []);
  const clearKept = useCallback(() => setKept(new Set()), []);

  // a space with nothing to look at (every agent seen and waiting, none awaiting your reply) goes to "Idle spaces", like
  // the app; the open conversation's space stays where it is so the row under the pointer doesn't move away
  const busy = (sp: Space) => sp.sessions.some((s) => !isQuiet(s, mutes) || activeKey === `s:${s.machine_id}/${s.session_id}`);
  // pinned spaces lead; under a quick filter nothing folds into "Idle spaces"
  const { active, idle } = arrangeSpaces(spaces, { pins, mutes, filtered, busy });
  const tree = { machines, reg, hover, activeKey, go, mutes, onSpaceMenu, onRowMenu, onNodeMenu, onNavigate };

  return (
    <div data-sidebar-agents ref={groupsRef}>
    <SidebarGroup className="mt-3" heading={
      <div data-sidebar-agents-controls className="pb-1">
      <div className="group/sec flex items-center gap-1 px-2">
        {foldable ? (
          <button type="button" aria-expanded={!folded} onClick={toggleBlock} className="touch-hit relative z-10 flex min-w-0 flex-1 items-center gap-1 whitespace-nowrap text-left text-[11px] font-semibold text-ink-3 hover:text-ink-2">
            <span className="min-w-0 truncate">Agents</span>
            <Icon name="chevron" size={10} stroke={2.4} className="shrink-0 opacity-0 transition-[opacity,transform] group-hover/sec:opacity-100 [@media(hover:none)]:opacity-100" style={{ ...sidebarFoldMotion(!folded), transform: folded ? "rotate(-90deg)" : "none" }} />
          </button>
        ) : (
          <span className="min-w-0 flex-1 text-[11px] font-semibold text-ink-3">Agents</span>
        )}
        {folded && <SessionTally sessions={live} folded />}
        <GroupFoldButton groups={groupsRef} onAction={() => hover(null)} />
        <button type="button" title={listLabel} aria-label={listLabel} aria-haspopup="menu" aria-expanded={Boolean(listMenu.anchor)} onClick={listMenu.open} className={cx("touch-hit relative z-10 flex size-5 shrink-0 items-center justify-center rounded-[5px] hover:bg-hover hover:text-ink-2", byUpdate ? "text-accent" : "text-ink-3", listMenu.anchor && "bg-hover")}>
          <Icon name={byUpdate ? "clock" : "more"} size={12} stroke={2} />
        </button>
        {add && <SectionAdd label={t("sidebar.new_agent")} onClick={onNewAgent} />}
      </div>
      <Collapse open={!folded} sidebarFold>
        <GroupingBar groupings={availableGroupings} selection={shownGrouping} onChoose={chooseGroupingOutOfChatList} />
        <FilterBar selection={filter} counts={filterTotals} onChoose={choose} botmux={botmux} />
      </Collapse>
      </div>
    }>
      {listMenu.anchor && (
        <Menu anchor={listMenu.anchor} onClose={listMenu.close} align="right" items={[...(shownGrouping === "state" ? [] : AGENT_SORTS.map((o): MenuItem => ({
          key: `sort:${o.key}`, label: o.label, icon: o.key === "updated" ? "clock" : "layers", checked: sortChoice === o.key,
          onSelect: () => { localStorage.setItem(AGENT_SORT_KEY, o.key); setSortChoice(o.key); hover(null); },
        }))),
          // every group of the list, like the app's list settings (#3027); "Expand all" also opens the spaces and tab groups
          // folded by hand that are not drawn now
          { key: "fold-all", label: t("sidebar.fold_all"), icon: "up", checked: false, divider: true, onSelect: () => { setAgentsFolded(true); hover(null); } },
          { key: "open-all", label: t("sidebar.open_all"), icon: "chevron", checked: false, onSelect: () => { setAgentsFolded(false); hover(null); } },
          // the agents archived from a row's ⋯ (lib/agent-archives.ts), to find and restore
          ...(archives ? [{ key: "archived", label: t("agentmenus.archived_agents"), icon: "archive" as const, divider: true, onSelect: () => { navigate({ name: "agent-archives" }); onNavigate?.(); hover(null); } }] : []),
          // a hub with the paged list: return to it from a grouping chosen there (its Browse by offers By repo)
          ...(pagedList ? [{ key: "paged-list", label: t("sidebar.paged_list"), icon: "clock" as const, divider: true, onSelect: pagedList }] : []),
        ]} />
      )}
      <FoldScope.Provider value={AGENTS_FOLD}>
      <Collapse open={!folded} sidebarFold unclipped>
      <div inert={folded || undefined}>
      <AgentDimensionFilterBar filters={dimFilters} onChange={setDimFilters} facets={facets} plan={searchPlan} chatList={chatList} onToggleChatList={toggleChatList} />
      {/* "herdr is not running on <computer>" with "Start" (#1916): before a message to an agent there fails */}
      {/* the one line about the hub (#2897): updating, rolled back, a newer version or image */}
      {!folded && <HubNoticeLine />}
      {!folded && <HerdrNotices />}

      {unreadShown.length > 0 && (
        <button type="button" onClick={() => setConfirmSeen(unreadShown)} className="relative z-10 mt-1 flex w-full items-center gap-2 rounded-[7px] px-2 sidebar-row text-left transition-[background-color,transform] duration-100 hover:bg-hover active:scale-[0.98] [@media(pointer:coarse)]:min-h-11">
          <span className="flex size-3.5 shrink-0 items-center justify-center text-green"><Icon name="check" size={13} stroke={2.4} /></span>
          <span className="min-w-0 flex-1 truncate text-[13px] text-ink">{markAllSeenLabel()}</span>
          <span className="shrink-0 text-[11px] whitespace-nowrap text-ink-3 tabular-nums">{stateCount("unread", unreadShown.length)}</span>
        </button>
      )}

      {filtered && shown.length === 0 && pinnedAgents.length === 0 && (
        <div className="mt-2 flex items-center gap-2 px-2 py-1.5 text-[12px] text-ink-3">
          <span className="min-w-0 flex-1 truncate">{filter !== "all" ? t("sidebar.filter_empty", { filter: AGENT_FILTERS.find((f) => f.key === filter)!.label }) : t("sidebar.no_match")}</span>
          <button type="button" onClick={() => { choose("all"); setDimFilters(DEFAULT_DIMENSION_FILTERS); }} className="touch-hit relative z-10 shrink-0 whitespace-nowrap rounded-control px-1.5 py-0.5 text-[12px] font-medium text-accent-ink hover:bg-hover">{t("sidebar.show_all")}</button>
        </div>
      )}

      {pinnedAgents.length > 0 && (
        <Section title={<span className="flex items-center gap-1"><Icon name="pin" size={10} stroke={2.2} />{t("sidebar.pinned_section")}</span>} folded={pinnedFolded} onToggle={togglePinned} right={<SessionTally sessions={pinnedAgents} folded={pinnedFolded} />}>
          {pinnedAgents.map((s) => {
            const key = `s:${s.machine_id}/${s.session_id}`;
            if (chatList) {
              return <AgentChatListRow key={key} s={s} k={key} reg={reg} hover={hover} active={activeKey === key} to={{ name: "session", mid: s.machine_id, sid: s.session_id }} go={go} pinned mutes={mutes} filters={dimFilters} ctx={evalCtx} onMenu={onRowMenu} />;
            }
            const context = [s.space_name || baseName(s.cwd), machines.length > 1 ? machineWithHub(s.machine_id, machines) : ""].filter(Boolean).join(" · ");
            return <SessionRow key={key} s={s} k={key} reg={reg} hover={hover} active={activeKey === key} to={{ name: "session", mid: s.machine_id, sid: s.session_id }} go={go} hint={context} onMenu={onRowMenu} />;
          })}
        </Section>
      )}

      {launches.length > 0 && <Section title={t("sidebar.launching")} folded={launchesFolded} onToggle={toggleLaunches} right={<GroupCount n={launches.length} label={t("sidebar.launching_count", { count: launches.length })} />}>
        {launches.map((launch) => <NavRow key={launch.id} k={`s:${launch.machineId}/${launch.id}`} reg={reg} hover={hover} active={activeKey === `s:${launch.machineId}/${launch.id}`} to={{ name: "session", mid: launch.machineId, sid: launch.id }} go={go} icon={launch.phase === "failed" ? "retry" : "agent"} label={launch.text || baseName(launch.cwd) || t("sidebar.launch_untitled")} hint={launch.phase === "failed" ? t("sidebar.launch_failed") : t("sidebar.launch_background")} />)}
      </Section>}

      {chatList ? (
        <div className="mt-1 flex flex-col gap-1" data-testid="agent-chatlist-section">
          {chatListUnpinned.map((s) => {
            const key = `s:${s.machine_id}/${s.session_id}`;
            return <AgentChatListRow key={key} s={s} k={key} reg={reg} hover={hover} active={activeKey === key} to={{ name: "session", mid: s.machine_id, sid: s.session_id }} go={go} pinned={false} mutes={mutes} filters={dimFilters} ctx={evalCtx} onMenu={onRowMenu} />;
          })}
        </div>
      ) : shownGrouping === "space" ? <>
      {active.map((sp) => (
        <SpaceSection key={sp.key} sp={sp} machineName={machines.length > 1 ? machineWithHub(sp.machineId, machines) : undefined} reg={reg} hover={hover} activeKey={activeKey} go={go} pinned={isSpacePinned(sp.machineId, sp.name, pins)} muted={isSpaceMuted(sp, mutes)} onMenu={onSpaceMenu} onRowMenu={onRowMenu} onNavigate={onNavigate} />
      ))}

      {idle.length > 0 && (
        <Section title={t("sidebar.idle_spaces", { count: idle.length })} folded={idleFolded} onToggle={toggleIdle}>
          {idle.flatMap((sp) =>
            sp.sessions.map((s) => (
              <SessionRow key={s.session_id} s={s} k={`s:${s.machine_id}/${s.session_id}`} reg={reg} hover={hover} active={activeKey === `s:${s.machine_id}/${s.session_id}`} to={{ name: "session", mid: s.machine_id, sid: s.session_id }} go={go} hint={sp.sessions.length > 1 ? `${sp.name} › ${tabLabel(s)}` : sp.name} onMenu={onRowMenu} />
            )),
          )}
        </Section>
      )}

      {loose.length > 0 && <LooseSection sessions={loose} reg={reg} hover={hover} activeKey={activeKey} go={go} onRowMenu={onRowMenu} />}

      </> : shownGrouping === "machine" ? machineTree.map((m) => (
        <MachineSection key={m.key} m={m} shells={shellTrees?.[m.machine.id]} {...tree} />
      )) : shownGrouping === "repo" ? (
        repoMachines.map((m) => (
          <RepoMachineSection key={m.key} m={m} flat={repoMachines.length <= 1} machines={machines} reg={reg} hover={hover} activeKey={activeKey} go={go} onRowMenu={onRowMenu} {...workspaceActions} />
        ))
      ) : shownGrouping === "direction" ? (
        directionAgents ? <TodoTreeView tree={directionAgents} machines={machines} showHost={mixedHosts} reg={reg} hover={hover} activeKey={activeKey} go={go} onRowMenu={onRowMenu} onNavigate={onNavigate} /> : <p className="px-2 py-1 text-[12px] text-ink-3">{t("sidebar.loading")}</p>
      ) : todosPending ? (
        <p className="px-2 py-1 text-[12px] text-ink-3">{t("sidebar.loading")}</p>
      ) : todoTree ? (
        <TodoTreeView tree={todoTree} machines={machines} showHost={mixedHosts} reg={reg} hover={hover} activeKey={activeKey} go={go} onRowMenu={onRowMenu} onNavigate={onNavigate} />
      ) : groups.map((group) => (
        <AgentSection key={group.key} group={group} machines={machines} byTodo={shownGrouping === "todo"} showHost={mixedHosts} reg={reg} hover={hover} activeKey={activeKey} go={go} onRowMenu={onRowMenu} onNavigate={onNavigate} />
      ))}

      {recent.length > 0 && (
        <Section title={t("sidebar.recent")} folded={recentFolded} onToggle={toggleRecent} right={<GroupCount n={recent.length} label={t("sidebar.agent_count", { count: recent.length })} />}>
          {recentShown.map((s) => (
            <SessionRow key={s.session_id} s={s} k={`s:${s.machine_id}/${s.session_id}`} reg={reg} hover={hover} active={activeKey === `s:${s.machine_id}/${s.session_id}`} to={{ name: "session", mid: s.machine_id, sid: s.session_id }} go={go} onMenu={onRowMenu} />
          ))}
          {recent.length > 8 && (
            <button type="button" onClick={() => setShowAllRecent((x) => !x)} className="relative z-10 w-full rounded-[7px] px-2 py-1 text-left text-[12px] text-ink-3 hover:text-ink-2 [@media(pointer:coarse)]:min-h-11">
              {showAllRecent ? t("sidebar.show_less") : t("sidebar.show_more", { count: recent.length - recentShown.length })}
            </button>
          )}
        </Section>
      )}
      </div>
      </Collapse>
      </FoldScope.Provider>
    </SidebarGroup>
      {/* the row's ⋯ (and its right click): pin, rename, mute, the todo it hangs on, close, delete — a long press on iOS
          Safari sends no contextmenu, so nothing lives only there */}
      {rowMenu && <AgentRowMenu s={rowMenu.s} anchor={rowMenu.anchor} at={rowMenu.at} onClose={() => setRowMenu(null)} onRename={setRenaming} onDelete={setDeleting} onLinkTodo={setLinkTargetSession} onCloseAgent={setClosingAgent} onArchiveClose={setArchiveClosing} />}
      {spaceMenu && (
        <SpaceMenu
          sp={spaceMenu.sp}
          anchor={spaceMenu.anchor}
          at={spaceMenu.at}
          fold={spaceMenu.fold}
          onClose={() => setSpaceMenu(null)}
          onRename={setRenamingSpace}
          onMarkSeen={setConfirmSeen}
          onLinkTodo={(sp) => setLinkTargetSpace({ machineId: sp.machineId, space: sp.name })}
        />
      )}
      {nodeMenu && <NodeMenu node={nodeMenu.node} at={nodeMenu.at} header={nodeMenu.header} onClose={() => setNodeMenu(null)} changed={() => { void refresh().catch(() => {}); void refreshPanes(); }} />}
      {renaming && <RenameDialog open session={renaming} onClose={() => setRenaming(null)} />}
      {renamingSpace && <RenameSpaceDialog open session={renamingSpace} onClose={() => setRenamingSpace(null)} />}
      {deleting && <DeleteSessionDialog open session={deleting} machineName={machineWithHub(deleting.machine_id, machines)} onClose={() => setDeleting(null)} />}
      {closingAgent && <CloseAgentDialog open session={closingAgent} machineName={machineWithHub(closingAgent.machine_id, machines)} onClose={() => setClosingAgent(null)} />}
      {archiveClosing && <ArchiveCloseDialog open session={archiveClosing} machineName={machineWithHub(archiveClosing.machine_id, machines)} onClose={() => setArchiveClosing(null)} />}
      <MarkSeenDialog list={confirmSeen} onClose={closeMarkSeen} onDone={clearKept} />
      <LinkTodoDialog open={Boolean(linkTargetSession)} session={linkTargetSession} onClose={closeLinkSession} />
      <LinkTodoDialog open={Boolean(linkTargetSpace)} space={linkTargetSpace} onClose={closeLinkSpace} />
    </div>
  );
}

/** How Agents are grouped (#2812): one segment per grouping, "By to-do" first, above the quick filter. The segments say
 *  the short name (To-do / Machine…) so all five fit the sidebar's width on one line; the full name is their label. */
const GroupingBar = memo(function GroupingBar({ groupings, selection, onChoose }: { groupings: readonly { key: AgentGrouping; label: string; short: string }[]; selection: AgentGrouping; onChoose: (g: AgentGrouping) => void }) {
  return (
    <div role="radiogroup" aria-label={t("sidebar.grouping_header")} className="mt-1.5 mx-1 flex gap-0.5 rounded-full bg-inset p-0.5 shadow-hairline">
      {groupings.map((g) => {
        const on = g.key === selection;
        return (
          <button
            key={g.key}
            type="button"
            role="radio"
            aria-checked={on}
            aria-label={g.label}
            title={g.label}
            onClick={() => onChoose(g.key)}
            className={cx(
              "touch-hit relative z-10 flex h-6 min-w-0 flex-auto items-center justify-center whitespace-nowrap rounded-full px-1.5 text-[11.5px] transition-[background-color,color,box-shadow] duration-150",
              on ? "bg-surface font-medium text-ink shadow-btn" : "text-ink-2 hover:bg-hover hover:text-ink",
            )}
          >
            <span className="truncate">{g.short}</span>
          </button>
        );
      })}
    </div>
  );
});

/** "All" / "Needs you" / "Unread" / "Working" with their counts (iOS QuickFilterBar): "All" and the states that have agents now; the
 * chosen one stays while it empties, so it can be left. Then "botmux" (#2786), when there are botmux sessions: a
 * show / hide switch that stacks on any filter. Hidden when there is nothing to pick. */
const FilterBar = memo(function FilterBar({ selection, counts, onChoose, botmux }: { selection: AgentFilter; counts: FilterCounts; onChoose: (f: AgentFilter) => void; botmux?: { hidden: boolean; onToggle: () => void } }) {
  const keys = shownFilters(selection, counts);
  if (keys.length < 2 && !botmux) return null;
  return (
    <div className="mt-1.5 flex flex-wrap gap-0.5 px-1">
      <div role="radiogroup" aria-label={t("sidebar.quick_filters")} className="contents">
      {keys.map((k) => {
        const f = AGENT_FILTERS.find((x) => x.key === k)!;
        const on = k === selection;
        const n = k === "all" ? undefined : counts[k];
        return (
          <button
            key={k}
            type="button"
            role="radio"
            aria-checked={on}
            aria-label={n === undefined ? f.label : `${f.label} ${n}`}
            onClick={() => onChoose(k)}
            className={cx(
              "touch-hit relative z-10 flex h-6 items-center gap-1 whitespace-nowrap rounded-full px-[7px] text-[11.5px] transition-[background-color,color,box-shadow] duration-150",
              on ? "bg-surface font-medium text-ink shadow-btn" : "text-ink-2 hover:bg-hover hover:text-ink",
            )}
          >
            {f.state && <StatusMark state={f.state} size="sm" waiting={k === "bg_waiting"} />}
            {f.label}
            {n !== undefined && <span className={cx("tabular-nums", on ? "text-ink-2" : "text-ink-3")}>{n}</span>}
          </button>
        );
      })}
      </div>
      {botmux && (
        <button
          type="button"
          aria-pressed={!botmux.hidden}
          title={t(botmux.hidden ? "lib-agent-filter.botmux_show" : "lib-agent-filter.botmux_hide")}
          onClick={botmux.onToggle}
          className={cx(
            "touch-hit relative z-10 flex h-6 items-center gap-1 whitespace-nowrap rounded-full px-[7px] text-[11.5px] transition-[background-color,color,box-shadow] duration-150",
            botmux.hidden ? "text-ink-3 line-through hover:bg-hover hover:text-ink-2" : "text-ink-2 hover:bg-hover hover:text-ink",
          )}
        >
          <Icon name={botmux.hidden ? "x" : "check"} size={10} stroke={2.4} />
          {t("lib-agent-filter.botmux")}
        </button>
      )}
    </div>
  );
});

// Every group in the sidebar is one of these: its title folds it, and the fold is kept in this browser (useFolded).
function AgentSection({ group, machines, byTodo, showHost, reg, hover, activeKey, go, onRowMenu, onNavigate }: { group: AgentGroup; machines: Machine[]; byTodo?: boolean; showHost?: boolean; reg: RegFn; hover: (k: string | null) => void; activeKey: string; go: GoFn; onRowMenu: RowMenuFn; onNavigate?: () => void }) {
  const [folded, toggle] = useFolded(group.key, group.folded, { follow: true, holds: holdsRow(group.sessions, activeKey) });
  const row = (s: Session) => {
    const key = `s:${s.machine_id}/${s.session_id}`;
    const context = [s.space_name || baseName(s.cwd), machines.length > 1 ? machineWithHub(s.machine_id, machines) : ""].filter(Boolean).join(" · ");
    return <SessionRow key={key} s={s} k={key} reg={reg} hover={hover} active={activeKey === key} to={{ name: "session", mid: s.machine_id, sid: s.session_id }} go={go} hint={context} host={showHost && sessionHost(s) !== "herdr" ? sessionHost(s) : undefined} showTodoTag={!byTodo} onMenu={onRowMenu} />;
  };

  const statusInfo = group.todoStatus ? TODO_STATUS_INFO[group.todoStatus as TodoStatus] : undefined;
  const statusTone = statusInfo?.tone === "red" ? "text-red" : statusInfo?.tone === "green" ? "text-green" : statusInfo?.tone === "amber" ? "text-orange" : "text-ink-3";

  const titleNode = group.todoId ? (
    <span className="flex min-w-0 items-center gap-1.5">
      <Icon name="todo" size={12} stroke={2} className="shrink-0 text-ink-3" />
      <span
        onClick={(e) => {
          e.stopPropagation();
          openTodoFromTag(group.todoId!, navigate, onNavigate);
        }}
        className="truncate normal-case tracking-normal hover:underline hover:text-ink cursor-pointer"
      >
        {group.name}
      </span>
    </span>
  ) : (
    <span className="flex min-w-0 items-center gap-1.5">
      {group.state && <span aria-hidden><StatusMark state={group.state} size="sm" waiting={group.waiting} /></span>}
      <span className="truncate">{group.name}</span>
    </span>
  );

  const rightNode = group.todoStatus ? (
    <span className={cx("text-[11px] font-semibold tracking-normal normal-case", statusTone)}>
      {statusInfo?.label}
    </span>
  ) : (
    <GroupCount n={group.sessions.length} label={t("sidebar.agent_count", { count: group.sessions.length })} />
  );

  return (
    <Section title={titleNode} folded={folded} onToggle={toggle} right={rightNode} hint={group.hint}>
      {group.sessions.map(row)}
    </Section>
  );
}

// A section's "+" (a new conversation, a new agent): quiet, next to the section's title.
/** A small button at the end of a section's heading: "+" (add), or another page of the section (`icon`). */
/** A section title's own action as an icon (New chat on Persona chats, New group…); `active`: the page it opens is open. */
function SectionAdd({ label, to, go, onClick, icon: name = "plus", active }: { label: string; to?: Route; go?: GoFn; onClick?: () => void; icon?: IconName; active?: boolean }) {
  const cls = cx("touch-hit relative z-10 flex size-5 shrink-0 items-center justify-center rounded-[5px] hover:bg-hover hover:text-ink-2", active ? "bg-hover text-ink" : "text-ink-3");
  const icon = <Icon name={name} size={12} stroke={2} />;
  if (to && go) return <a href={href(to)} onClick={go(to)} title={label} aria-label={label} aria-current={active ? "page" : undefined} className={cls}>{icon}</a>;
  return <button type="button" onClick={onClick} title={label} aria-label={label} className={cls}>{icon}</button>;
}
