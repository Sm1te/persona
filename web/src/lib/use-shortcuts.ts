// The shell's side of the keyboard shortcuts (lib/shortcuts.ts, docs/web/web.md "Keyboard shortcuts"): what each command does here,
// and the one keydown listener that dispatches them. Mounted once, by the shell.
import { t } from "../i18n";
import { useEffect, useRef } from "react";
import { toast } from "../ui/atoms";
import { AGENT_GROUPINGS } from "./agent-groups";
import { directionTreeGroups } from "./direction-tree";
import { adjacentTab, machineAt, panelToggle, terminalChat, type PaneRef } from "./shortcut-nav";
import { toggleHelp } from "./shortcut-help";
import { toggleMaximized, toggleSidebarColumn } from "./layout";
import { togglePalette } from "./palette";
import { closePanel, getPanel, openPanel, type Panel } from "./panel";
import { inWorkView, navigate, withWorkView, type Route } from "./router";
import { canShowTerminal, readView, rememberView, resolveView } from "./session-view";
import { cycleGrouping, groupingLabel, setAllFolded, stepSidebarRow } from "./sidebar-api";
import { focusWaitingCard } from "./card-keys";
import { dispatchKey, registerShortcut } from "./shortcuts";
import { getSession, getState } from "./store";

export interface ShortcutContext {
  route: Route;
  /** The conversation on the page. */
  pane?: PaneRef;
  workPaneAvailable?: (pane: PaneRef) => boolean;
  /** A phone-width screen: the sidebar is a drawer, there is no column to hide. */
  narrow: boolean;
  toggleDrawer: () => void;
  /** Closes the drawer (a no-op when it is shut), as a tap on one of its rows does. */
  closeDrawer?: () => void;
}

/** The panel ⌘B closed last, to bring back. */
let lastPanel: Panel | null = null;

/** The narrow layout's sidebar drawer's label (App.tsx): a modal dialog the page's shortcuts don't stand aside for. */
export const SIDEBAR_LABEL = t("lib-use-shortcuts.sidebar");

/** A dialog or the palette is open: the shortcuts that act on the page stand aside. */
export function overlayOpen(doc: Pick<Document, "querySelector"> = document): boolean {
  return Boolean(doc.querySelector(`[data-shortcut-layer], [role="dialog"][aria-modal="true"]:not([aria-label=${JSON.stringify(SIDEBAR_LABEL)}])`));
}

const visible = (el: Element) => el.getClientRects().length > 0;

/** ⌘I: the conversation's input box, else the terminal that is showing. */
function focusInput(route: Route): boolean {
  const chat = ["session", "tab", "persona", "persona-new", "agents"].includes(route.name);
  const box = chat ? [...document.querySelectorAll<HTMLTextAreaElement>("main textarea:not(.xterm-helper-textarea)")].find((t) => !t.disabled && visible(t)) : undefined;
  const el = box ?? [...document.querySelectorAll<HTMLTextAreaElement>(".xterm-helper-textarea")].find((t) => visible(t.closest(".xterm") ?? t));
  if (!el) return false;
  el.focus();
  return true;
}

/** Registers the commands while the shell is mounted, and listens for the keys that run them. */
export function useShortcuts(ctx: ShortcutContext) {
  const at = useRef(ctx);
  at.current = ctx;

  useEffect(() => {
    const off = [
      registerShortcut("jump", () => togglePalette("jump")),
      registerShortcut("commands", () => togglePalette("command")),
      registerShortcut("help", () => toggleHelp()),
      registerShortcut("settings", () => navigate({ name: "settings" })),
      // what a click on the sidebar's Persona row does: the main conversation (#5581)
      registerShortcut("persona", () => {
        navigate({ name: "persona" });
        // a route change closes the drawer (App.tsx); already on Persona it would stay open over it
        if (at.current.narrow) at.current.closeDrawer?.();
      }),
      registerShortcut("machine", ({ digit }) => {
        const m = machineAt(getState().machines, digit ?? 0);
        if (!m) return false;
        navigate({ name: "machine", mid: m.id });
      }),
      registerShortcut("left-sidebar", () => {
        if (at.current.narrow) at.current.toggleDrawer();
        else toggleSidebarColumn();
      }),
      registerShortcut("maximize", () => (at.current.narrow ? false : void toggleMaximized())),
      registerShortcut("terminal-chat", () => {
        const { route, pane, workPaneAvailable } = at.current;
        const inWork = inWorkView(route);
        const can = !!pane && canShowTerminal(getSession(pane.mid, pane.sid)) && (!inWork || workPaneAvailable?.(pane) === true);
        const url = route.name === "session" || route.name === "tab" ? route.view : undefined;
        const to = terminalChat(route, pane ? resolveView(url, readView(pane.mid, pane.sid), can) : "chat", can);
        if (!to) return false;
        if (inWork && pane && (to.go.name === "session" || to.go.name === "tab") && to.go.view) rememberView(pane.mid, pane.sid, to.go.view);
        // on an agent's page the view changes in place: Back leaves the page, it does not undo each ⌘J
        navigate(to.go, route.name === "session" || route.name === "tab");
      }),
      registerShortcut("right-panel", () => {
        const current = getPanel();
        const pane = at.current.pane;
        const to = panelToggle(current, lastPanel, pane, pane ? getSession(pane.mid, pane.sid)?.cwd : undefined);
        if (!to) return false;
        if ("close" in to) {
          lastPanel = current;
          closePanel();
        } else openPanel(to.open);
      }),
      registerShortcut("focus-input", () => focusInput(at.current.route)),
      // the card then answers on its own keys (lib/card-keys.ts, Interactions.tsx)
      registerShortcut("focus-card", () => focusWaitingCard()),
      registerShortcut("prev-tab", () => step(-1)),
      registerShortcut("next-tab", () => step(1)),
      registerShortcut("sidebar-grouping", () => {
        const offered = AGENT_GROUPINGS.map((g) => g.key).filter((k) => (getState().todosSupported !== false || k !== "todo") && (k !== "direction" || directionTreeGroups()));
        toast(t("lib-use-shortcuts.grouping_toast", { grouping: groupingLabel(cycleGrouping(offered)) }));
      }),
      registerShortcut("prev-row", () => stepSidebarRow(-1)),
      registerShortcut("next-row", () => stepSidebarRow(1)),
      registerShortcut("collapse-tree", () => setAllFolded(true)),
      registerShortcut("expand-tree", () => setAllFolded(false)),
    ];
    function step(dir: 1 | -1): boolean | void {
      const s = getState();
      const to = adjacentTab(s.machines, Object.values(s.sessions).flat(), at.current.pane, dir);
      if (!to) return false;
      navigate(withWorkView(to, inWorkView(at.current.route)));
    }
    const key = (e: KeyboardEvent) => void dispatchKey(e, { overlay: overlayOpen() });
    addEventListener("keydown", key);
    return () => {
      removeEventListener("keydown", key);
      off.forEach((f) => f());
    };
  }, []);
}
