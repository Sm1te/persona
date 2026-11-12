import { APP_SCHEMES, assistantPath } from "./compatibility";
// Where a link opens (CLAUDE.md "Links open inside the app", docs/web/web.md "Links"). In a browser the browser is the app:
// ordinary pages open in a new tab (most sites refuse to be framed and the page cannot tell it was refused, so a
// framed panel would just be blank), Feishu documents and Moments posts open in the side panel's reader (the same links
// the phone reads natively, FeishuLinks.route) when the hub reads Feishu (feature `lark`; otherwise they are ordinary
// pages), and persona://session links move to that conversation. This hub's own agent and task pages (/s/<machine>/
// <session>, /todos?id=…, the links the assistant writes) open as pages of their own, never beside the chat (#5096), and
// so do its conversations (/persona/<id>). The
// hub's own pages are those of this page's origin and of every address the hub advertises (hello's endpoints, its
// public_url first: the address the assistant writes, which may not be the one this page was opened at).
//
// Also the hand-off between the reader and the composers: text picked in the panel goes to the composer the user is
// writing in, as <persona-context> tags in the next message (docs/protocol/protocol.md "Tags in messages").
import { t } from "../i18n";
import { useSyncExternalStore } from "react";
import { uuid } from "./format";
import { openPanel } from "./panel";
import { leaveChat, taskRoute } from "./handoff";
import { navigate, parse as parseRoute } from "./router";
import { BASE, stripBase } from "./base";
import { contextXML } from "./context-xml";
import { otherManager } from "./manager-choice";
import { localhostLink, PREVIEW_FEATURE } from "./preview";
import type { PortLink } from "./port-links";
import { getSession, getState, hubHas, larkOn, onHubEvent } from "./store";

/** What a Feishu document link points at, from the link alone (FeishuDocsKind.from). A wiki link does not say. */
export type DocKind = "doc" | "sheet" | "bitable" | "mindnote" | "file" | "slides" | "wiki" | "page" | "whiteboard" | "other";

export type LinkRoute =
  | { kind: "doc"; doc: DocKind } // a Feishu cloud document: the panel's reader
  | { kind: "moments"; id: string } // a Moments post: the panel
  | { kind: "session"; mid: string; sid: string } // persona://session/<machine>/<session>: that conversation
  | { kind: "agent"; mid: string; sid: string } // this hub's /s/<machine>/<session>: that agent's page
  | { kind: "task"; id: string } // this hub's /todos?id=<todo>: that task's Agent's page, or its to-do's while it has none
  | { kind: "conversation"; sid: string; mid?: string; manager?: string } // this hub's /persona/<id>: that assistant conversation, in place (with the computer and manager its address names)
  | { kind: "web" } // any other http(s) page: a new tab
  | { kind: "system" } // mailto: / tel:: the browser hands it to the system
  | { kind: "none" }; // anything else (javascript:, other apps' schemes, not a URL): not followed

const FEISHU_HOSTS = ["larkoffice.com", "feishu.cn", "larksuite.com"];
// First path segments the reader opens (FeishuLinks.isDoc).
const DOC_PATHS = ["docx", "wiki", "docs", "sheets", "base", "mindnotes", "file", "slides"];

function parse(url: string): URL | null {
  try {
    return new URL(url);
  } catch {
    return null;
  }
}

function isFeishuHost(host: string) {
  const h = host.toLowerCase();
  return FEISHU_HOSTS.some((d) => h === d || h.endsWith(`.${d}`));
}

export function docKind(url: string): DocKind {
  const parts = (parse(url)?.pathname ?? "").split("/").filter(Boolean);
  switch (parts[0] ?? "") {
    case "docx":
    case "docs":
    case "doc":
      return "doc";
    case "sheets":
    case "sheet":
      return "sheet";
    case "base":
    case "bitable":
      return "bitable";
    case "mindnotes":
    case "mindnote":
      return "mindnote";
    case "file":
      return "file";
    case "slides":
      return "slides";
    case "wiki":
      return "wiki";
    case "page":
      return "page";
    case "board":
    case "whiteboard":
      return "whiteboard";
    default:
      return "other";
  }
}

export const DOC_LABEL: Record<DocKind, string> = {
  doc: t("lib-links.doc"),
  sheet: t("lib-links.sheet"),
  bitable: t("lib-links.bitable"),
  mindnote: t("lib-links.mindnote"),
  file: t("lib-links.file"),
  slides: t("lib-links.slides"),
  wiki: t("lib-links.wiki"),
  page: t("lib-links.page"),
  whiteboard: t("lib-links.whiteboard"),
  other: t("lib-links.doc"),
};

