import { Suspense, useCallback, useEffect, useId, useRef, useState } from "react";
import { api, ApiError, errorText, getToken, handleUnauthorized, setToken } from "./lib/api";
import { pair, takeToken } from "./lib/auth";
import { useCloseOnBack } from "./lib/back-close";
import { SCRIM_FADE, useLingers } from "./lib/lingers";
import { PageBoundary } from "./views/PageBoundary";
import { PerAssistant } from "./views/PerAssistant";
import { OverlayBoundary, useOpened } from "./views/LazyOverlay";
import { navigationCrumb } from "./lib/error-reports";
import { lazyPage, preloadPages } from "./lib/lazy-page";
import { drawerA11y, focusBack, useDrawerDrag } from "./lib/drawer";
import { acceptHubs, here, rememberHub, takeHubs } from "./lib/hubs";
import { useSessionPanels } from "./lib/panel-memory";
import { resolveTab, tabOfSessionLink, tabPagePane } from "./lib/herdr-tree";
import { resolveSessionLink, sessionLinkRoute, type SessionLink } from "./lib/session-link";
import { href, inWorkView, navigate, onLinkClick, type Route, routePath, useRoute, withWorkView } from "./lib/router";
import { agentChatKey, noteChatShown, personaChatKey } from "./lib/recent-chats";
import { ago, sessionTitle, shortPath } from "./lib/format";
import type { Session } from "./lib/types";
import { unreachable } from "./lib/shell";
import { useTabLookup } from "./lib/tab-lookup";
import { ConnectionPill } from "./views/ConnectionPill";
import { NARROW, useKeyboardFrame, useMedia } from "./lib/viewport";
import { connect, disconnect, reset, type State, useStore } from "./lib/store";
import { watchRoster } from "./lib/managers";
import { t } from "./i18n";
import { Button, cx, EASE, IconButton, Loading, Spinner, Toasts } from "./ui/atoms";
import { focusables, trapTab } from "./ui/overlay";
import { Empty } from "./views/session/files/parts";
import { AgentsPage } from "./views/AgentsPage";
import { AgentArchives } from "./views/agents/AgentArchives";
import { ChatView } from "./views/chat/ChatView";
import { RequestPage } from "./views/RequestPage";
import { Home } from "./views/Home";
import { PersonaAssistant } from "./views/PersonaAssistant";
import { NewWorktreeHost } from "./views/NewWorktreeHost";
import { WorktreeActionsHost } from "./views/WorktreeActions";
import { openWorktreeActions } from "./lib/worktree-actions";
import { selectWorkspace } from "./lib/workspace-selection";
import { setAgentGrouping } from "./lib/sidebar-api";
import type { WorkspaceTarget } from "./lib/workspace-target";
import { ForwardHost } from "./views/chat/ForwardHost";
import { onOpenNewAgent, type NewAgentPreset } from "./lib/new-agent";
import { StartingAgent } from "./views/StartingAgent";
import { ShortcutHelp } from "./views/ShortcutHelp";
import { closePalette, openPalette, usePalette } from "./lib/palette";
import { sidebarRail, sidebarShown, useLayout } from "./lib/layout";
import { SIDEBAR_LABEL, useShortcuts } from "./lib/use-shortcuts";
import { Sidebar, SidebarRail } from "./views/Sidebar";
import { SidebarResize } from "./views/SidebarResize";
import { SidePanel } from "./views/SidePanel";
import { ExtensionNavPage } from "./views/extensions/ExtensionSlots";
import { useExtensionPlace } from "./lib/extension-slots";
import { AttentionSignals } from "./lib/attention-signals";

