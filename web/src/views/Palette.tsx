// ⌘K: Beautiful UI's Search, over every agent and Persona conversation — and the terminal panes of the computers
// (lib/terminals.ts, the /terminal page's data). `>` first (or ⌘⇧P) is command mode: the registered commands with their
// shortcuts, and whatever other modules add through registerContextCommands (lib/palette.ts). docs/web/web.md"Keyboard shortcuts".
import { useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { enc, api } from "../lib/api";
import { machineWithHub } from "../lib/mesh";
import { createPortal } from "react-dom";
import { ago, sessionTitle, shortPath, stateOf, tabLabel } from "../lib/format";
import { appCommands, commandKeys, contextCommands, filterCommands, inboxHits, orderGroups, parseQuery, placeHits, setPaletteQuery, terminalHits, usePalette, type InboxHit, type MachinePanes, type PaletteCommand, type PlaceHit, type PlaceInput, type TerminalHit } from "../lib/palette";
import { isDone, itemRoute, useInbox, type InboxItem } from "../lib/inbox";
import { useNavPages } from "./Sidebar";
import { conversationPage } from "../lib/conversation-page";
import { href, navigate, type Route } from "../lib/router";
import { composing } from "../lib/send-history";
import { useAgentSessions } from "../lib/sessions";
import { PALETTE_RECENT_CHATS, shownChats, useRecentChats } from "../lib/recent-chats";
import { chordParts, handlerOf, runShortcut, SCOPE_LABEL, useBindings } from "../lib/shortcuts";
import { useStore } from "../lib/store";
import { buildTerminalTree, type PanesResponse } from "../lib/terminals";
import type { Session } from "../lib/types";
import { AgentMark, SessionMark } from "../ui/AgentMark";
import { CARD_OUT, EASE, EXIT_MS, Kbd, toast, useExit } from "../ui/atoms";
import { StatusMark } from "../ui/StatusMark";
import { Icon } from "../ui/icons";
import { useModalFocus } from "../ui/overlay";
import { copyFileMention, FilesHeading, FilesNotice, FileResultRow, openFileHit, usePaletteFiles, type FileRow } from "./palette/FilesSection";
import { t } from "../i18n";
import { byTime } from "../lib/times";
import { STATE_LABEL } from "../lib/state-labels";

interface Hit {
  key: string;
  s: Session;
  to: Route;
  assistant?: boolean;
  score: number;
}

/** One selectable row of the list: a conversation, a terminal pane, an inbox item, a page ("Go to"), a file
 *  (views/palette/FilesSection.tsx), or (command mode) a command. */
type Row =
  /** `recent`: in "Recently opened" (#2191), `previous` its first, the one ⌘K ⏎ goes back to. */
  | { kind: "session"; key: string; hit: Hit; recent?: boolean; previous?: boolean }
  | { kind: "pane"; key: string; hit: TerminalHit }
  | { kind: "inbox"; key: string; hit: InboxHit<InboxItem> }
  | { kind: "place"; key: string; hit: PlaceHit }
  | { kind: "command"; key: string; cmd: PaletteCommand }
  | FileRow;

// The words every page answers to in any UI language (the Mac's quick open matches English and Chinese alike);
// the catalog's words_* add the UI language's own synonyms. Search data, never shown: i18n-ignore.
const PLACE_WORDS: Record<string, string> = {
  persona: "persona assistant persona main 助手 主对话", // i18n-ignore
  history: "history chats conversations 对话 历史", // i18n-ignore
  home: "home overview 总览 首页", // i18n-ignore
  inbox: "inbox mail lark feishu 邮件 飞书 收件箱", // i18n-ignore
  health: "health 健康", // i18n-ignore
  messages: "messages lark feishu chats 飞书 消息 聊天", // i18n-ignore
  "workspace-board": "board kanban workflow 看板 工作流", // i18n-ignore
  todos: "todo todos tasks 待办 任务", // i18n-ignore
  routines: "routines schedule cron followup 定时 跟进", // i18n-ignore
  kb: "knowledge kb people projects docs 知识库 项目 文档 会议", // i18n-ignore
  machines: "machines monitor computers cpu memory 电脑 监控 内存", // i18n-ignore
  terminal: "terminal shell 终端", // i18n-ignore
  terminals: "terminals terminal shell 终端", // i18n-ignore
  settings: "settings preferences 设置 偏好", // i18n-ignore
};

export function placeWords(key: string): string {
  const own: Record<string, string> = {
    persona: t("palette.words_persona"),
    history: t("palette.words_history"),
    home: t("palette.words_home"),
    inbox: t("palette.words_inbox"),
    health: t("palette.words_health"),
    messages: t("palette.words_messages"),
    "workspace-board": t("palette.words_board"),
    todos: t("palette.words_todos"),
    routines: t("palette.words_routines"),
    kb: t("palette.words_kb"),
    machines: t("palette.words_machines"),
    terminal: t("palette.words_terminal"),
    terminals: t("palette.words_terminal"),
    settings: t("palette.words_settings"),
  };
  return [PLACE_WORDS[key], own[key]].filter(Boolean).join(" ");
}

/** A group's heading above its first row ("Inbox", "Go to"). */
function GroupHeading({ children }: { children: React.ReactNode }) {
  return <div className="px-2.5 pt-2 pb-1 text-[10.5px] font-medium tracking-[0.06em] text-ink-3 uppercase">{children}</div>;
}

/** One row of the list as drawn: the option, and the heading above it when a group starts there. */
interface Section {
  key: string;
  heading: ReactNode;
  row: ReactNode;
}

/** The rows in their groups: a group with a heading is a role=group named by it, so a screen reader hears the
 *  heading with the options and the listbox holds only options and groups. */
function sections(items: Section[], listId: string): ReactNode[] {
  const groups: { key: string; heading: ReactNode; rows: Section[] }[] = [];
  for (const it of items) {
    if (it.heading || !groups.length) groups.push({ key: it.key, heading: it.heading, rows: [] });
    groups[groups.length - 1].rows.push(it);
  }
  return groups.map((g, n) => {
    const rows = g.rows.map((it) => <div key={it.key} role="none">{it.row}</div>);
    if (!g.heading) return <div key={g.key} role="none">{rows}</div>;
    const id = `${listId}-g${n}`;
    return (
      <div key={g.key} role="group" aria-labelledby={id}>
        <div id={id}>{g.heading}</div>
        {rows}
      </div>
    );
  });
}

function score(s: Session, q: string, machine: string): number {
  if (!q) return 1;
  const title = sessionTitle(s).toLowerCase();
  if (title.startsWith(q)) return 100;
  if (title.includes(q)) return 80;
  const hay = [s.cwd, s.space_name, s.slot_name, machine, s.last_text].join(" ").toLowerCase();
  return q.split(/\s+/).every((w) => hay.includes(w) || title.includes(w)) ? 40 : 0;
}

const HOME: Route = { name: "home" };

/** The conversation "Go back" opens (#2191): the newest opened here, other than the one on the page, that still exists. */
function previousChat(recent: { entries: string[]; current: string }, sessions: Session[], conversations: Session[]): { s: Session; to: Route } | null {
  const find = (k: string): { s: Session; to: Route } | null => {
    if (k.startsWith("j:")) {
      const s = conversations.find((c) => `j:${c.session_id}` === k);
      return s ? { s, to: { name: "persona", sid: s.session_id } } : null;
    }
    const s = sessions.find((c) => `s:${c.machine_id}/${c.session_id}` === k);
    return s ? { s, to: { name: "session", mid: s.machine_id, sid: s.session_id } } : null;
  };
  const [key] = shownChats(recent.entries, recent.current, (k) => find(k) !== null, 1);
  return key ? find(key) : null;
}

export function Palette({ open, onClose, onNewAgent, pane, route = HOME }: { open: boolean; onClose: () => void; onNewAgent: () => void; pane?: { mid: string; sid: string }; route?: Route }) {
  // what is typed lives in the store (lib/palette.ts): the shortcuts open it in a mode (`>` for commands) and read it
  const q = usePalette().query;
  const setQ = setPaletteQuery;
  const { mode, text } = parseQuery(q);
  const [active, setActive] = useState(0);
  const [box, setBox] = useState<{ top: number; height: number } | null>(null);
  const sessions = useAgentSessions();
  // the conversations opened here, newest first, and the one on the page (#2191)
  const recentChats = useRecentChats();
  const assistant = useStore((s) => s.assistant);
  const machines = useStore((s) => s.machines);
  const bySession = useStore((s) => s.sessions);
  const panesOn = useStore((s) => s.features.includes("terminal_panes"));
  const bindings = useBindings();
  // "Inbox" and "Go to" (the Mac's quick open): the open inbox items, and the sidebar's pages with Persona and "All chats"
  const inboxItems = useInbox((s) => s.items);
  // every page, the ones under More too: finding a page is what this is for
  const { all: pages } = useNavPages();
  // the pages come anew each render: kept by what they are, so the list (and the highlight under it) stays put
  const hasAssistant = Boolean(assistant);
  const pagesKey = pages.map((p) => [p.k, p.label, p.icon, href(p.to)].join("\u0001")).join("\n");
  const places = useMemo<PlaceInput[]>(
    () => [
      ...(hasAssistant ? [{ key: "persona", title: t("palette.place_persona"), icon: "spark" as const, to: { name: "persona" } as Route }, { key: "history", title: t("sidebar.all_chats"), icon: "list" as const, to: { name: "history" } as Route }] : []),
      ...pages.map((p) => ({ key: p.k, title: p.label, icon: p.icon, to: p.to })),
      // the Sessions and Terminal page's Terminals list (#4754): every plain terminal, to open one and type
      ...(panesOn ? [{ key: "terminals", title: t("palette.place_terminals"), icon: "terminal" as const, to: { name: "terminal", view: "terminals" } as Route }] : []),
    ].map((p) => ({ ...p, words: placeWords(p.key) })),
    // eslint-disable-next-line react-hooks/exhaustive-deps -- `pages` is new each render; pagesKey says what it holds
    [hasAssistant, pagesKey, panesOn],
  );
  const rows = useRef<(HTMLButtonElement | null)[]>([]);
  const list = useRef<HTMLDivElement>(null);
  // the listbox and its options, for the field's aria-controls / aria-activedescendant
  const listId = useId();
  const optionId = (i: number) => `${listId}-${i}`;

  useEffect(() => {
    if (open) setActive(0);
  }, [open]);

  // The terminal panes of the online computers, read when the palette opens (the /terminal page's endpoint).
  const onlineKey = machines.filter((m) => m.online).map((m) => m.id).join("\n");
  const [trees, setTrees] = useState<Record<string, PanesResponse>>({});
  useEffect(() => {
    if (!open || !panesOn || !onlineKey) return;
    let live = true;
    for (const mid of onlineKey.split("\n")) {
      api<PanesResponse>(`/machines/${enc(mid)}/panes`).then(
        (res) => live && setTrees((t) => ({ ...t, [mid]: res })),
        () => {}, // a computer that cannot be read just has no panes here
      );
    }
    return () => {
      live = false;
    };
  }, [open, panesOn, onlineKey]);
  const machinePanes = useMemo<MachinePanes[]>(
    () =>
      machines.flatMap((m) =>
        trees[m.id] ? [{ mid: m.id, machine: machineWithHub(m.id, machines), spaces: buildTerminalTree(trees[m.id], bySession[m.id] ?? []), sessionIds: new Set((bySession[m.id] ?? []).map((x) => x.session_id)) }] : [],
      ),
    [machines, trees, bySession],
  );

  const hits = useMemo((): Row[] => {
    if (mode !== "jump") return [];
    const query = text.toLowerCase();
    const name = (m: string) => machineWithHub(m, machines);
    const out: Hit[] = [];
    for (const s of sessions) {
      const sc = score(s, query, name(s.machine_id));
      if (sc) out.push({ key: `s:${s.machine_id}/${s.session_id}`, s, to: { name: "session", mid: s.machine_id, sid: s.session_id }, score: sc + (stateOf(s) === "offline" ? 0 : 5) });
    }
    for (const s of assistant?.conversations ?? []) {
      const sc = score(s, query, "persona");
      if (sc) out.push({ key: `j:${s.session_id}`, s, to: conversationPage(s.session_id, s.machine_id, s.manager), assistant: true, score: sc });
    }
    // nothing typed: the conversations opened here come first, newest first, without the one on the page (#2191); the
    // first is the one before it, picked already, so ⌘K ⏎ goes back to it
    const byKey = new Map(out.map((h) => [h.key, h]));
    const recent = query ? [] : shownChats(recentChats.entries, recentChats.current, (k) => byKey.has(k), PALETTE_RECENT_CHATS);
    const recentRows: Row[] = recent.map((k, i) => ({ kind: "session" as const, key: k, hit: byKey.get(k)!, recent: true, previous: i === 0 }));
    const all: (Row & { score: number; at: string })[] = [
      ...out.filter((h) => !recent.includes(h.key)).map((h) => ({ kind: "session" as const, key: h.key, hit: h, score: h.score, at: h.s.updated_at ?? "" })),
      ...terminalHits(machinePanes, text).map((h) => ({ kind: "pane" as const, key: h.key, hit: h, score: h.score, at: "" })),
    ];
    all.sort((a, b) => b.score - a.score || byTime(b.at, a.at));
    const mail = inboxHits(inboxItems.filter((it) => !isDone(it)), text).map((h) => ({ kind: "inbox" as const, key: h.key, hit: h, score: h.score }));
    const go = placeHits(places, text).map((h) => ({ kind: "place" as const, key: h.key, hit: h, score: h.score }));
    return [...recentRows, ...orderGroups<Row & { score: number }>([all.slice(0, 40), mail, go]).flat()];
  }, [mode, text, sessions, assistant, machines, machinePanes, inboxItems, places, recentChats]);

  // command mode: what other modules offer for here, then the registered commands with their shortcuts
  const commands = useMemo((): Row[] => {
    if (!open || mode !== "command") return [];
    const run = (id: string, digit?: number) => {
      if (!runShortcut(id, { digit })) toast(t("palette.command_unavailable"));
    };
    // Go back to the previous conversation (#2191): the newest opened here other than the one on the page that still exists
    const back = previousChat(recentChats, sessions, assistant?.conversations ?? []);
    const extra: PaletteCommand[] = [
      ...(back ? [{ id: "app:previous-chat", title: t("palette.previous_chat"), detail: sessionTitle(back.s), section: SCOPE_LABEL.global, icon: "undo" as const, keywords: "back previous last recent 返回 上一个 最近", run: () => navigate(back.to) }] : []), // i18n-ignore: search words in both languages, as PLACE_WORDS
      { id: "app:new-agent", title: t("palette.new_agent"), section: SCOPE_LABEL.global, icon: "plus", keywords: "new agent", run: onNewAgent },
      { id: "app:new-conversation", title: t("palette.new_conversation"), section: SCOPE_LABEL.global, icon: "chat", keywords: "persona new", run: () => navigate({ name: "persona-new" }) },
    ];
    const all = [...contextCommands({ route, pane }), ...appCommands({ bindings, available: (id) => Boolean(handlerOf(id)), run, machines: machines.map((m) => ({ id: m.id, name: m.name })), extra })];
    return filterCommands(all, text).map((cmd) => ({ kind: "command" as const, key: cmd.id, cmd }));
    // eslint-disable-next-line react-hooks/exhaustive-deps -- `route` is parsed anew each render; its path says it all
  }, [open, mode, text, bindings, machines, pane?.mid, pane?.sid, href(route), onNewAgent, recentChats, sessions, assistant]);
  // the files under the directory of the page (#2438): listed after the conversations and panes
  const files = usePaletteFiles({ open, jump: mode === "jump", text, route, pane, machinePanes });
  const shown = useMemo(() => (mode === "command" ? commands : [...hits, ...files.rows]), [mode, commands, hits, files.rows]);

  useEffect(() => setActive(0), [q]);
  useLayoutEffect(() => {
    const el = rows.current[active];
    setBox(el ? { top: el.offsetTop, height: el.offsetHeight } : null);
    el?.scrollIntoView({ block: "nearest" });
  }, [active, shown]);

  // It leaves as Dialog does (ui/atoms.tsx useExit): a fade and the card back down, showing what it last showed.
  const backdrop = useRef<HTMLDivElement>(null);
  const card = useRef<HTMLDivElement>(null);
  const input = useRef<HTMLInputElement>(null);
  const kept = useRef<ReactNode>(null);
  const exit = useExit(open, EXIT_MS.card, () => [
    backdrop.current?.animate([{ opacity: 1 }, { opacity: 0 }], { duration: EXIT_MS.card, easing: "ease-out", fill: "forwards" }),
    card.current?.animate(CARD_OUT, { duration: EXIT_MS.card, easing: EASE, fill: "forwards" }),
  ]);
  // a modal for the keyboard (ui/overlay.ts): Tab stays in, and closing gives the focus back to where it was. Called
  // before the effect below, so it notes that place before the field takes the focus.
  useModalFocus(card, open);
  // the field takes the focus on every open (opened again on its way out, it is not mounted anew)
  useEffect(() => {
    if (open) input.current?.focus({ preventScroll: true });
  }, [open]);

  if (!exit.mounted) return null;
  const pick = (r: Row) => {
    onClose();
    if (r.kind === "command") r.cmd.run();
    else if (r.kind === "file") openFileHit(r);
    else if (r.kind === "inbox") navigate(itemRoute(r.hit.item));
    else if (r.kind === "place") navigate(r.hit.place.to);
    else navigate(r.hit.to);
  };
  const command = mode === "command";
  const label = command ? t("palette.command_palette") : t("palette.search_sessions");

  return createPortal(
    <div ref={backdrop} data-closing={exit.leaving || undefined} inert={exit.leaving || undefined} className="fixed inset-0 z-40 flex items-start justify-center bg-black/20 px-4 pt-[14vh] backdrop-blur-[2px] dark:bg-black/45" style={{ animation: "fade-in 120ms ease-out both" }} onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div ref={card} role="dialog" aria-modal="true" tabIndex={-1} data-shortcut-layer={open || undefined} aria-label={label} className="w-full max-w-[560px] overflow-hidden rounded-[14px] bg-surface shadow-overlay outline-none" style={{ animation: `slide-up 200ms ${EASE} both` }}>
        {/* on the way out it shows what it showed when open: closed, the lists below come back empty */}
        {open ? (kept.current = <>
        <label className="flex h-12 items-center gap-2.5 border-b border-line px-4">
          <Icon name="search" size={15} stroke={2} className="text-ink-3" />
          <input
            ref={input}
            role="combobox"
            aria-label={label}
            aria-autocomplete="list"
            aria-expanded={shown.length > 0}
            aria-controls={shown.length > 0 ? listId : undefined}
            aria-activedescendant={shown[active] ? optionId(active) : undefined}
            value={q}
            onChange={(e) => setQ(e.target.value)}
            onKeyDown={(e) => {
              if (composing(e.nativeEvent)) return;
              if (e.key === "ArrowDown" || e.key === "ArrowUp") {
                e.preventDefault();
                if (shown.length) setActive((a) => (a + (e.key === "ArrowDown" ? 1 : shown.length - 1)) % shown.length);
              } else if (e.key === "Enter") {
                e.preventDefault();
                const row = shown[active];
                if (row?.kind === "file" && (e.metaKey || e.ctrlKey)) {
                  onClose();
                  void copyFileMention(row);
                } else if (row) pick(row);
              } else if (e.key === "Escape") onClose();
            }}
            placeholder={command ? t("palette.command_placeholder") : files.ctx ? t("files-find.searching_placeholder") : t("palette.search_placeholder")}
            className="min-w-0 flex-1 bg-transparent text-[14px] text-ink outline-none placeholder:text-ink-3"
          />
          {/* the key hints are for a keyboard: a touch screen has none (#1996) */}
          <span className="contents [@media(pointer:coarse)]:hidden">
            <Kbd>Esc</Kbd>
          </span>
        </label>
        <div ref={list} className="relative max-h-[52vh] overflow-y-auto p-1.5" onMouseLeave={() => {}}>
          <span aria-hidden className="pointer-events-none absolute inset-x-1.5 rounded-[8px] bg-hover" style={{ top: box?.top ?? 0, height: box?.height ?? 0, opacity: box ? 1 : 0, transition: `top 180ms ${EASE}, height 180ms ${EASE}` }} />
          {shown.length > 0 && (
          <div role="listbox" id={listId} aria-label={label}>
          {sections(shown.map((r, i): Section => {
            const ref = (el: HTMLButtonElement | null) => {
              rows.current[i] = el;
            };
            const rowClass = "relative z-10 flex w-full items-center gap-3 rounded-[8px] px-2.5 py-2 text-left";
            const option = { id: optionId(i), role: "option" as const, "aria-selected": i === active };
            if (r.kind === "command") {
              const c = r.cmd;
              const heading = c.section && c.section !== (shown[i - 1] as Extract<Row, { kind: "command" }> | undefined)?.cmd.section;
              return {
                key: r.key,
                heading: heading ? <div className="px-2.5 pt-2 pb-1 text-[10.5px] font-medium tracking-[0.06em] text-ink-3 uppercase">{c.section}</div> : null,
                row: (
                  <button ref={ref} type="button" {...option} onMouseMove={() => setActive(i)} onClick={() => pick(r)} className={rowClass}>
                    <span className="flex size-5 shrink-0 items-center justify-center">{c.icon && <Icon name={c.icon} size={13} className={c.danger ? "text-red" : "text-ink-3"} />}</span>
                    <span className={`min-w-0 flex-1 truncate text-[13px] ${c.danger ? "text-red" : "text-ink"}`}>
                      {c.title}
                      {c.detail && <span className="ml-2 text-[12px] text-ink-3">{c.detail}</span>}
                    </span>
                    {c.chord && (
                      <span className="flex shrink-0 items-center gap-1 [@media(pointer:coarse)]:hidden">
                        {commandKeys(c).map((k, n) => (
                          <Kbd key={n}>{k}</Kbd>
                        ))}
                      </span>
                    )}
                  </button>
                ),
              };
            }
            if (r.kind === "file") {
              const first = (shown[i - 1] as Row | undefined)?.kind !== "file";
              return { key: r.key, heading: first ? <FilesHeading /> : null, row: <FileResultRow ref={ref} row={r} option={option} className={rowClass} onMouseMove={() => setActive(i)} onClick={() => pick(r)} /> };
            }
            if (r.kind === "inbox" || r.kind === "place") {
              const heading = (shown[i - 1] as Row | undefined)?.kind !== r.kind;
              const body =
                r.kind === "inbox" ? (
                  <>
                    <span className="flex size-5 shrink-0 items-center justify-center"><Icon name="inbox" size={13} className="text-ink-3" /></span>
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-[13px] font-medium text-ink">{r.hit.item.title}</span>
                      <span className="block truncate text-[11.5px] text-ink-3">{[r.hit.item.sender, r.hit.item.preview].filter(Boolean).join(t("palette.sender_separator"))}</span>
                    </span>
                    <span className="shrink-0 text-[11px] text-ink-3">{ago(r.hit.item.updated_at)}</span>
                  </>
                ) : (
                  <>
                    <span className="flex size-5 shrink-0 items-center justify-center"><Icon name={r.hit.place.icon} size={13} className="text-ink-3" /></span>
                    <span className="min-w-0 flex-1 truncate text-[13px] font-medium text-ink">{r.hit.place.title}</span>
                  </>
                );
              return {
                key: r.key,
                heading: heading ? <GroupHeading>{r.kind === "inbox" ? t("palette.group_inbox") : t("palette.group_places")}</GroupHeading> : null,
                row: (
                  <button ref={ref} type="button" {...option} onMouseMove={() => setActive(i)} onClick={() => pick(r)} className={rowClass}>
                    {body}
                  </button>
                ),
              };
            }
            // the conversations and panes have a heading only after another group ("Recently opened", "Inbox" or "Go to");
            // "Recently opened" has its own
            const before = shown[i - 1] as Row | undefined;
            const prev = before?.kind;
            const recent = r.kind === "session" && r.recent;
            const afterRecent = before?.kind === "session" && before.recent && !recent;
            const sessionsHeading = recent
              ? (i === 0 ? <GroupHeading>{t("palette.group_recent")}</GroupHeading> : null)
              : (i > 0 && ((prev !== "session" && prev !== "pane") || afterRecent) ? <GroupHeading>{t("palette.group_sessions")}</GroupHeading> : null);
            if (r.kind === "pane") {
              const h = r.hit;
              return {
                key: r.key,
                heading: sessionsHeading,
                row: (
                <button ref={ref} type="button" {...option} onMouseMove={() => setActive(i)} onClick={() => pick(r)} className={rowClass}>
                  {h.state !== "shell" && h.agent && <AgentMark agent={h.agent} surface={h.surface} size={14} />}
                  <span className="flex size-5 shrink-0 items-center justify-center">{h.state === "shell" ? <Icon name="terminal" size={13} className="text-ink-3" /> : <StatusMark state={h.state} />}</span>
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-[13px] font-medium text-ink">{h.title}</span>
                    <span className="block truncate text-[11.5px] text-ink-3">{[h.trail, h.detail].filter(Boolean).join(" · ")}</span>
                  </span>
                  <span className="shrink-0 text-[11px] text-ink-3">{t("palette.terminal")}</span>
                </button>
                ),
              };
            }
            const h = r.hit;
            const st = stateOf(h.s);
            const meta = h.assistant ? "Persona" : [machineWithHub(h.s.machine_id, machines), h.s.space_name, tabLabel(h.s)].filter(Boolean).join(" › ");
            return {
              key: h.key,
              heading: sessionsHeading,
              row: (
              <button ref={ref} type="button" {...option} onMouseMove={() => setActive(i)} onClick={() => pick(r)} className={rowClass}>
                {!h.assistant && <SessionMark s={h.s} size={14} />}
                <span className="flex size-5 shrink-0 items-center justify-center">{h.assistant ? <Icon name="spark" size={13} className="text-ink-3" /> : <StatusMark state={st} />}</span>
                <span className="min-w-0 flex-1">
                  <span className="flex min-w-0 items-center gap-1.5">
                    <span className="truncate text-[13px] font-medium text-ink">{sessionTitle(h.s)}</span>
                    {r.previous && <span className="shrink-0 rounded-[5px] bg-field px-1.5 text-[10.5px] text-ink-3 shadow-hairline">{t("palette.previous")}</span>}
                  </span>
                  <span className="block truncate text-[11.5px] text-ink-3">{[meta, shortPath(h.s.cwd)].filter(Boolean).join(" · ")}</span>
                </span>
                <span className="shrink-0 text-[11px] text-ink-3">{st === "offline" || h.assistant ? ago(h.s.updated_at) : STATE_LABEL[st]}</span>
              </button>
              ),
            };
          }), listId)}
          </div>
          )}
          {shown.length === 0 && command && (
            <div className="px-4 py-10 text-center">
              <p className="text-[13px] font-medium text-ink">{t("palette.no_commands")}</p>
              <p className="mt-0.5 text-[12px] text-ink-3">{t("palette.no_commands_hint")}</p>
            </div>
          )}
          {!command && <FilesNotice status={files.status} text={text} count={files.rows.length} />}
          {shown.length === 0 && !command && files.status !== "loading" && (
            <div className="flex flex-col items-center gap-3 px-4 py-10 text-center">
              <span className="flex size-9 items-center justify-center rounded-full bg-field text-ink-3 shadow-hairline">
                <Icon name="search" size={15} />
              </span>
              <div>
                <p className="text-[13px] font-medium text-ink">{t("palette.no_results", { query: q })}</p>
                <p className="mt-0.5 text-[12px] text-ink-3">{t("palette.no_results_hint")}</p>
              </div>
              <button
                type="button"
                onClick={() => {
                  onClose();
                  onNewAgent();
                }}
                className="rounded-control px-2.5 py-1 text-[12.5px] font-medium text-accent-ink hover:bg-accent-tint"
              >
                {t("palette.new_agent")}
              </button>
            </div>
          )}
        </div>
        <div className="flex items-center gap-3 border-t border-line bg-inset px-4 py-2 text-[11px] text-ink-3 [@media(pointer:coarse)]:hidden">
          <span className="flex items-center gap-1">
            <Kbd>↑</Kbd>
            <Kbd>↓</Kbd> {t("palette.select")}
          </span>
          <span className="flex items-center gap-1">
            <Kbd>↵</Kbd> {command ? t("palette.run") : t("palette.open")}
          </span>
          {shown[active]?.kind === "file" && (
            <span className="flex items-center gap-1">
              <Kbd>{chordParts("Mod+Enter")[0]}</Kbd>
              <Kbd>↵</Kbd> {t("files-find.copy_mention")}
            </span>
          )}
          {!command && (
            <span className="flex items-center gap-1">
              <Kbd>&gt;</Kbd> {t("palette.commands")}
            </span>
          )}
        </div>
        </>) : kept.current}
      </div>
    </div>,
    document.body,
  );
}