/** A Moments share link's post id (LarkMoments.postID). */
function momentsPostID(u: URL): string | null {
  if (!u.pathname.endsWith("/moments/detail")) return null;
  const id = u.searchParams.get("postId") ?? "";
  return /^\d+$/.test(id) ? id : null;
}

// The origins of the addresses the hub advertises (hello's endpoints), kept from its last hello.
let advertised: string[] = [];
onHubEvent("hello", (msg) => {
  advertised = hubOrigins(msg.endpoints);
});

/** The origins of a hello's `endpoints` ({url}[]); anything else in the list is skipped. */
export function hubOrigins(endpoints: unknown): string[] {
  if (!Array.isArray(endpoints)) return [];
  return endpoints.flatMap((e) => {
    const u = typeof e === "object" && e && typeof (e as { url?: unknown }).url === "string" ? parse((e as { url: string }).url) : null;
    return u && (u.protocol === "http:" || u.protocol === "https:") ? [u.origin] : [];
  });
}

const pageOrigin = () => (typeof location !== "undefined" ? [location.origin, ...advertised] : advertised);

/** An agent, task or conversation page of the hub this page is served by (router.ts's /s/…, /todos, /persona/…, under the
 * sub-path the page is served at), or null. */
function hubPage(u: URL, origin: string | string[], base: string): LinkRoute | null {
  const origins = (Array.isArray(origin) ? origin : [origin]).filter(Boolean);
  if (!origins.includes(u.origin)) return null;
  if (base && u.pathname !== base && !u.pathname.startsWith(base + "/")) return null;
  let p: string[];
  try {
    p = stripBase(u.pathname, base).split("/").filter(Boolean).map(decodeURIComponent);
  } catch {
    return null;
  }
  if (p[0] === "s" && p[1] && p[2] && p.length === 3) return { kind: "agent", mid: p[1], sid: p[2] };
  if (assistantPath(p[0]) && p[1] && p[1] !== "new" && p.length === 2) {
    const mid = u.searchParams.get("machine");
    const manager = u.searchParams.get("manager");
    return { kind: "conversation", sid: p[1], ...(mid ? { mid } : {}), ...(manager ? { manager } : {}) };
  }
  if (p[0] === "todos" && p.length <= 2) {
    const id = p[1] || u.searchParams.get("id");
    if (id) return { kind: "task", id };
  }
  return null;
}

/**
 * Where `url` opens. `lark`: whether the hub reads Feishu (store.larkOn); without it Feishu links are web pages.
 * `origin`: this page's and the hub's advertised ones, for telling the hub's own pages from other sites.
 */
export function linkRoute(url: string, lark = larkOn(), origin: string | string[] = pageOrigin(), base = BASE): LinkRoute {
  const u = parse(url.trim());
  if (!u) return { kind: "none" };
  const scheme = u.protocol.toLowerCase();
  if (APP_SCHEMES.has(scheme.slice(0, -1))) {
    // persona://session/<mid>/<sid>: the host is "session"
    try {
      const parts = `${u.host}${u.pathname}`.split("/").filter(Boolean).map(decodeURIComponent);
      if (parts[0] === "session" && parts[1] && parts[2]) return { kind: "session", mid: parts[1], sid: parts[2] };
    } catch {
      // A malformed percent escape in model output must not crash the transcript render.
      return { kind: "none" };
    }
    return { kind: "none" };
  }
  if (scheme === "mailto:" || scheme === "tel:") return { kind: "system" };
  if (scheme !== "http:" && scheme !== "https:") return { kind: "none" };
  const own = hubPage(u, origin, base);
  if (own) return own;
  if (lark && isFeishuHost(u.hostname)) {
    const first = u.pathname.split("/").filter(Boolean)[0] ?? "";
    if (DOC_PATHS.includes(first)) return { kind: "doc", doc: docKind(url) };
    const post = momentsPostID(u);
    if (post) return { kind: "moments", id: post };
  }
  return { kind: "web" };
}

/**
 * Whether a link opens inside the page (panel or route) rather than in a new tab. A Base never does: the reader can't
 * show one (the phone says so and offers Feishu), so the tab is one click shorter.
 */
export function opensInApp(url: string): boolean {
  if (previewTarget(url)) return true;
  const r = linkRoute(url);
  return (r.kind === "doc" && r.doc !== "bitable") || r.kind === "moments" || r.kind === "session" || r.kind === "agent" || r.kind === "task" || r.kind === "conversation";
}

/**
 * A localhost link on an agent's page (`http://localhost:5173/x`, as a dev server prints it): that port of the
 * agent's computer, shown in the preview panel (docs/web/preview.md) instead of a tab on this one's localhost. Null
 * elsewhere, and on a hub without previews.
 */