// The pages a first screen doesn't need load when they are opened (#1996), or as a link to their route is reached
// (lib/lazy-page.ts); "Chat", Persona and "Overview" stay in the main bundle (the extensions' page too: the sidebar already has
// ExtensionSlots for its icons).
// The sign-in page is only for a browser that isn't signed in yet (#4603): every load after that would carry it on the
// first screen for nothing. Its chunk (and its words, the login namespace) loads when it is drawn.
const Login = lazyPage(() => import("./views/Login").then((m) => m.Login));
const AgentsView = lazyPage(() => import("./views/agents/AgentsView").then((m) => m.AgentsView), { routes: ["agents"] });
const WorkspaceBoard = lazyPage(() => import("./views/WorkspaceBoard").then((m) => m.WorkspaceBoard), { routes: ["workspace-board"] });
const Machines = lazyPage(() => import("./views/Machines").then((m) => m.Machines), { routes: ["machines"] });
const TerminalPage = lazyPage(() => import("./views/terminal/TerminalPage").then((m) => m.TerminalPage), { routes: ["terminal"] });
const SubscriptionsPage = lazyPage(() => import("./views/subscriptions/SubscriptionsPage").then((m) => m.SubscriptionsPage), { routes: ["subscriptions"] });
const MachineDetail = lazyPage(() => import("./views/machines/MachineDetail").then((m) => m.MachineDetail), { routes: ["machine"] });
const SpacePage = lazyPage(() => import("./views/SpacePage").then((m) => m.SpacePage), { routes: ["space"] });
const Settings = lazyPage(() => import("./views/Settings").then((m) => m.Settings), { routes: ["settings"] });
const Health = lazyPage(() => import("./views/health/Health").then((m) => m.Health), { routes: ["health"] });
const History = lazyPage(() => import("./views/assistant/History").then((m) => m.History), { routes: ["history"] });
const Profile = lazyPage(() => import("./views/assistant/Profile").then((m) => m.Profile), { routes: ["profile"] });
const Routines = lazyPage(() => import("./views/assistant/Routines").then((m) => m.Routines), { routes: ["routines"] });
const Inbox = lazyPage(() => import("./views/inbox/Inbox").then((m) => m.Inbox), { routes: ["inbox"] });
const Messages = lazyPage(() => import("./views/messages/Messages").then((m) => m.Messages), { routes: ["messages"] });
const Knowledge = lazyPage(() => import("./views/knowledge/Knowledge").then((m) => m.Knowledge), { routes: ["kb"] });
const TodosView = lazyPage(() => import("./views/Todos").then((m) => m.TodosView), { routes: ["todos"] });
// Keep the shortcut palette warm. The new-agent dialog loads only when opened.
const Palette = lazyPage(() => import("./views/Palette").then((m) => m.Palette), { idle: true });
const NewAgent = lazyPage(() => import("./views/NewAgent").then((m) => m.NewAgent));

// A link with the hub's token (`?token=…` or `#token=…`) signs in without the form. The token leaves the address bar
// before anything else runs (history, bookmarks, a copied link), and is traded for this browser's own one.
const linked = typeof window === "undefined" ? null : takeToken(window.location.href);
if (linked) window.history.replaceState(window.history.state, "", linked.rest);

// Switching from another hub's page hands its list of hubs over in the fragment (`#hubs=`, lib/hubs.ts): take it into
// this browser's list and out of the address bar the same way.
const carried = typeof window === "undefined" ? null : takeHubs(window.location.href);
if (carried) {
  acceptHubs(carried.hubs, carried.removed);
  window.history.replaceState(window.history.state, "", carried.rest);
}

const workspaceActions = { onWorkspaceMenu: openWorktreeActions };
function openWorkspaceInTree(target: WorkspaceTarget) {
  selectWorkspace(target);
  setAgentGrouping("repo");
  navigate({ name: "agent-list" });
}

