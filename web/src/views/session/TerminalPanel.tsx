// A session's terminal in the side panel (docs/integrations/terminal.md, docs/web/web.md "Side panel"), as iOS's terminal: the pane live
// over the hub's stream (polling the screen where it cannot stream), driven at the panel's size ("Take control") or watched at the
// pane's ("View only"). Watching, scrolling up reads herdr's history above the screen and "Wrap" folds long rows at the panel's
// width; driving, the key bar has the keys a phone's keyboard lacks. The logic is in terminal/feed.ts; xterm loads only
// when a terminal opens.
//
// Beside that single-pane surface the panel has a second, "Full view" (docs/integrations/terminal.md "Full herdr UI"): herdr's own
// client with its sidebar, tabs and splits (terminal/FullTerminal.tsx). The header's switch chooses, and the two give
// way to each other with a short slide and fade. Each surface draws its own controls into the header's slot.
import { useCallback, useEffect, useLayoutEffect, useReducer, useRef, useState, useSyncExternalStore } from "react";
import { createPortal } from "react-dom";
import { resumeAgent, useResume } from "../../lib/agent-resume";
import { isStoppedAgent, sessionTitle, stateOf } from "../../lib/format";
import { machineWithHub } from "../../lib/mesh";
import { registerShortcut } from "../../lib/shortcuts";
import { getSession, useStore } from "../../lib/store";
import { lineCount } from "../../lib/terminal";
import { isPaneAddress } from "../../lib/terminals";
import { shouldRefocus, terminalMenu, wantsOwnMenu, type TerminalMenuKey } from "../../lib/terminal-details";
import { FIND_CLOSED, findReducer, isFindShortcut, queryFromSelection } from "../../lib/terminal-search";
import {
  LAST_MODE_KEY,
  PREFERENCE_KEY,
  SURFACE_MS,
  inHerdrClient,
  noFullClient,
  otherMode,
  pickMode,
  parseMode,
  parsePreference,
  slideFrom,
  surfacesReducer,
  type TerminalMode,
} from "../../lib/terminal-mode";
import { Button, cx, Dialog, IconButton, Loading, Menu, Spinner, toast, type MenuItem } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { prefersReducedMotion } from "../../ui/stream/motion";
import { PanelHeader } from "../../ui/PanelHeader";
import { DrawnRows, rowsStyle } from "./terminal/DrawnRows";
import { TerminalFeed, type FeedView } from "./terminal/feed";
import { FindBar } from "./terminal/FindBar";
import { focusFacts, selectionInside } from "./terminal/focus";
import { FullTerminal, useUnseen } from "./terminal/FullTerminal";
import { KeyBar } from "./terminal/KeyBar";
import { KEYBAR_KEY, savedKeyBar } from "./terminal/keybar-pref";
import { LinkStatus } from "./terminal/LinkStatus";
import { ModeToggle } from "./terminal/ModeToggle";
import { ShortcutsEditor } from "./terminal/Shortcuts";
import { Surface } from "./terminal/Surface";
import { TerminalOptions } from "./terminal/TerminalOptions";
import { UploadStatus } from "./terminal/UploadStatus";
import { t } from "../../i18n";
import { SegmentedTrack } from "../../ui/Segmented";

const nothing = () => () => {};

function useFeed(feed: TerminalFeed | null): FeedView | null {
  return useSyncExternalStore(feed?.subscribe ?? nothing, () => feed?.snapshot() ?? null);
}