export function previewTarget(url: string): { mid: string; sid: string; cwd: string; port: number; path: string } | null {
  if (typeof location === "undefined" || !hubHas(getState().features, PREVIEW_FEATURE)) return null;
  const r = parseRoute(stripBase(location.pathname), location.search.replace(/^\?/, ""));
  if (r.name !== "session" && r.name !== "tab") return null;
  const mid = r.mid;
  const at = localhostLink(url, machineHostnames(mid));
  if (!at) return null;
  // This hub's own pages (a hub on the agent's computer, this page opened at localhost) open as hub pages, not in a preview.
  const u = parse(url);
  if (u && hubPage(u, pageOrigin(), BASE)) return null;
  const sid = r.name === "session" ? r.sid : r.sid ?? "";
  const s = sid ? getSession(mid, sid) : undefined;
  if (!s?.cwd) return null;
  return { mid, sid, cwd: s.cwd, ...at };
}

/** The names a computer goes by in links to its ports: its host name, when the hub knows it. */
export function machineHostnames(mid: string): string[] {
  const h = getState().machines.find((m) => m.id === mid)?.hostname;
  return h ? [h] : [];
}

/** Whether a port link of an agent's computer is one of this hub's own pages (the hub runs on that computer and the
 *  answer links its /s/…, /todos or /persona/… page; iOS isHubPage, #2696): it is no dev server, so it gets no chip. */
export function isHubPortLink(l: PortLink, hostnames: readonly string[]): boolean {
  const scheme = l.secure ? "https" : "http";
  return ["127.0.0.1", "localhost", ...hostnames].some((host) => {
    const u = parse(`${scheme}://${host}:${l.port}${l.path}`);
    return !!u && !!hubPage(u, pageOrigin(), BASE);
  });
}

/** The href to put on an <a>: persona://session links become the in-app path, so ⌘-click opens a tab of that page. */
export function linkHref(url: string): string {
  const r = linkRoute(url);
  return r.kind === "session" ? `${BASE}/s/${encodeURIComponent(r.mid)}/${encodeURIComponent(r.sid)}` : url;
}

/** Opens a link where it belongs (see the top of this file). `title` names it in the panel until the page says;
 *  `manager`: the one whose chat it is in (useLinkManager), which reads the document or post it opens in the panel. */
export function openLink(url: string, title?: string, manager?: string) {
  const preview = previewTarget(url);
  if (preview) {
    openPanel({ kind: "preview", ...preview });
    return;
  }
  const r = linkRoute(url);
  switch (r.kind) {
    case "session":
      navigate({ name: "session", mid: r.mid, sid: r.sid });
      return;
    // a page of its own at every width, never a side panel (#5096, cindy's session links); the chat keeps its place
    case "agent":
      leaveChat({ name: "session", mid: r.mid, sid: r.sid }, false);
      return;
    case "task":
      leaveChat(taskRoute(r.id), false);
      return;
    case "conversation":
      navigate({ name: "persona", sid: r.sid, ...(r.mid ? { mid: r.mid } : {}), ...(r.manager ? { manager: r.manager } : {}) });
      return;
    case "doc":
    case "moments":
      if (opensInApp(url)) {
        openPanel({ kind: "link", url, title: title || undefined, manager: otherManager(manager) });
        return;
      }
      window.open(url, "_blank", "noopener,noreferrer");
      return;
    case "web":
      window.open(url, "_blank", "noopener,noreferrer");
      return;
    case "system":
      location.href = url;
      return;
    case "none":
      return;
  }
}

/**
 * onClick for an <a href={linkHref(url)}>: a plain click on a link that opens in the page opens it there; ⌘ / Ctrl /
 * Shift / middle clicks and links that belong in a tab keep the browser's own behavior.
 */