export function App() {
  const [authed, setAuthed] = useState(() => !linked?.token && Boolean(getToken()));
  const [joining, setJoining] = useState(() => Boolean(linked?.token));
  const [linkError, setLinkError] = useState("");

  useEffect(() => {
    if (!linked?.token) return;
    let live = true;
    // Already signed in here (the link opened again): keep this browser's token instead of minting another one.
    const signedIn = getToken() ? api("/machines").then(() => true, () => false) : Promise.resolve(false);
    signedIn
      .then((ok) => (ok ? undefined : pair(linked.token)))
      .then(() => live && setAuthed(true))
      .catch((err) => {
        if (!live) return;
        const msg = errorText(err);
        const invalid = (err instanceof ApiError && err.status === 401) || msg === "invalid token";
        setLinkError(invalid ? t("app.link_token_invalid") : msg);
        setAuthed(Boolean(getToken())); // still signed in from before: stay in
      })
      .finally(() => live && setJoining(false));
    return () => {
      live = false;
    };
  }, []);

  useEffect(() => {
    handleUnauthorized(() => {
      setToken("");
      reset();
      // say why the sign-in page is back: this browser's credential stopped working (revoked, or the hub forgot it)
      setLinkError(t("app.signed_out"));
      setAuthed(false);
    });
  }, []);

  useEffect(() => {
    if (!authed) return;
    connect();
    // the managers' roster from the start (docs/assistant/managers.md "Clients"): what goes by the default waits for it
    watchRoster();
    return () => disconnect();
  }, [authed]);

  return (
    <>
      {joining ? (
        <Joining />
      ) : authed ? (
        <Shell />
      ) : (
        <PageBoundary page="login">
          <Suspense fallback={<PageLoading />}>
            <Login error={linkError} onDone={() => setAuthed(true)} />
          </Suspense>
        </PageBoundary>
      )}
      <Toasts />
    </>
  );
}

// A page whose chunk is still on its way: shown only if that takes a moment, so a quick load does not flash it.
function PageLoading() {
  return (
    <div className="flex h-full items-center justify-center" style={{ animation: "fade-in 150ms ease-out 250ms both" }}>
      <Loading />
    </div>
  );
}

// A machine's sessions as a `/s/…` or `/t/…` page looks them up: the complete rows, then the summaries not among them.
function pageRows(s: State, mid: string): Session[] {
  const complete = s.sessions[mid] ?? [];
  const details = new Set(complete.map((row) => row.session_id));
  return [...complete, ...s.sessionSummaries.filter((row) => row.machine_id === mid && !details.has(row.session_id))];
}

/** What a `/s/…` or `/t/…` page opens, settled from its machine's rows. Only plain values: a push for another session on
 *  the machine leaves it equal (useStore's shallow compare), so the shell and everything it draws are left alone. */
interface PageTarget {
  /** How a `/s/…` link's id settled (lib/session-link.ts); undefined on a `/t/…` page. */
  link?: SessionLink["kind"];
  /** Where the link really goes, replacing it in place (Back doesn't come here again); "" when it stays. */
  to: string;
  /** The page's machine: a tab's panes are all on it. */
  mid: string;
  /** The conversation shown; undefined while there is none to show. */
  sid?: string;
}

function pageTarget(s: State, route: Route): PageTarget | undefined {
  if (route.name === "tab") {
    // the pane a tab opens on: the asked-for one, its session, or the tab's most urgent (lib/herdr-tree.ts)
    const pane = tabPagePane(resolveTab(pageRows(s, route.mid), route), route, s.loaded);
    return { link: undefined, to: "", mid: route.mid, sid: pane?.sid };
  }
  if (route.name !== "session") return undefined;
  const rows = pageRows(s, route.mid);
  // an old `/s/…` link of a pane in a tab becomes that tab's `/t/…`
  const tab = tabOfSessionLink(rows, route);
  // a `/s/…` link with only the front of an id (the 8-character ones shown everywhere, #2464): the whole id's link;
  // several sessions starting with it are listed to pick from (SessionChoices)
  const link = resolveSessionLink(rows, route.mid, route.sid, s.loaded);
  const resolved = tab ?? (link.kind === "prefix" ? sessionLinkRoute(rows, link.session, route.view) : undefined);
  const to = resolved ? routePath(withWorkView(resolved, inWorkView(route))) : "";
  const shown = link.kind === "exact" || link.kind === "unlisted";
  return { link: link.kind, to, mid: route.mid, sid: shown ? route.sid : undefined };
}