/** The single-pane surface: the panel as it has always been, less its header (the panel draws that; this draws its controls into `slot`). */
function PaneTerminal({
  mid,
  sid,
  cwd,
  onFiles,
  slot,
  live,
  resumable,
  onOffline,
}: {
  mid: string;
  sid: string;
  cwd?: string;
  onFiles?: () => void;
  slot: HTMLElement | null;
  /** False while this surface is on its way out. */
  live: boolean;
  /** The panel offers Resume for this Agent (#4754): its own line says why the terminal is not running, not the link's. */
  resumable?: boolean;
  /** Whether the stream says the session is not running (the panel's Resume line says the terminal is gone). */
  onOffline?: (offline: boolean) => void;
}) {
  const session = useStore(() => getSession(mid, sid));
  const canDrive = useStore((s) => s.features.includes("terminal_control"));
  const host = useRef<HTMLDivElement>(null);
  const viewport = useRef<HTMLDivElement>(null);
  const [feed, setFeed] = useState<TerminalFeed | null>(null);
  const v = useFeed(feed);
  const [keyBar, setKeyBar] = useState(savedKeyBar);
  const [options, setOptions] = useState<HTMLElement | null>(null);
  const [editing, setEditing] = useState(false);
  const root = useRef<HTMLDivElement>(null);
  const [find, dispatchFind] = useReducer(findReducer, FIND_CLOSED);
  const [menu, setMenu] = useState<{ x: number; y: number; selection: boolean } | null>(null);
  const atBottom = useRef(true);
  const lastTop = useRef(0);
  const lastHeight = useRef(0);
  const lastOlder = useRef(0);

  useEffect(() => {
    const f = new TerminalFeed(mid, sid);
    setFeed(f);
    void f.mount(host.current!, viewport.current!);
    return () => f.dispose();
  }, [mid, sid]);

  const dir = cwd || session?.cwd || "";
  useEffect(() => {
    if (feed) feed.cwd = dir;
  }, [feed, dir]);

  // Resume (#4754): once the Agent is back (the hub answered, or its row no longer says stopped) the stream is asked for
  // again at once instead of after the back-off.
  const offline = Boolean(v?.offline);
  useEffect(() => onOffline?.(offline), [offline, onOffline]);
  const resumedAt = useResume(mid, sid);
  const back = resumedAt.phase === "done" ? resumedAt.at : 0;
  const stopped = isStoppedAgent(session);
  // read when they change, not on the feed's arrival: its first subscribe is its own
  const feedNow = useRef(feed);
  feedNow.current = feed;
  useEffect(() => {
    if (back || !stopped) feedNow.current?.retry();
  }, [back, stopped]);

  const streaming = v?.mode === "stream";
  const takeBack = Boolean(v && streaming && v.wants && v.control === "declined" && v.why !== "unsupported");
  const driving = v?.control === "active" || v?.control === "starting";
  const drawn = Boolean(v?.screen);
  const older = v?.older ?? [];
  const showKeyBar = Boolean(feed && v && keyBar && streaming && canDrive && v.wants);

  // A page of history placed above keeps what is on screen where it was; the drawn screen follows its end unless the
  // user scrolled up.
  useLayoutEffect(() => {
    const el = viewport.current;
    if (!el) return;
    if (older.length > lastOlder.current) el.scrollTop += el.scrollHeight - lastHeight.current;
    else if (drawn && atBottom.current && !v?.paused) el.scrollTop = el.scrollHeight;
    lastOlder.current = older.length;
    lastHeight.current = el.scrollHeight;
    lastTop.current = el.scrollTop;
  }, [older, drawn, v?.screen, v?.paused]);

  const nearTop = () => {
    if (feed && v && !driving && v.loaded && v.hasOlder && !v.loadingOlder && !v.offline) void feed.loadOlder();
  };
  const onScroll = (e: React.UIEvent<HTMLDivElement>) => {
    const el = e.currentTarget;
    atBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < 8;
    if (el.scrollTop < lastTop.current && el.scrollTop < 24) nearTop(); // scrolled up to the top
    lastTop.current = el.scrollTop;
  };
  const onWheel = (e: React.WheelEvent<HTMLDivElement>) => {
    if (e.deltaY < 0 && e.currentTarget.scrollTop <= 0) nearTop(); // nothing to scroll: the wheel still asks for more
  };
  const backToLatest = () => {
    atBottom.current = true;
    feed?.resume();
  };
  const chooseKeyBar = (on: boolean) => {
    localStorage.setItem(KEYBAR_KEY, on ? "1" : "0");
    setKeyBar(on);
  };

  // ── focus: driving, the keyboard goes back to the terminal after a click in the panel and when the window returns ──
  const refocus = useCallback(() => {
    const el = root.current;
    if (el && feed && shouldRefocus(focusFacts(el, feed.driving, feed.hasSelection()))) feed.focus();
  }, [feed]);
  useEffect(() => {
    addEventListener("focus", refocus);
    return () => removeEventListener("focus", refocus);
  }, [refocus]);

  // ── find (lib/terminal-search.ts): ⌘F / Ctrl+Shift+F in the panel, or "Find…" in the right-click menu ──
  const openFind = () => {
    const picked = feed?.selectedText() || (viewport.current && selectionInside(viewport.current) ? (window.getSelection()?.toString() ?? "") : "");
    dispatchFind({ type: "open", query: queryFromSelection(picked) ?? undefined });
  };
  const closeFind = () => {
    dispatchFind({ type: "close" });
    feed?.clearFind();
    if (feed?.driving) feed.focus();
  };
  const onKeyDownCapture = (e: React.KeyboardEvent) => {
    if (!isFindShortcut(e.nativeEvent, { mac: /Mac|iPhone|iPad/.test(navigator.platform), driving: Boolean(feed?.driving) })) return;
    e.preventDefault(); // not the browser's own find
    e.stopPropagation(); // nor xterm's: the program does not get the key
    openFind();
  };
  // The current match is brought into view: a marked row of the page's, or xterm's screen under the history.
  const current = v?.marks?.current ?? null;
  useEffect(() => {
    if (current === null) return;
    viewport.current?.querySelector('[data-find="current"]')?.scrollIntoView({ block: "center" });
  }, [current]);
  const screenMatch = v?.find && v.find.index >= 0 && current === null ? v.find.index : -1;
  useEffect(() => {
    if (screenMatch >= 0) host.current?.scrollIntoView({ block: "nearest" });
  }, [screenMatch]);

  // ── right-click menu ──
  const onContextMenu = (e: React.MouseEvent) => {
    const pointerType = (e.nativeEvent as PointerEvent).pointerType;
    if (!(feed ? feed.wantsMenu(e.nativeEvent, pointerType) : wantsOwnMenu({ shiftKey: e.shiftKey, pointerType }))) return; // Shift / the mouse mode decide while the mouse goes to the program
    e.preventDefault();
    const own = Boolean(viewport.current && selectionInside(viewport.current));
    // xterm stops the keys it has focus for (Esc would go to the program): the menu takes them while it is open
    (document.activeElement as HTMLElement | null)?.blur();
    setMenu({ x: e.clientX, y: e.clientY, selection: Boolean(feed?.hasSelection()) || own });
  };
  const closeMenu = useCallback(() => setMenu(null), []);
  const menuItems = (): MenuItem[] => {
    const icons: Record<TerminalMenuKey, MenuItem["icon"]> = { copy: "copy", paste: "paste", selectAll: "list", search: "search", clearSelection: "x" };
    const run: Record<TerminalMenuKey, () => void> = {
      copy: () => {
        if (feed?.hasSelection()) feed.copySelection();
        else feed?.copyText(window.getSelection()?.toString() ?? "");
      },
      paste: () => void feed?.pasteClipboard(),
      selectAll: () => {
        if (drawn && viewport.current) window.getSelection()?.selectAllChildren(viewport.current);
        else feed?.selectAll();
      },
      search: openFind,
      clearSelection: () => {
        feed?.clearSelection();
        window.getSelection()?.removeAllRanges();
      },
    };
    return terminalMenu({ selection: menu?.selection ?? false, driving }).map((m) => ({
      key: m.key,
      label: m.label,
      icon: icons[m.key],
      disabled: m.disabled || !feed,
      divider: m.divider,
      onSelect: run[m.key],
    }));
  };

  return (
    <div
      ref={root}
      onKeyDownCapture={onKeyDownCapture}
      onClick={(e) => !(e.target as Element).closest('[role="search"]') && setTimeout(refocus, 0)} // the find bar keeps its own focus
      tabIndex={-1} // a click anywhere in the panel leaves the focus in it, so ⌘F finds in the terminal (watching, nothing else takes it)
      className="@container flex min-h-0 flex-1 flex-col outline-none"
    >
      {slot && live && createPortal(
          <>
            {v && <SizeLabel v={v} switching={Boolean(feed && canDrive && streaming)} />}
            {feed && v && canDrive && streaming && <ModeSwitch v={v} onChange={(on) => feed.setWants(on)} />}
            {feed && v && !driving && (
              <IconButton
                icon="wrap"
                label={v.wrap ? t("session-terminalpanel.wrap_on") : t("session-terminalpanel.wrap_off")}
                active={v.wrap}
                onClick={() => feed.setWrap(!v.wrap)}
              />
            )}
            {onFiles && dir && <IconButton icon="folder" label={t("session-terminalpanel.files")} onClick={onFiles} className="max-lg:hidden" />}
            <IconButton
              icon="gear"
              label={t("session-terminalpanel.settings")}
              active={Boolean(options)}
              disabled={!feed}
              onClick={(e) => {
                const target = e.currentTarget;
                setOptions((o) => (o ? null : target));
              }}
            />
            <IconButton icon="retry" label={t("session-terminalpanel.reconnect")} onClick={() => feed?.retry()} disabled={!feed} />
          </>,
          slot,
        )}
      {feed && v && !driving && v.loaded && !v.offline && <HistoryStatus v={v} onLoad={() => void feed.loadOlder()} />}
      <div className="relative flex min-h-0 flex-1 flex-col">
        <div
          ref={viewport}
          onScroll={onScroll}
          onWheel={onWheel}
          onContextMenu={onContextMenu}
          className={cx(
            "relative min-h-0 flex-1 p-2 transition-[opacity,filter] duration-300 [overflow-anchor:none]",
            drawn ? "overflow-x-hidden overflow-y-auto" : v?.pinned || older.length ? "overflow-auto" : "overflow-hidden",
            v?.stale && v.loaded && "opacity-45 grayscale",
            v?.dropping && "ring-2 ring-inset ring-accent",
          )}
        >
          {v && older.length > 0 && (
            <div className={cx("font-mono", !drawn && "w-max min-w-full")} style={rowsStyle(v.palette, v.fontFamily, v.fontSize)}>
              <DrawnRows rows={older} fold={drawn} fromEnd marks={v.marks} />
            </div>
          )}
          {v?.screen && (
            <div className="font-mono" style={rowsStyle(v.palette, v.fontFamily, v.fontSize)}>
              <DrawnRows rows={v.screen.rows} fold cursor={v.screen.cursor} marks={v.marks} offset={older.length} />
            </div>
          )}
          <div
            ref={host}
            aria-hidden={drawn || undefined}
            className={cx("w-full font-mono", drawn ? "pointer-events-none invisible absolute inset-2 overflow-hidden" : "h-full")}
          />
        </div>
        {feed && v && find.open && (
          <FindBar
            feed={feed}
            v={v}
            state={find}
            onQuery={(query) => dispatchFind({ type: "query", query })}
            onToggleCase={() => dispatchFind({ type: "toggleCase" })}
            onClose={closeFind}
          />
        )}
        {feed && v && (
          <div className="pointer-events-none absolute inset-x-2 top-2 z-10 flex flex-col gap-1.5">
            {v.stale && !(resumable && v.offline) && <LinkStatus feed={feed} />}
            {v.uploading && <UploadStatus text={v.uploading} />}
            {takeBack && (
              <div className="pointer-events-auto flex items-center gap-2 rounded-control bg-surface py-1.5 pr-1.5 pl-2.5 text-[12px] text-ink-2 shadow-raised" style={{ animation: "fade-in 160ms ease-out both" }}>
                <Icon name="info" size={13} className="shrink-0 text-ink-3" />
                <span className="min-w-0 flex-1 truncate">{v.why === "taken" ? t("session-terminalpanel.taken_elsewhere") : v.note}</span>
                <button
                  type="button"
                  onClick={() => feed.takeOver()}
                  className="h-6 shrink-0 whitespace-nowrap rounded-chip bg-ink px-2.5 text-[12px] font-medium text-surface transition-transform duration-150 active:scale-[0.96]"
                >
                  {t("session-terminalpanel.take_control")}
                </button>
              </div>
            )}
          </div>
        )}
        {v?.paused && (
          <div className="pointer-events-none absolute inset-x-0 bottom-3 z-10 flex justify-center">
            <button
              type="button"
              onClick={backToLatest}
              className="pointer-events-auto inline-flex h-7 items-center gap-1.5 whitespace-nowrap rounded-full bg-surface px-3 text-[12px] font-medium text-ink shadow-raised transition-transform duration-150 active:scale-[0.96]"
              style={{ animation: "fade-up 180ms ease-out both" }}
            >
              <Icon name="arrowDown" size={13} />
              {t("session-terminalpanel.back_to_latest")}
            </button>
          </div>
        )}
        {(!v || !v.loaded) && (
          <div className="pointer-events-none absolute inset-0 flex items-center justify-center">
            <Loading label={t("session-terminalpanel.connecting")} />
          </div>
        )}
      </div>
      {showKeyBar && feed && v && <KeyBar feed={feed} v={v} onEditShortcuts={() => setEditing(true)} />}
      {options && feed && v && (
        <TerminalOptions
          anchor={options}
          feed={feed}
          v={v}
          keyBar={keyBar}
          onKeyBar={chooseKeyBar}
          onEditShortcuts={() => setEditing(true)}
          onClose={() => setOptions(null)}
        />
      )}
      {menu && (
        <Menu
          items={menuItems()}
          anchor={null}
          point={{ x: menu.x, y: menu.y }}
          onClose={() => {
            closeMenu();
            setTimeout(refocus, 0); // after Esc or a choice: back to the terminal, unless the choice went elsewhere (the find field)
          }}
          width={176}
        />
      )}
      <ShortcutsEditor
        open={editing}
        onClose={() => {
          setEditing(false);
          feed?.focus();
        }}
      />

      <Dialog
        open={v?.control === "busy"}
        onClose={() => feed?.watchOnly()}
        title={t("session-terminalpanel.busy_title")}
        footer={
          <>
            <Button tone="quiet" onClick={() => feed?.watchOnly()}>
              {t("session-terminalpanel.watch_only")}
            </Button>
            <Button tone="primary" onClick={() => feed?.takeOver()}>
              {t("session-terminalpanel.take_control")}
            </Button>
          </>
        }
      >
        <p className="text-[13px] leading-relaxed text-ink-2">{t("session-terminalpanel.busy_detail")}</p>
      </Dialog>

      <Dialog
        open={Boolean(v?.pasting)}
        onClose={() => feed?.confirmPaste(false)}
        title={t("session-terminalpanel.paste_title", { count: v?.pasting ? lineCount(v.pasting) : 0 })}
        footer={
          <>
            <Button tone="quiet" onClick={() => feed?.confirmPaste(false)}>
              {t("common.cancel")}
            </Button>
            <Button tone="primary" onClick={() => feed?.confirmPaste(true)}>
              {t("session-terminalpanel.paste")}
            </Button>
          </>
        }
      >
        <p className="text-[13px] leading-relaxed text-ink-2">{t("session-terminalpanel.paste_detail")}</p>
      </Dialog>
    </div>
  );
}