export function clickLink(e: React.MouseEvent, url: string | undefined, title?: string, manager?: string) {
  if (!url || e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
  if (!opensInApp(url)) return;
  e.preventDefault();
  openLink(url, title, manager);
}

// ── preloading (CLAUDE.md: opening speed first) ──────────

let prefetcher: ((url: string, manager?: string) => void) | null = null;

/** The reader registers how to warm a document (views/link/feishu-doc.ts). */
export function setLinkPrefetcher(fn: (url: string, manager?: string) => void) {
  prefetcher = fn;
}

/** A link the pointer rests on: start reading it (as manager, as openLink would) so the panel opens on a finished page. */
export function prefetchLink(url: string | undefined, manager?: string) {
  if (url && opensInApp(url) && linkRoute(url).kind === "doc") prefetcher?.(url, manager);
}

// ── <persona-context> ─────────────────────────────────────

/** A piece of a page the user picked (LinkSheet.swift ContextRef). `text` is "(whole document)" in Chinese for a whole document. */
export interface ContextRef {
  id: string;
  /** "workspace": a folder on a computer picked in the @ menu, carrying `machine` and `cwd` (docs/protocol/protocol.md "Tags in messages").
   *  "forward": a message forwarded from another chat (lib/forward.ts, docs/protocol/protocol.md "Forwarded messages"). */
  kind: "context" | "edit" | "workspace" | "forward";
  title: string;
  url: string;
  text: string;
  /** Extra attributes written on the tag after kind / title / url (a workspace's `machine` and `cwd`, a forward's `msg` and metadata). */
  attrs?: Record<string, string>;
  /** A forward's line under its card over the box: which computer it comes from and where it goes. Shown, never sent. */
  route?: string;
}

// i18n-ignore: a marker inside <persona-context>, the app (QuoteViews.swift) matches it
export const WHOLE = "（整篇）";

// writing the tags: lib/context-xml.ts (no store, no router: New agent and the tests take it alone)
export { contextXML, withContexts } from "./context-xml";

/** "AI edit": the picked text and what to do with it, sent at once (LinkSheet.swift sendEdit). */
export function editMessage(r: Pick<ContextRef, "title" | "url" | "text">, ask: string): string {
  const a = ask.trim();
  return `${contextXML({ ...r, kind: "edit" })}\n\n${a ? t("lib-links.edit_with_ask", { ask: a }) : t("lib-links.edit_plain")}`;
}

export function newContext(kind: ContextRef["kind"], title: string, url: string, text: string): ContextRef {
  return { id: uuid(), kind, title, url, text: text.trim() || WHOLE };
}

// ── contexts waiting in a composer (memory only, per draft key) ──

const pending = new Map<string, ContextRef[]>();
const ctxListeners = new Set<() => void>();
const NONE: ContextRef[] = [];

function emitContexts() {
  ctxListeners.forEach((l) => l());
}

export function getContexts(key: string): ContextRef[] {
  return pending.get(key) ?? NONE;
}

export function addContext(key: string, ref: ContextRef) {
  const cur = getContexts(key);
  // the same passage of the same page once
  if (cur.some((c) => c.url === ref.url && c.text === ref.text && c.kind === ref.kind)) return;
  pending.set(key, [...cur, ref]);
  emitContexts();
}

export function removeContext(key: string, id: string) {
  const next = getContexts(key).filter((c) => c.id !== id);
  if (next.length) pending.set(key, next);
  else pending.delete(key);
  emitContexts();
}

export function useContexts(key: string): ContextRef[] {
  return useSyncExternalStore(
    (l) => {
      ctxListeners.add(l);
      return () => ctxListeners.delete(l);
    },
    () => getContexts(key),
  );
}

// ── the composer being written in ────────────────────────

/** What a mounted composer lets the panel do: its draft key, and sending a message now ("AI edit"). */
export interface ComposerHandle {
  key: string;
  send: (text: string) => Promise<boolean>;
  focus: () => void;
  /** False while it can't send (a read-only source): picked text then goes elsewhere. */
  ready?: () => boolean;
}

let composers: ComposerHandle[] = []; // most recently mounted / focused last
const waiting: { key?: string; run: (c: ComposerHandle) => void }[] = [];

/** A composer mounts: it becomes the active one. Returns the unregister for its cleanup. */
export function registerComposer(h: ComposerHandle): () => void {
  composers = [...composers.filter((c) => c !== h), h];
  for (let i = 0; i < waiting.length; i++) {
    const w = waiting[i];
    if (w.key !== undefined && w.key !== h.key) continue;
    waiting.splice(i--, 1);
    w.run(h);
  }
  return () => {
    composers = composers.filter((c) => c !== h);
  };
}

/** The user went back to a composer (focus): it is the active one again. */
export function activateComposer(h: ComposerHandle) {
  if (composers.includes(h)) composers = [...composers.filter((c) => c !== h), h];
}

export function activeComposer(): ComposerHandle | undefined {
  return composers[composers.length - 1];
}

/** The active composer if it can send now. */
export function usableComposer(): ComposerHandle | undefined {
  const c = activeComposer();
  return c && (c.ready?.() ?? true) ? c : undefined;
}

/** Runs `run` with the composer for `key` once it mounts (now, if it is mounted); after `ms` gives up (`expired`). */
export function whenComposer(key: string | undefined, run: (c: ComposerHandle) => void, expired?: () => void, ms = 8000) {
  const now = key === undefined ? activeComposer() : composers.find((c) => c.key === key);
  if (now) {
    run(now);
    return;
  }
  const w = { key, run };
  waiting.push(w);
  setTimeout(() => {
    const i = waiting.indexOf(w);
    if (i < 0) return;
    waiting.splice(i, 1);
    expired?.();
  }, ms);
}