function Shell() {
  const route = useRoute();
  // an error report's breadcrumb: the kind of page only (its name), never the ids or the query in its address
  useEffect(() => navigationCrumb(route.name), [route.name]);
  const conn = useStore((s) => s.conn);
  // this hub goes on the list of hubs to switch between, under the name it gives itself
  const hubName = useStore((s) => s.hubName);
  useEffect(() => void rememberHub(here(), hubName), [hubName]);
  // the conversation on the page: a session's (`/s/…`), or the pane a tab (`/t/…`) opens on
  const target = useStore((s) => pageTarget(s, route));
  const loaded = useStore((s) => s.loaded);
  const tabLinkPath = target?.to ?? "";
  useEffect(() => {
    if (tabLinkPath) navigate(tabLinkPath, true);
  }, [tabLinkPath]);
  // a tab not among the page's rows (a paged hub lists only active and pinned ones): asked of the hub before it is gone
  const tabRoute = route.name === "tab" ? route : undefined;
  const lookup = useTabLookup(tabRoute, !!tabRoute && loaded && !target?.sid);
  const found = lookup.state === "found" ? { mid: lookup.mid, sid: lookup.sid } : undefined;
  const pane = target?.sid ? { mid: target.mid, sid: target.sid } : found;
  const launch = useStore((s) => (pane ? s.launches[pane.sid] : undefined));
  const starting = useStore((s) => (pane ? (s.sessions[pane.mid] ?? []).find((session) => session.session_id === pane.sid)?.starting : undefined));
  const shell = useRef<HTMLDivElement>(null);
  useKeyboardFrame(shell);
  // a tab opened without a pane settles on the one it picked: another pane needing you later does not swap the page
  const picked = route.name === "tab" && !route.sid ? pane?.sid : undefined;
  useEffect(() => {
    if (picked && route.name === "tab") navigate({ ...route, sid: picked }, true);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [picked]);
  const palette = usePalette();
  const layout = useLayout();
  const [newAgent, setNewAgent] = useState(false);
  const [preset, setPreset] = useState<NewAgentPreset | null>(null);
  useEffect(() => preloadPages(), []);
  useEffect(() => onOpenNewAgent((p) => {
    setPreset(p);
    setNewAgent(true);
  }), []);
  const openNewAgent = useCallback(() => setNewAgent(true), []);
  const closeNewAgent = useCallback(() => {
    setNewAgent(false);
    setPreset(null);
  }, []);
  const paletteOpened = useOpened(palette.open);
  const newAgentOpened = useOpened(newAgent);

  // ── the sidebar: a column on a wide screen, a drawer over the page on a narrow one (#1996) ──
  const narrow = useMedia(NARROW);
  const [drawer, setDrawer] = useState(false);
  const toggle = useCallback(() => setDrawer((d) => !d), []);
  const close = useCallback(() => setDrawer(false), []);
  // The scrim leaves the page once it has faded out (#2102): kept there at opacity 0, iOS took its colour (the page's
  // under 20% black) for the status bar strip of the installed app.
  const scrimShown = useLingers(drawer, SCRIM_FADE);
  const drawerBox = useRef<HTMLDivElement>(null);
  const sidebarId = useId();
  const scrim = useRef<HTMLDivElement>(null);
  const mainBox = useRef<HTMLElement>(null);
  const a11y = drawerA11y(narrow, drawer);
  // folded to its icons on a wide screen (⌘⌥B, kept in this browser); a narrow screen keeps the full drawer
  const rail = !narrow && sidebarRail(layout);
  useEffect(() => {
    if (!narrow) setDrawer(false);
  }, [narrow]);
  // dragged out and back with a finger anywhere on the page or the drawer (not on a full-screen side panel)
  useDrawerDrag({
    root: shell,
    drawer: drawerBox,
    scrim,
    open: drawer,
    setOpen: setDrawer,
    enabled: narrow,
    scope: (t) => [mainBox, drawerBox, scrim].some((r) => r.current?.contains(t)),
  });
  // Back closes it first
  useCloseOnBack(drawer, close, narrow);
  // Esc closes it (a menu or a dialog over it closes first); Tab stays in it, like a dialog's
  useEffect(() => {
    if (!a11y.modal) return;
    const key = (e: KeyboardEvent) => {
      if (document.querySelector("[role=menu]") || document.querySelectorAll("[role=dialog]").length > 1) return;
      if (e.key === "Tab" && drawerBox.current) {
        const list = focusables(drawerBox.current);
        const to = trapTab(list.length, list.indexOf(document.activeElement as HTMLElement), e.shiftKey);
        if (to === null) return;
        e.preventDefault();
        list[to]?.focus();
        return;
      }
      if (e.key !== "Escape") return;
      e.preventDefault();
      e.stopPropagation();
      close();
    };
    addEventListener("keydown", key, true);
    return () => removeEventListener("keydown", key, true);
  }, [a11y.modal, close]);
  // open, focus moves into it; shut, back to what opened it
  const opener = useRef<HTMLElement | null>(null);
  useEffect(() => {
    const box = drawerBox.current;
    if (!box || !narrow) return;
    if (drawer) {
      opener.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
      focusables(box)[0]?.focus({ preventScroll: true });
      return;
    }
    const back = focusBack(opener.current, document.activeElement, box.contains(document.activeElement), document.body);
    opener.current = null;
    back?.focus({ preventScroll: true });
  }, [drawer, narrow]);

  // the keyboard shortcuts (⌘K and the rest, lib/shortcuts.ts): what they do here, and the listener that runs them
  const workPane = useRef<{ mid: string; sid: string } | undefined>(undefined);
  const reportWorkPane = useCallback((available?: { mid: string; sid: string }) => { workPane.current = available; }, []);
  const workPaneAvailable = (at: { mid: string; sid: string }) => workPane.current?.mid === at.mid && workPane.current?.sid === at.sid;
  useShortcuts({ route, pane, narrow, toggleDrawer: toggle, closeDrawer: close, workPaneAvailable });

  useEffect(() => setDrawer(false), [route.name, pane ? pane.sid : route.name === "persona" ? route.sid : route.name === "agents" ? route.id : ""]);
  // a session's files / Git belong to that conversation: moving to another page closes them (a link and a task stay),
  // and coming back to it opens them again where they were (#5150, lib/panel-memory.ts); a Persona chat's panel too
  // (#6199)
  const chat = route.name === "persona" ? (route.sid ?? "") : undefined;
  // the conversation on the page goes first among the recently opened ones, ⌘K's first group (#2191): an Agent's, or a
  // Persona conversation (the main one when the page names none); any other page shows none
  const personaMain = useStore((s) => s.assistant?.main ?? "");
  const personaShown = route.name === "persona" ? (route.sid || personaMain) : "";
  const shownChat = pane ? agentChatKey(pane.mid, pane.sid) : personaShown ? personaChatKey(personaShown) : "";
  useEffect(() => noteChatShown(shownChat), [shownChat]);
  const at = pane ? `${pane.mid}/${pane.sid}` : chat !== undefined ? `persona/${chat}` : route.name;
  useSessionPanels(at, pane, chat);

  const extensionTitle = useExtensionPlace(route.name === "extension" ? route.extension : "", route.name === "extension" ? route.ui : "")?.title;
  const title =
    inWorkView(route) || route.name === "terminal" ? t("work-entry.title") : pane ? "Agent" : route.name === "persona" ? "Persona" : route.name === "persona-new" ? t("app.title_new_chat") : route.name === "history" ? t("app.title_history") : route.name === "health" ? t("app.title_health") : route.name === "profile" ? t("app.title_profile") : route.name === "inbox" ? t("app.title_inbox") : route.name === "messages" ? t("app.title_messages")
      : route.name === "request" ? t("request-page.title") : route.name === "routines" ? t("app.title_routines") : route.name === "kb" ? t("app.title_kb") : route.name === "workspace-board" ? t("app.title_workspace_board") : route.name === "todos" ? t("app.title_todos") : route.name === "machines" || route.name === "machine" ? t("app.title_machines") : route.name === "space" ? route.space : route.name === "settings" ? t("app.title_settings")
      : route.name === "agents" ? t("app.title_agents") : route.name === "agent-list" ? "Agents" : route.name === "agent-archives" ? t("app.title_agent_archives") : route.name === "extension" ? extensionTitle || t("app.title_extension") : route.name === "subscriptions" ? t("app.title_subscriptions") : t("app.title_home");

  // never reached the hub: say so with a way to try again, instead of pages loading forever (kept through a retry)
  const [away, setAway] = useState(false);
  const offline = unreachable(away, conn, loaded);
  if (offline !== away) setAway(offline);
  let main: React.ReactNode;
  if (offline) main = <Offline onToggleSidebar={toggle} trying={conn === "connecting"} />;
  else switch (route.name) {
    case "request":
      main = <RequestPage id={route.id} onToggleSidebar={toggle} />;
      break;
    case "session":
    case "tab": {
      const detail = (terminalAvailable?: boolean) =>
        route.name === "session" && target?.link === "ambiguous" ? <SessionChoices mid={route.mid} sid={route.sid} onToggleSidebar={toggle} />
          : !pane ? (loaded && route.name === "tab" && lookup.state === "missing" ? <TabGone onToggleSidebar={toggle} /> : <Loading />)
          : launch ? <StartingAgent launch={launch} onToggleSidebar={toggle} />
          : <ChatView key={`${pane.mid}/${pane.sid}`} mid={pane.mid} sid={pane.sid} starting={starting} onToggleSidebar={terminalAvailable === undefined ? toggle : undefined} inPage terminalAvailable={terminalAvailable} />;
      main = inWorkView(route)
        ? <TerminalPage onToggleSidebar={toggle} onNewAgent={openNewAgent} detail={{ selected: pane, render: detail }} onWorkPane={reportWorkPane} />
        : detail();
      break;
    }
    case "persona":
      main = <PersonaAssistant sid={route.sid} routed={route.mid || route.manager ? { mid: route.mid, manager: route.manager } : undefined} onToggleSidebar={toggle} />;
      break;
    case "persona-new":
      main = <PersonaAssistant fresh onToggleSidebar={toggle} />;
      break;
    case "history":
      main = <History origin={route.origin} onToggleSidebar={toggle} />;
      break;
    case "health":
      main = <Health key={route.sid ?? (route.fresh ? "new" : "")} sid={route.sid} fresh={route.fresh} onToggleSidebar={toggle} />;
      break;
    case "profile":
      main = <Profile onToggleSidebar={toggle} />;
      break;
    case "inbox":
      main = <Inbox id={route.id} manager={route.manager} onToggleSidebar={toggle} />;
      break;
    case "messages":
      main = <Messages id={route.id} onToggleSidebar={toggle} />;
      break;
    case "routines":
      main = <Routines onToggleSidebar={toggle} />;
      break;
    case "kb":
      main = <Knowledge kind={route.kind} id={route.id} onToggleSidebar={toggle} />;
      break;
    case "workspace-board":
      main = <WorkspaceBoard onToggleSidebar={toggle} />;
      break;
    case "todos":
      main = <TodosView selectedId={route.id} view={route.view} mode={route.mode} date={route.date} onToggleSidebar={toggle} />;
      break;
    case "agents":
      main = <AgentsView key={route.id ?? ""} id={route.id} onToggleSidebar={toggle} />;
      break;
    case "agent-list":
      main = <AgentsPage route={route} onToggleSidebar={toggle} onNewAgent={openNewAgent} workspaceActions={workspaceActions} />;
      break;
    case "agent-archives":
      main = <AgentArchives onToggleSidebar={toggle} />;
      break;
    case "machines":
      main = <Machines onToggleSidebar={toggle} />;
      break;
    case "terminal":
      main = <TerminalPage route={route} onToggleSidebar={toggle} onNewAgent={openNewAgent} />;
      break;
    case "machine":
      main = <MachineDetail key={route.mid} mid={route.mid} onToggleSidebar={toggle} />;
      break;
    case "space":
      main = <SpacePage key={`${route.mid}/${route.space}`} mid={route.mid} space={route.space} onToggleSidebar={toggle} />;
      break;
    case "settings":
      main = <Settings onToggleSidebar={toggle} />;
      break;
    case "subscriptions":
      main = <SubscriptionsPage onToggleSidebar={toggle} />;
      break;
    case "extension":
      main = <ExtensionNavPage key={`${route.extension}/${route.ui}`} extension={route.extension} ui={route.ui} onToggleSidebar={toggle} />;
      break;
    default:
      main = <Home onNewAgent={openNewAgent} onToggleSidebar={toggle} />;
  }

  return (
    // #root is 100dvh (index.html); with a soft keyboard up (iOS) the shell is only the part above it (lib/viewport.ts)
    <div ref={shell} className="flex h-full overflow-hidden bg-page">
      {/* wide: a fixed column; narrow: a drawer over the page, clear of a landscape notch and the home indicator */}
      <div
        ref={drawerBox}
        id={sidebarId}
        role={a11y.modal ? "dialog" : undefined}
        aria-modal={a11y.modal || undefined}
        aria-label={a11y.modal ? SIDEBAR_LABEL : undefined}
        inert={a11y.inert || undefined}
        className={cx(
          "fixed inset-y-0 left-0 z-30 w-[284px] border-r border-line transition-transform duration-300 max-lg:w-[calc(284px+env(safe-area-inset-left))] max-lg:bg-canvas max-lg:pb-[env(safe-area-inset-bottom)] max-lg:pl-[env(safe-area-inset-left)] lg:relative lg:z-auto lg:translate-x-0 lg:shadow-none",
          drawer ? "translate-x-0 shadow-overlay" : "-translate-x-full",
          // ⌘⌥B folds it to the rail, ⌘⇧↵ hides it, on a wide screen; unfolded, its right edge drags (SidebarResize)
          rail ? "lg:w-14" : "lg:w-[var(--sidebar-w,284px)]",
          !sidebarShown(layout) && "lg:hidden",
        )}
        style={{ transitionTimingFunction: EASE }}
      >
        {rail ? <SidebarRail route={route} onSearch={() => openPalette("jump")} onNewAgent={openNewAgent} /> : <Sidebar route={route} onSearch={() => openPalette("jump")} onNewAgent={openNewAgent} onNavigate={close} workspaceActions={workspaceActions} />}
        {!narrow && !rail && <SidebarResize column={sidebarId} />}
      </div>
      {/* the scrim fades out as well as in; a drag sets its opacity as it goes (lib/drawer.ts) */}
      <div
        ref={scrim}
        aria-hidden
        className={cx("fixed inset-0 z-20 bg-black/20 transition-opacity duration-200 lg:hidden", drawer ? "opacity-100 starting:opacity-0" : "pointer-events-none opacity-0", !scrimShown && "hidden")}
        onClick={close}
      />
      <main ref={mainBox} inert={a11y.modal || undefined} className="relative min-w-0 flex-1">
        {/* dropped after loading: which phase, and a click explains with "Retry now" (views/ConnectionPill.tsx); under the page's
            header, which a page taller than 56px reports as --page-header (Settings, #3594) */}
        {conn !== "online" && loaded && !offline && (
          <div className="pointer-events-none absolute inset-x-0 top-[var(--page-header,3.5rem)] z-10 flex justify-center px-4 pt-2" style={{ animation: `slide-up 220ms ${EASE} both` }}>
            <ConnectionPill />
          </div>
        )}
        <PageBoundary page={href(route)}><Suspense fallback={<PageLoading />}><PerAssistant route={route}>{main}</PerAssistant></Suspense></PageBoundary>
      </main>
      <SidePanel />
      {paletteOpened && (
        <OverlayBoundary open={palette.open} onClose={closePalette}>
          <Suspense fallback={null}>
            <Palette open={palette.open} onClose={closePalette} onNewAgent={openNewAgent} pane={pane} route={route} />
          </Suspense>
        </OverlayBoundary>
      )}
      <ShortcutHelp />
      {newAgentOpened && (
        <OverlayBoundary open={newAgent} onClose={closeNewAgent}>
          <Suspense fallback={null}>
            <NewAgent open={newAgent} preset={preset} onClose={closeNewAgent} />
          </Suspense>
        </OverlayBoundary>
      )}
      <NewWorktreeHost />
      <ForwardHost />
      <WorktreeActionsHost onOpenWorkspace={openWorkspaceInTree} />
      {/* tab title with the "Needs you" count, favicon badge, optional sounds (#2332): a leaf, so a sessions push re-renders
          only it, not the whole shell */}
      <AttentionSignals label={title} pane={pane} />
    </div>
  );
}

// The hub was never reached (offline, or it is down): an empty state that says so and tries again on a tap, instead of
// every page loading forever. The socket keeps retrying on its own as well; once in, the page comes back by itself.
function Offline({ onToggleSidebar, trying }: { onToggleSidebar: () => void; trying: boolean }) {
  return (
    <div className="flex h-full flex-col">
      <header className="flex h-14 shrink-0 items-center px-3 sm:px-4">
        <IconButton icon="sidebar" label={t("app.sidebar")} onClick={onToggleSidebar} className="lg:hidden" />
      </header>
      <div className="flex min-h-0 flex-1 flex-col pb-[10vh]" role="status">
        <Empty
          icon="hub"
          title={t("app.offline_title")}
          detail={t("app.offline_detail")}
          action={<Button tone="secondary" icon="retry" busy={trying} onClick={connect}>{trying ? t("app.connecting") : t("app.retry")}</Button>}
        />
      </div>
    </div>
  );
}

// A tab link whose tab is gone (closed, or herdr restarted with new ids) and no pane to fall back to.
function TabGone({ onToggleSidebar }: { onToggleSidebar: () => void }) {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-3 px-6 text-center">
      <p className="text-[13px] text-ink-2">{t("app.tab_gone")}</p>
      <button type="button" onClick={onToggleSidebar} className="whitespace-nowrap rounded-control px-2.5 py-1.5 text-[12.5px] font-medium text-accent-ink hover:bg-hover lg:hidden">{t("app.open_sidebar")}</button>
    </div>
  );
}