/** Terminals whose mode was chosen with the switch in this page's life (the last choice and Settings decide for the others). */
const chosen = new Map<string, TerminalMode>();

function modeFor(key: string, mid: string, supported: boolean): TerminalMode {
  return pickMode({
    own: chosen.get(key),
    supported,
    noFull: noFullClient.has(mid),
    pref: parsePreference(localStorage.getItem(PREFERENCE_KEY)),
    last: parseMode(localStorage.getItem(LAST_MODE_KEY)),
  });
}

export function TerminalPanel({
  mid,
  sid,
  label,
  cwd,
  onFiles,
  onClose,
  embedded,
}: {
  mid: string;
  sid: string;
  label?: string;
  /** The pane's working directory when the page knows it (else the session's): dropped Files entries are named relative to it. */
  cwd?: string;
  /** Opens the Files panel beside the terminal (the terminal page; as a side panel the Files panel would replace this one). */
  onFiles?: () => void;
  onClose: () => void;
  /** Shown under a header of the page's own (a session's terminal view): this panel's header shrinks to a toolbar of its controls. */
  embedded?: boolean;
}) {
  const session = useStore(() => getSession(mid, sid));
  const machine = useStore((s) => machineWithHub(mid, s.machines));
  const supported = useStore((s) => s.features.includes("herdr_client")) && inHerdrClient(sid, session?.pane_id);
  const key = `${mid}\n${sid}`;
  const root = useRef<HTMLDivElement>(null);
  const [slot, setSlot] = useState<HTMLElement | null>(null);
  const [surfaces, dispatch] = useReducer(surfacesReducer, undefined, () => ({ shown: modeFor(key, mid, supported), leaving: null, from: null }));
  const unseen = useUnseen(root);

  // The hub's features can arrive after the panel does: a terminal nobody chose a mode for takes the one Settings says then.
  useEffect(() => {
    const want = modeFor(key, mid, supported);
    if (surfaces.leaving === null && want !== surfaces.shown && (!supported || !chosen.has(key))) dispatch({ type: "adopt", mode: want });
  }, [key, mid, supported, surfaces.shown, surfaces.leaving]);

  const choose = useCallback(
    (mode: TerminalMode) => {
      if (!supported) return;
      chosen.set(key, mode);
      if (mode === "full") noFullClient.delete(mid); // asked for by hand: tried again
      try {
        localStorage.setItem(LAST_MODE_KEY, mode);
      } catch {
        // not kept: the next terminal starts as Settings says
      }
      dispatch({ type: "choose", mode });
    },
    [key, mid, supported],
  );
  const toggle = useCallback(() => choose(otherMode(surfaces.shown)), [choose, surfaces.shown]);

  // Mod+Alt+F (lib/shortcuts.ts), while this panel is the one in front.
  useEffect(() => {
    if (!supported || unseen) return;
    return registerShortcut("terminal-mode", () => void toggle());
  }, [supported, unseen, toggle]);

  // The surface that gave way is let go once it has faded (at once under reduced motion).
  useEffect(() => {
    if (!surfaces.leaving) return;
    if (prefersReducedMotion()) return void dispatch({ type: "done" });
    const t = setTimeout(() => dispatch({ type: "done" }), SURFACE_MS + 60);
    return () => clearTimeout(t);
  }, [surfaces.leaving]);

  // The client did not start (an old node, no herdr, a refusal): this terminal is a single pane after all, and says why once.
  const fallBack = useCallback(
    (why: string) => {
      noFullClient.add(mid);
      chosen.set(key, "pane");
      dispatch({ type: "choose", mode: "pane" });
      toast(t("terminal-full.fallback", { why }), "info");
    },
    [key, mid],
  );

  // A stopped Agent (#4754), or an Agent the stream says is not running: Resume, the one way it runs again, sits over the
  // terminal in either surface, and stays while a Resume is out (the row may say running before the hub answers). A
  // failed one shows its error there for as long as the Agent is still not running.
  const [paneOffline, setPaneOffline] = useState(false);
  const resume = useResume(mid, sid);
  const agentRow = Boolean(session && !session.source_kind && !session.assistant && !isPaneAddress(sid));
  const resumable = agentRow && (isStoppedAgent(session) || (paneOffline && stateOf(session!) === "offline") || resume.phase === "resuming");

  const dir = cwd || session?.cwd || "";
  const subtitle = [label ?? (session ? sessionTitle(session) : ""), machine].filter(Boolean).join(" · ");
  // pane → full: the pane surface still drives its own pane while it fades, so the other opens after it
  const waitFor = (mode: TerminalMode) => (surfaces.from === "pane" && mode === "full" ? SURFACE_MS + 40 : 0);
  const order: TerminalMode[] = surfaces.leaving ? [surfaces.leaving, surfaces.shown] : [surfaces.shown];

  return (
    <div ref={root} className="@container flex min-h-0 flex-1 flex-col">
      {embedded ? (
        // the page has its header (title, "Terminal" | "Chat", close): this is a toolbar of the terminal's own controls under it
        <div className="flex h-10 shrink-0 items-center gap-2 border-b border-line px-3">
          {supported && <ModeToggle mode={surfaces.shown} onChange={choose} />}
          <span className="flex-1" />
          <span ref={setSlot} className="contents" />
        </div>
      ) : (
        <PanelHeader
          icon="terminal"
          title={t("session-terminalpanel.title")}
          subtitle={subtitle}
          onClose={onClose}
          actions={
            <>
              {supported && <ModeToggle mode={surfaces.shown} onChange={choose} />}
              <span ref={setSlot} className="contents" />
            </>
          }
        />
      )}
      {resumable && <ResumeLine mid={mid} sid={sid} closed={paneOffline} />}
      <div className="relative min-h-0 flex-1 overflow-hidden">
        {order.map((mode) => {
          const leaving = mode === surfaces.leaving;
          return (
            <Surface key={mode} leaving={leaving} enter={!leaving && surfaces.from !== null} dir={slideFrom(surfaces.shown)}>
              {mode === "pane" ? (
                <PaneTerminal mid={mid} sid={sid} cwd={cwd} onFiles={onFiles} slot={slot} live={!leaving} resumable={resumable} onOffline={setPaneOffline} />
              ) : (
                <FullTerminal
                  mid={mid}
                  sid={sid}
                  cwd={dir}
                  onFiles={onFiles}
                  slot={slot}
                  live={!leaving}
                  delay={waitFor(mode)}
                  onFallback={() => choose("pane")}
                  onFailed={fallBack}
                />
              )}
            </Surface>
          );
        })}
      </div>
    </div>
  );
}

