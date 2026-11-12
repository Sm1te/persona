// The right-hand panel next to the page (docs/web/web.md "Side panel"): one at a time, chosen in lib/panel.ts. (A session's
// terminal is not one: it is the agent page's own view, lib/session-view.ts.)
// Wide screens (lg+) get a resizable column that pushes the page; narrower ones an overlay over the page.
import { lazy, Suspense, useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import { directoryPanelKey, closePanel, panelOpener, usePanel, type Panel } from "../lib/panel";
import { activeComposer } from "../lib/links";
import { returnFocus } from "../ui/overlay";
import { CHAT_MIN, PANEL_MIN, useLayout } from "../lib/layout";
import { cx, EASE, Loading } from "../ui/atoms";
import { ResizeHandle } from "../ui/ResizeHandle";
import { PlanPanel } from "./chat/PlanPanel";
import { useCloseOnBack } from "../lib/back-close";
import { mediaMatches, NARROW, PHONE, useMedia } from "../lib/viewport";
import { t } from "../i18n";
import { PageBoundary } from "./PageBoundary";

// Files, Git, the file viewer and a link load when one is first opened (#2673): with their viewers (sheets, Office,
// compare, documents) they are a large part of the main bundle no first screen needs. The assistant's task / agent
// panels stay static: small, and part of the assistant flow.
const FilesPanel = lazy(() => import("./session/FilesPanel").then((m) => ({ default: m.FilesPanel })));
const FileViewerPanel = lazy(() => import("./session/FileViewer").then((m) => ({ default: m.FileViewerPanel })));
const GitPanel = lazy(() => import("./session/GitPanel").then((m) => ({ default: m.GitPanel })));
const PreviewPanel = lazy(() => import("./session/PreviewPanel").then((m) => ({ default: m.PreviewPanel })));
const ReviewPanel = lazy(() => import("./session/ReviewPanel").then((m) => ({ default: m.ReviewPanel })));
const LinkPanel = lazy(() => import("./link/LinkPanel").then((m) => ({ default: m.LinkPanel })));

const WIDTH_KEY = "persona.panelWidth";
const DEFAULT_WIDTH = 600;
// the panel's least and the conversation's least beside it live with the shell's other widths (lib/layout.ts)
export { CHAT_MIN };

/**
 * The width the panel is drawn at: the one asked for (dragged, or remembered), within what leaves the page beside it
 * CHAT_MIN. `room` is the page's width plus the panel's, the part of the shell the two share (the sidebar's is not in
 * it), so a folded or hidden sidebar gives the panel more. Never under PANEL_MIN.
 */
export function panelWidth(want: number, room: number) {
  return Math.round(Math.max(PANEL_MIN, Math.min(want, room - CHAT_MIN)));
}

export function SidePanel() {
  const panel = usePanel();
  // ⌘⇧↵ shows only the middle: the panel stays open (its terminal keeps running) but is not drawn, on a wide screen
  const maximized = useLayout().maximized;
  // narrow, it covers the page: Back closes it first (and stays on the conversation)
  useCloseOnBack(Boolean(panel), closePanel, useMedia(NARROW));
  // the width asked for; what is drawn is `shown`, which also fits the room there is now (the window, the sidebar).
  // Until the room is measured (or with no page beside it), the old cap: 72% of the window.
  const [width, setWidth] = useState(() => Math.max(PANEL_MIN, Number(localStorage.getItem(WIDTH_KEY)) || DEFAULT_WIDTH));
  const [room, setRoom] = useState(() => innerWidth * 0.72 + CHAT_MIN);
  const shown = panelWidth(width, room);
  const id = useId();
  const aside = useRef<HTMLElement>(null);
  const open = Boolean(panel);
  // Closed (×, Esc, its button again): the focus goes back to the button that opened it, else to the message box, not
  // to the page (#5155). Not the message box on a phone: that would bring the keyboard up.
  useEffect(() => {
    const el = aside.current;
    if (!open || !el) return;
    return () => returnFocus(el, panelOpener(), mediaMatches(PHONE) ? undefined : () => activeComposer()?.focus());
  }, [open]);

  // The page (<main>, the panel's sibling, App.tsx) and the panel share the room: measure it before paint, and again
  // whenever the page's width changes (window resized, sidebar folded / hidden / back). Their sum does not change with
  // the panel's own width, so this does not feed back on itself.
  useLayoutEffect(() => {
    const el = aside.current;
    const main = el?.parentElement?.querySelector<HTMLElement>(":scope > main");
    if (!el || !main) return;
    const measure = () => setRoom(main.getBoundingClientRect().width + el.getBoundingClientRect().width);
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(measure);
    ro.observe(main);
    return () => ro.disconnect();
  }, [open]);

  if (!panel) return null;
  const resize = (w: number) => {
    const next = panelWidth(w, room);
    setWidth(next);
    localStorage.setItem(WIDTH_KEY, String(next));
  };
  const reset = () => {
    setWidth(DEFAULT_WIDTH);
    localStorage.removeItem(WIDTH_KEY);
  };
  return (
    <aside
      ref={aside}
      id={id}
      aria-label={panelLabel(panel)}
      onKeyDown={(e) => {
        // Esc closes the panel from inside it, unless something in it (a field, a menu) or a dialog over it took the key
        if (e.key !== "Escape" || e.defaultPrevented || e.nativeEvent.isComposing) return;
        if (document.querySelector("[role=menu],[role=dialog]")) return;
        e.preventDefault();
        // not also the conversation's Esc (it stops a running agent, ChatView)
        e.stopPropagation();
        // the focus goes back to the panel's button, or the message box (the close effect above, #5155)
        closePanel();
      }}
      // narrow: over the visible part of the page (--kb-top / --kb-h, lib/viewport.ts), so on iOS the terminal's key
      // bar sits on the keyboard instead of under it
      className={cx(
        "fixed inset-x-0 top-(--kb-top) z-30 flex h-(--kb-h) flex-col bg-page shadow-overlay lg:relative lg:inset-auto lg:top-auto lg:z-auto lg:h-auto lg:w-(--panel-w) lg:shrink-0 lg:border-l lg:border-line lg:shadow-none",
        maximized && "lg:hidden",
      )}
      style={{ "--panel-w": `${shown}px`, animation: `slide-in-right 260ms ${EASE} both` } as React.CSSProperties}
    >
      {/* the panel is on the right: dragging its left edge left (or ArrowLeft) widens it */}
      <ResizeHandle
        edge="left"
        value={shown}
        min={PANEL_MIN}
        max={panelWidth(Infinity, room)}
        onResize={setWidth}
        onCommit={resize}
        onReset={reset}
        label={t("sidepanel.resize_label")}
        title={t("sidepanel.resize_title")}
        controls={id}
        className="hidden lg:block"
      />
      <div className="flex min-h-0 flex-1 flex-col">
        {/* a chunk gone after a hub redeploy reloads once, else shows a retry; never a blank panel */}
        <PageBoundary page={JSON.stringify(panel)}>
          <Suspense fallback={<div className="flex flex-1 items-center justify-center"><Loading /></div>}>{body(panel)}</Suspense>
        </PageBoundary>
      </div>
    </aside>
  );
}

function panelLabel(p: Panel) {
  return p.kind === "files" ? t("sidepanel.files") : p.kind === "git" ? "Git" : p.kind === "review" ? t("sidepanel.review") : p.kind === "file" ? t("sidepanel.files") : p.kind === "plan" ? t("sidepanel.plan") : p.kind === "preview" ? t("sidepanel.preview") : t("sidepanel.link");
}

function body(p: Panel) {
  switch (p.kind) {
    case "files":
      return <FilesPanel key={directoryPanelKey(p)} mid={p.mid} sid={p.sid} cwd={p.cwd} path={p.path} file={p.file} onClose={closePanel} />;
    case "git":
      return <GitPanel key={directoryPanelKey(p)} mid={p.mid} sid={p.sid} cwd={p.cwd} at={p.at} onClose={closePanel} />;
    case "review":
      return <ReviewPanel key={directoryPanelKey(p)} mid={p.mid} sid={p.sid} cwd={p.cwd} scope={p.scope} at={p.at} onClose={closePanel} />;
    case "file":
      return <FileViewerPanel key={`${p.mid}:${p.path}:${p.line ?? ""}-${p.to ?? ""}`} mid={p.mid} sid={p.sid} path={p.path} cwd={p.cwd} line={p.line} to={p.to} onClose={closePanel} />;
    case "link":
      return <LinkPanel key={`${p.manager ?? ""} ${p.url}`} url={p.url} title={p.title} manager={p.manager} onClose={closePanel} />;
    case "plan":
      return <PlanPanel key={`${p.mid}/${p.sid}`} mid={p.mid} sid={p.sid} onClose={closePanel} />;
    case "preview":
      return <PreviewPanel key={`${p.mid}/${p.sid}/${p.port ?? ""}${p.path ?? ""}`} mid={p.mid} sid={p.sid} cwd={p.cwd} port={p.port} path={p.path} onClose={closePanel} />;
  }
}