// A `/s/…` link whose id prefix more than one session on the machine starts with (#2464): pick one, none is guessed.
function SessionChoices({ mid, sid, onToggleSidebar }: { mid: string; sid: string; onToggleSidebar: () => void }) {
  // read here, not in the shell: the list keeps up with the machine's pushes without drawing the whole app again
  const all = useStore((s) => pageRows(s, mid));
  const loaded = useStore((s) => s.loaded);
  const link = resolveSessionLink(all, mid, sid, loaded);
  const candidates = link.kind === "ambiguous" ? link.candidates : [];
  return (
    <div className="flex h-full flex-col items-center justify-center gap-3 px-6 text-center">
      <p className="text-[13px] text-ink-2">{t("app.session_choices", { count: candidates.length, sid })}</p>
      <ul className="flex w-full max-w-96 flex-col gap-1">
        {candidates.map((s) => {
          const to = sessionLinkRoute(all, s);
          return (
            <li key={s.session_id}>
              <a href={href(to)} onClick={(e) => onLinkClick(e, to)} className="flex flex-col items-start gap-0.5 rounded-control px-3 py-2 text-left hover:bg-hover">
                <span className="w-full truncate text-[13px] font-medium text-ink">{sessionTitle(s)}</span>
                <span className="w-full truncate font-mono text-[11.5px] text-ink-3">{s.session_id}</span>
                {(s.cwd || s.activity_at || s.updated_at) && (
                  <span className="w-full truncate text-[11.5px] text-ink-3">{[shortPath(s.cwd), ago(s.activity_at || s.updated_at)].filter(Boolean).join(" · ")}</span>
                )}
              </a>
            </li>
          );
        })}
      </ul>
      <button type="button" onClick={onToggleSidebar} className="whitespace-nowrap rounded-control px-2.5 py-1.5 text-[12.5px] font-medium text-accent-ink hover:bg-hover lg:hidden">{t("app.open_sidebar")}</button>
    </div>
  );
}

// Signing in with the link's token: a moment, no form.
function Joining() {
  return (
    <div className="flex min-h-full items-center justify-center bg-page" role="status" aria-live="polite">
      <span className="flex items-center gap-2 text-[13px] text-ink-2">
        <Spinner />
        {t("app.joining")}
      </span>
    </div>
  );
}