/** A stopped Agent's terminal (#4754): what happened, and Resume (`POST /sessions` with its id, no message). The chat's
 *  read-only line starts the same Resume, so both show its progress and its error (lib/agent-resume.ts). `closed`: the
 *  stream says the terminal it left is gone, so Resume opens a new tab. */
function ResumeLine({ mid, sid, closed }: { mid: string; sid: string; closed: boolean }) {
  const resume = useResume(mid, sid);
  return (
    <div role="status" data-resume-line className="flex shrink-0 flex-wrap items-center gap-x-2 gap-y-1.5 border-b border-line bg-inset px-3 py-2 text-xs text-ink-2">
      <Icon name="stop" size={13} className="shrink-0 text-ink-3" />
      <span className="min-w-0 flex-1 basis-48 break-words">{t(closed ? "session-terminalpanel.stopped_closed" : "session-terminalpanel.stopped")}</span>
      <Button tone="secondary" icon="play" busy={resume.phase === "resuming"} onClick={() => void resumeAgent(mid, sid)}>
        {t("session-terminalpanel.resume")}
      </Button>
      {resume.phase === "failed" && <p role="alert" className="w-full break-words text-red">{t("session-terminalpanel.resume_failed", { error: resume.error })}</p>}
    </div>
  );
}

/**
 * Over the terminal while watching (iOS historyStatus): scrolling up reads the page before (or a click does), and
 * where history ends.
 */
function HistoryStatus({ v, onLoad }: { v: FeedView; onLoad: () => void }) {
  const cls = "flex h-7 shrink-0 items-center justify-center gap-1.5 overflow-hidden px-2 whitespace-nowrap text-[11.5px] text-ink-3";
  if (v.loadingOlder)
    return (
      <div className={cls}>
        <Spinner size={11} />
        {t("session-terminalpanel.loading_older")}
      </div>
    );
  if (v.atLimit) return <div className={cls}>{t("session-terminalpanel.history_limit")}</div>;
  if (!v.hasOlder) return <div className={cls}>{t("session-terminalpanel.history_start")}</div>;
  return (
    <div className={cls}>
      <button type="button" onClick={onLoad} className="inline-flex h-6 items-center gap-1 rounded-chip px-2 hover:bg-hover hover:text-ink-2">
        <Icon name="up" size={12} />
        {t("session-terminalpanel.load_older")}
      </button>
    </div>
  );
}

/**
 * What the terminal is doing when the "Take control" / "View only" switch does not say it (#3667): "Taking control…" while taking it, "View only" when
 * it can only be watched or "Take control" was asked for but the keys do not go in (declined, taken, lost). Otherwise nothing:
 * the switch's own words say it, and the grid is in its hint.
 */
function SizeLabel({ v, switching }: { v: FeedView; switching: boolean }) {
  if (!v.ready || !v.loaded) return null;
  if (v.control === "starting") return <span className="hidden shrink-0 whitespace-nowrap text-[11.5px] text-ink-3 @md:inline">{t("session-terminalpanel.size_starting")}</span>;
  // The switch is the truth only when it shows what happens: "Take control" while the keys go in, or "View only" chosen.
  if (switching && (v.control === "active" || !v.wants)) return null;
  const title = v.mode === "poll" ? t("session-terminalpanel.size_poll_hint") : t("session-terminalpanel.size_watch_hint");
  return (
    <span title={title} className="hidden shrink-0 whitespace-nowrap text-[11.5px] text-ink-3 @md:inline">
      {t("session-terminalpanel.size_watch_only")}
    </span>
  );
}

/**
 * "Take control (can type)": the pane takes the panel's size and keys go in; "View only": the pane as the computer has it. The words say
 * the difference; the hint adds the grid in plain columns and rows (#3667).
 */
function ModeSwitch({ v, onChange }: { v: FeedView; onChange: (drive: boolean) => void }) {
  const drive = v.wants;
  const grid = v.cols && v.rows ? { cols: v.cols, rows: v.rows } : null;
  const withGrid = (hint: string, key: string) => (grid ? `${hint}\n${t(key, grid)}` : hint);
  const options: [string, boolean, string][] = [
    [t("session-terminalpanel.mode_control"), true, withGrid(t("session-terminalpanel.mode_control_hint"), "session-terminalpanel.grid_panel")],
    [t("session-terminalpanel.mode_watch"), false, withGrid(t("session-terminalpanel.mode_watch_hint"), "session-terminalpanel.grid_pane")],
  ];
  return (
    <SegmentedTrack
      role="radiogroup"
      label={t("session-terminalpanel.mode_label")}
      data-grid={grid ? `${grid.cols}x${grid.rows}` : undefined}
      className="flex h-7 shrink-0 items-center rounded-control p-0.5"
      thumbClassName="rounded-chip"
    >
      {options.map(([label, value, title]) => (
        <button
          key={label}
          type="button"
          role="radio"
          aria-checked={drive === value}
          data-selected={drive === value}
          title={title}
          onClick={() => onChange(value)}
          className={cx(
            "relative z-[1] h-6 whitespace-nowrap rounded-chip px-2 text-[12px] font-medium transition-colors duration-150",
            drive === value ? "text-ink" : "text-ink-3 hover:text-ink",
          )}
        >
          {label}
        </button>
      ))}
    </SegmentedTrack>
  );
}
