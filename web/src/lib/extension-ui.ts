import { canonicalAppURL } from "./compatibility";
// An extension's declarative UI (docs/extensions/extensions.md "Declarative UI"): the extension sends a View (a few fixed kinds of items), the
// web draws it with its own design system, and buttons go back to the extension. No extension code runs here, no extension
// colors, sizes or icons: a tone becomes one of the theme's role colors. The checks match server/internal/extension/ui.go
// (the hub refuses a bad view before it gets here; the web still draws only what it knows). UI version 2
// (docs/extensions/extensions.md "UI slots") adds the slots (nav_page, overview_card, machine_section), chart, stat_grid, segmented,
// refresh_after and the render context; an item type the web does not know is skipped, never a white page. An
// inbox_item (docs/extensions/extensions.md "Inbox items") draws the body of an inbox item from the extension's own connector.
import { api, ApiError, enc } from "./api";
import { itemHeaders } from "./manager-choice";
import { extRoot } from "./extension-api";
import { areaBands, colorOf, hue, imageLoadable, readV6 } from "./extension-ui-v6";
import { isHttps, isObject, readAction, readPush } from "./extension-ui-read";
import { t } from "../i18n";
import type { Comment, Hunk, Thread, TreeEntry } from "./code-view";
import type { UIColor, UIImageSource, UIRowAction, UIRowControl, UISpark, UITile } from "./extension-ui-v6";
import { SUBSCRIPTIONS_UI } from "./subscriptions-ui";

export { isHttps };

/** The UI version the web draws (context.ui_version). */
export const UI_VERSION = 6;

export type UITone = "neutral" | "positive" | "caution" | "danger";

export interface UIAction {
  id: string;
  /** Words to confirm with first; empty: no confirmation. */
  confirm?: string;
  /** Send every field's value along. */
  submit?: boolean;
  /** UI version 3: open another page of the extension on top of this one, without asking the extension first. */
  push?: UIPush;
  /** UI version 4: values the action carries, sent with the fields' and the component's (these win on a name). */
  values?: Record<string, string>;
}

/** A page pushed on the navigation stack (docs/extensions/extensions.md "Page navigation"): drawn with this state, by this ui (the same one
 *  when empty); the title shows until its view says its own. */
export interface UIPush {
  state?: string;
  title?: string;
  ui?: string;
}

export interface UIChoice {
  value: string;
  title?: string;
}

/** One item of a section; which fields it has depends on `type`. */
export interface UIItem {
  type: "text" | "metric" | "progress" | "row" | "button" | "link" | "field" | "divider" | "chart" | "stat_grid" | "segmented" | string;
  text?: string;
  /** text: body | secondary | caption | code */
  style?: string;
  label?: string;
  value?: string;
  detail?: string;
  tone?: UITone;
  ratio?: number;
  title?: string;
  subtitle?: string;
  badge?: { text: string; tone?: UITone; color?: UIColor };
  action?: UIAction;
  /** button: normal | destructive */
  role?: string;
  url?: string;
  /** field, segmented */
  name?: string;
  /** field: text | secret | number | toggle | choice; chart: bar | line */
  kind?: "text" | "secret" | "number" | "toggle" | "choice" | "bar" | "line" | string;
  choices?: UIChoice[];
  placeholder?: string;
  /** chart: its series (the same labels, in the same order), the unit of the values, bars stacked */
  series?: ChartSeries[];
  unit?: string;
  stacked?: boolean;
  /** stat_grid */
  stats?: UIStat[];
  /** UI version 3. Any item: what it is in this view, kept across draws (the list keeps its place, an item its state). */
  key?: string;
  /** row: one of an icon (ROW_ICONS), a picture with initials to fall back on, a thumbnail; empty: also its icon */
  icon?: string;
  avatar?: { url?: string; text?: string; ref?: string; path?: string };
  thumbnail?: { url?: string; aspect?: "square" | "wide" | string; ref?: string; path?: string };
  /** table */
  columns?: { title: string; align?: "start" | "end" | string }[];
  rows?: string[][];
  caption?: string;
  /** empty: a button to do something about it */
  button?: { title: string; action: UIAction };
  /** UI version 4 (#2951): header */
  badges?: { text: string; tone?: UITone }[];
  facts?: { label?: string; value: string; icon?: string }[];
  /** breadcrumb */
  crumbs?: { title: string; action?: UIAction }[];
  /** file_tree: its entries, the one selected; expand loads a directory given with nothing under it */
  entries?: TreeEntry[];
  selected?: string;
  expand?: UIAction;
  /** code, diff */
  path?: string;
  language?: string;
  start_line?: number;
  highlight?: { start: number; end: number };
  truncated?: boolean;
  load?: UIAction;
  old_path?: string;
  status?: string;
  additions?: number;
  deletions?: number;
  binary?: boolean;
  collapsed?: boolean;
  hunks?: Hunk[];
  threads?: Thread[];
  comment?: UIAction;
  /** comment_thread */
  comments?: Comment[];
  resolved?: boolean;
  reply?: UIAction;
  /** composer */
  buttons?: { title: string; action: UIAction; tone?: UITone }[];
  /** UI version 5 (#3136), diff: hidden lines asked for, viewed marks, generated files, drafts, suggestions */
  context?: UIAction;
  viewed?: boolean;
  mark_viewed?: UIAction;
  revision?: string;
  generated?: boolean;
  similarity?: number;
  draft?: UIAction;
  suggestions?: boolean;
  /** code: a link to lines on the site, {start} and {end} filled in */
  line_url?: string;
  /** UI version 6: image */
  src?: UIImageSource;
  alt?: string;
  fit?: "fill" | "fit" | string;
  no_zoom?: boolean;
  /** image: square | landscape | wide | portrait | auto */
  aspect?: string;
  /** grid, carousel */
  tiles?: UITile[];
  max_columns?: number;
  /** carousel: narrow | regular | wide */
  width?: string;
  /** row: a switch, slider or stepper; its extra actions */
  control?: UIRowControl;
  actions?: UIRowAction[];
  /** row, metric: a small trend line; row, metric, empty: the icon's color */
  spark?: UISpark;
  icon_color?: UIColor;
  /** chart: a donut's words in the hole, a candlestick's rising color */
  center?: string;
  rising?: "green" | "red" | string;
  /** field: date, time, datetime limits */
  earliest?: string;
  latest?: string;
}

/** A view's tabs on top (UI version 4): choosing one sends {name: value} (with the search's q) at once. */
export interface UITabs {
  name: string;
  value: string;
  items: { value: string; title?: string; badge?: string }[];
  action: UIAction;
}

/** A view's search box (UI version 3): what the user types goes to the extension as `q`, 0.3 s after they stop. */
export interface UISearch {
  placeholder?: string;
  value?: string;
  action: UIAction;
}

/** A view's filter next to its search box: one of a few, sent as `{name: value}` (with the search's `q`) at once. */
export interface UIFilter {
  name: string;
  title?: string;
  choices: UIChoice[];
  value?: string;
  action: UIAction;
}

/** A section's last line: more of the same, asked for when it scrolls into view (once) or when pressed. */
export interface UIMore {
  title?: string;
  action: UIAction;
}

/** One line, or one color of bars; no tone: the web's own series colors, in order. */
export interface ChartSeries {
  name?: string;
  tone?: UITone;
  /** UI version 6: a named color in place of the series palette */
  color?: UIColor;
  points: ChartPoint[];
}

/** A value at a label; `display` is how to write it ("¥1.20"), else the web writes the value with the chart's unit. */
export interface ChartPoint {
  label: string;
  value: number;
  display?: string;
  /** UI version 6, candlestick: the day's open, high, low, close (value is the close) and volume */
  open?: number;
  high?: number;
  low?: number;
  close?: number;
  volume?: number;
}

/** One number of a stat_grid; the tone colors its detail. */
export interface UIStat {
  label: string;
  value: string;
  detail?: string;
  tone?: UITone;
  /** UI version 6 */
  spark?: UISpark;
}

export interface UISection {
  title?: string;
  footer?: string;
  items: UIItem[];
  key?: string;
  more?: UIMore;
  /** UI version 4: in a split view, side is the column at the start (the file tree) */
  pane?: "main" | "side" | string;
}

export interface UIView {
  title?: string;
  sections: UISection[];
  /** The extension's own, sent back as it is. */
  state?: string;
  /** Draw again after this many seconds while on screen (30 at least, a day at most; 0 never). */
  refresh_after?: number;
  search?: UISearch;
  filter?: UIFilter;
  /** UI version 4 */
  tabs?: UITabs;
  layout?: "stack" | "split" | string;
  /** UI version 5: the pending review's bar */
  review?: UIReview;
}

/** The pending review (UI version 5): how many drafts, a summary sent as name with the button pressed, discard. */
export interface UIReview {
  count: number;
  name: string;
  placeholder?: string;
  buttons: { title: string; action: UIAction; tone?: UITone }[];
  discard?: UIAction;
  /** The summary already written (a session review whose send failed, #151); not sent by extensions. */
  value?: string;
}

/** Where an extension draws (GET /api/v1/extensions/-/ui, and an extension detail's `ui`). */
export interface UIPlace {
  extension: string;
  /** The extension's name (UI version 2). */
  extension_name?: string;
  id: string;
  title: string;
  placement: "plugin_page" | "launcher_section" | "nav_page" | "overview_card" | "machine_section" | "inbox_item" | string;
  /** compact (a card) or full (a page, a section). */
  size?: "compact" | "full" | string;
  /** A nav_page's icon, from UI_ICONS. */
  icon?: string;
  /** The page (nav_page or plugin_page of the same extension) an overview_card opens. */
  opens?: string;
  /** On a nav_page's entry; updated by the plugin_ui event. */
  badge?: UIBadge | null;
  /** The connector whose inbox items an inbox_item draws (the items' `source`). */
  connector?: string;
}

export interface UIBadge {
  text: string;
  tone?: UITone;
}

/** What a button's press answered: a new view, a word, a link; nothing at all: draw again. */
export interface UIActionResult {
  view?: UIView;
  toast?: string;
  open_url?: string;
  /** UI version 3: open another page (a saved form's detail), or go back a page (the one below is drawn again). */
  push?: UIPush;
  pop?: boolean;
  /** UI version 5: lines of a file a diff's context asked for; components in place of those with the same key */
  lines?: { path: string; side?: string; start: number; lines: string[]; total?: number };
  items?: UIItem[];
}

/** Where a view is drawn: the launcher (launcher_section), the machine (machine_section), the inbox item (inbox_item), the size (a card). */
export interface UIContext {
  launcher?: string;
  machine?: string;
  item?: string;
  size?: "compact" | "full";
}

/** What the web says with every render and action (docs/extensions/extensions.md "Context"): time zone, language, platform, UI version. */
export interface UIContextSent extends UIContext {
  tz?: string;
  locale?: string;
  platform: "web";
  ui_version: number;
}

/** The context sent: the place's, with the browser's time zone and language. */
export function uiContext(context?: UIContext): UIContextSent {
  let tz: string | undefined;
  try {
    tz = Intl.DateTimeFormat().resolvedOptions().timeZone || undefined;
  } catch {
    tz = undefined;
  }
  const locale = typeof navigator !== "undefined" ? navigator.language || undefined : undefined;
  const out: UIContextSent = { platform: "web", ui_version: UI_VERSION };
  if (tz) out.tz = tz;
  if (locale) out.locale = locale;
  if (context?.launcher) out.launcher = context.launcher;
  if (context?.machine) out.machine = context.machine;
  if (context?.item) out.item = context.item;
  if (context?.size) out.size = context.size;
  return out;
}

// ── the hub ─────────────────────────────────────────────

/** The extensions' places of a placement; no placement: every slot's but launcher sections' (nav, overview, machines).
 *  `manager`: whose extensions (several managers, docs/assistant/managers.md "Clients"); none: the default's. */
export function uiPlaces(placement: string, launcher?: string, machine?: string, manager?: string): Promise<UIPlace[]> {
  const q = new URLSearchParams(placement ? { placement } : {});
  if (launcher) q.set("launcher", launcher);
  if (machine) q.set("machine", machine);
  return api<{ ui?: UIPlace[] }>(`${extRoot()}/-/ui${q.size ? `?${q}` : ""}`, { headers: itemHeaders(manager) }).then((r) => (Array.isArray(r.ui) ? r.ui.map(placeOf) : []));
}

/** A place as either kind of hub lists it: the extension's id and name under the new keys (an older hub says plugin,
 *  plugin_name). */
export function placeOf(u: UIPlace & { plugin?: string; plugin_name?: string }): UIPlace {
  const { plugin, plugin_name, ...rest } = u;
  return { ...rest, extension: u.extension ?? plugin ?? "", ...((u.extension_name ?? plugin_name) ? { extension_name: u.extension_name ?? plugin_name } : {}) };
}

// The subscription pages (#2041) are the manager's own, drawn and pressed like an extension's page under a reserved id.
const uiPath = (extension: string, ui: string) =>
  extension === SUBSCRIPTIONS_UI ? "/subscriptions/-/ui" : `${extRoot()}/${enc(extension)}/-/ui/${enc(ui)}`;

/** Draws the view (no action). A view that does not read at all is an error; unknown items are left out. `manager`:
 *  whose extension it is (another manager's inbox item, or computer); none: the default's. */
export const renderUI = (extension: string, ui: string, context?: UIContext, state?: string, manager?: string) =>
  api<{ view: unknown }>(uiPath(extension, ui), { method: "POST", json: { context: uiContext(context), ...(state ? { state } : {}) }, headers: itemHeaders(manager) }).then((r) => readView(r.view));

/** A press of a button (or a row): the action's id, the view's state, and the fields' values when it submits. */
export const actUI = (extension: string, ui: string, action: string, opts: { context?: UIContext; state?: string; values?: Record<string, string>; manager?: string } = {}) =>
  api<UIActionResult>(uiPath(extension, ui), {
    method: "POST",
    json: { context: uiContext(opts.context), ...(opts.state ? { state: opts.state } : {}), action, ...(opts.values ? { values: opts.values } : {}) },
    headers: itemHeaders(opts.manager),
  }).then((r) => ({
    ...r,
    view: r.view ? readView(r.view) : undefined,
    // UI version 5: components in place, read as a view's are
    items: Array.isArray(r.items) ? readView({ sections: [{ items: r.items }] }).sections[0]?.items.filter((it) => it.key) : undefined,
  }));

/**
 * The extension said this is not its gateway (a launcher_section without `hosts` is listed on every launcher; docs/extensions/extensions.md
 * "Declarative UI"): 404 {code: "unsupported"}. The section is hidden, not an error.
 */
export const isUnsupported = (e: unknown) => e instanceof ApiError && e.status === 404 && e.code === "unsupported";

// ── reading a view ──────────────────────────────────────

const TONES: readonly string[] = ["neutral", "positive", "caution", "danger"];

/** A tone the web knows, else neutral. */
export const toneOf = (t?: string): UITone => (t && TONES.includes(t) ? (t as UITone) : "neutral");

/** A ratio as a percentage the bar can draw: 0…100, whatever the extension sent. */
export function percent(ratio?: number): number {
  if (typeof ratio !== "number" || !Number.isFinite(ratio)) return 0;
  return Math.round(Math.min(1, Math.max(0, ratio)) * 1000) / 10;
}

/** The fields of a view, by name, as they start: what the extension filled in (never a secret's), toggles "true" / "false". */
export function fieldValues(view: UIView | null | undefined): Record<string, string> {
  const out: Record<string, string> = {};
  for (const s of view?.sections ?? []) {
    for (const it of s.items ?? []) {
      if (it.type !== "field" || !it.name) continue;
      if (it.kind === "secret") out[it.name] = "";
      else if (it.kind === "toggle") out[it.name] = it.value === "true" ? "true" : "false";
      else if (it.kind === "choice") out[it.name] = it.value ?? it.choices?.[0]?.value ?? "";
      else if (it.kind === "multichoice") out[it.name] = it.value ?? "[]";
      else out[it.name] = it.value ?? "";
    }
    // a segmented is a field chosen at once: a submitting button sends its choice too
    for (const it of s.items ?? []) {
      if (it.type === "segmented" && it.name) out[it.name] = it.value ?? it.choices?.[0]?.value ?? "";
    }
  }
  return out;
}

/** The values a submitting button sends: every field of the view, in the order drawn. */
export function submitValues(view: UIView | null | undefined, values: Record<string, string>): Record<string, string> {
  const start = fieldValues(view);
  const out: Record<string, string> = {};
  for (const k of Object.keys(start)) out[k] = values[k] ?? start[k];
  return out;
}

/** A number field typed into that does not read as a number. */
export function numberProblem(text: string): string {
  const s = text.trim();
  return s && !Number.isFinite(Number(s)) ? t("lib-extension-ui.not_a_number") : "";
}

/** How a section lays out: metrics side by side, buttons in one row, everything else on its own line. */
export type UIBlock = { kind: "metrics"; items: UIItem[] } | { kind: "buttons"; items: UIItem[] } | { kind: "item"; item: UIItem };

export function blocks(items: UIItem[]): UIBlock[] {
  const out: UIBlock[] = [];
  for (const it of items) {
    const last = out[out.length - 1];
    if (it.type === "metric" || it.type === "button") {
      const kind = it.type === "metric" ? "metrics" : "buttons";
      if (last && last.kind === kind) last.items.push(it);
      else out.push({ kind, items: [it] });
    } else out.push({ kind: "item", item: it });
  }
  return out;
}

/**
 * What to do after a press: the view to show (new, or "reload" to draw again), a word to say, a link to open; `source`:
 * open the inbox item's own source page (an inbox_item's open_url persona://inbox/<item>/source, for the item it is
 * drawn for); `settings`: the in-app path of an extension's own settings (open_url persona://settings/extensions/<id>).
 */
export function outcome(
  r: UIActionResult,
  item?: string,
): { view?: UIView; items?: UIItem[]; reload: boolean; toast?: string; url?: string; source?: true; settings?: string; push?: UIPush; pop?: true } {
  const url = r.open_url && isHttps(r.open_url) ? r.open_url : undefined;
  const source = !!item && !!r.open_url && inboxSourceItem(r.open_url) === item;
  const settings = r.open_url ? extensionSettingsHref(r.open_url) : undefined;
  const push = readPush(r.push);
  const pop = r.pop === true && !push;
  return {
    view: r.view,
    ...(r.items?.length ? { items: r.items } : {}),
    // UI version 5: lines for a diff, or components in place, say enough
    reload: !r.view && !r.toast && !r.open_url && !push && !pop && !r.items?.length && !r.lines,
    toast: r.toast || undefined,
    url,
    ...(source ? { source: true as const } : {}),
    ...(settings ? { settings } : {}),
    ...(push ? { push } : {}),
    ...(pop ? { pop: true as const } : {}),
  };
}

/** How deep the pages pushed on one another go (docs/extensions/extensions.md "Page navigation"): a push past it replaces the top one. */
export const MAX_STACK = 8;

/** One page of the navigation stack: drawn by `ui` with `state`; `title` until the view says its own. */
export interface UIPage {
  ui: string;
  state?: string;
  title?: string;
}

/** The stack after a push from the top page of `ui`. */
export function pushPage(stack: readonly UIPage[], push: UIPush, ui: string): UIPage[] {
  const page: UIPage = { ui: push.ui || ui, ...(push.state !== undefined ? { state: push.state } : {}), ...(push.title ? { title: push.title } : {}) };
  const out = [...stack, page];
  return out.length > MAX_STACK ? [...out.slice(0, MAX_STACK - 1), page] : out;
}

/** The icons UI version 6 adds (docs/extensions/extensions.md "UI version 6"), on a row, a metric, a tile, a row action and a nav_page. */
export const NEW_ICONS_6 = [
  "home", "sofa", "light", "power", "fan", "thermometer", "droplet", "lock", "unlock", "door", "window", "camera", "video", "play",
  "pause", "stop", "speaker", "music", "tv", "plug", "battery", "bolt", "sun", "moon", "cloud", "rain", "snow", "wind", "leaf", "car",
  "location", "robot", "wifi", "shield", "trend_up", "trend_down", "wallet", "bank", "heart", "plus", "minus", "trash", "edit",
  "refresh", "share", "info",
] as const;

/** The fixed icons a row may have (docs/extensions/extensions.md "Declarative UI"): each app draws its own; unknown ones are left out. */
export const ROW_ICONS = [
  "extension", "plugin", "chart", "gauge", "list", "bell", "doc", "calendar", "money", "person", "chat", "mail", "link", "folder",
  "star", "check", "warning", "clock", "tag", "search", "settings", "branch", "commit", "code", "pull_request", "merge", "file",
  // UI version 6: the home's and the markets' meanings
  ...NEW_ICONS_6,
] as const;
export type RowIcon = (typeof ROW_ICONS)[number];

/** A row's icon the web knows; undefined (no icon) for any other. */
export const rowIcon = (name?: string): RowIcon | undefined => ((ROW_ICONS as readonly string[]).includes(name ?? "") ? (name as RowIcon) : undefined);

/** Only https pictures load (no credentials go with them). */
export const imageURL = (url?: string): string | undefined => (url && isHttps(url) ? url : undefined);

/** The one or two letters a picture falls back on: the extension's, else the first letter of the title. */
export function initials(text?: string, title?: string): string {
  const t = (text ?? "").trim();
  if (t) return Array.from(t).slice(0, 2).join("");
  const first = Array.from((title ?? "").trim())[0];
  return first ? first.toUpperCase() : "";
}

/** A table's cells, as many as it has columns (missing ones blank, extra ones dropped), at most 100 rows. */
export function tableRows(item: UIItem): string[][] {
  const n = item.columns?.length ?? 0;
  return (item.rows ?? []).slice(0, 100).map((r) => Array.from({ length: n }, (_, i) => (typeof r[i] === "string" ? r[i] : r[i] == null ? "" : String(r[i]))));
}

/**
 * The item an inbox item's source link names (persona://inbox/<item>/source, the id escaped as one path segment, as
 * docs/extensions/extensions.md "Inbox items" says); undefined for any other link.
 */
export function inboxSourceItem(url: string): string | undefined {
  const m = /^persona:\/\/inbox\/([^/?#]+)\/source$/.exec(canonicalAppURL(url));
  if (!m) return undefined;
  try {
    return decodeURIComponent(m[1]);
  } catch {
    return undefined;
  }
}

/**
 * The in-app path of an extension's settings page that persona://settings/extensions/<id> names (the id escaped as one
 * path segment): /settings?tab=extensions&extension=<id>. undefined for any other link, and for an id the hub keeps for
 * itself (one starting with "-") or one with a "/".
 */
export function extensionSettingsHref(url: string): string | undefined {
  const m = /^persona:\/\/settings\/extensions\/([^/?#]+)$/.exec(canonicalAppURL(url));
  if (!m) return undefined;
  let id: string;
  try {
    id = decodeURIComponent(m[1]);
  } catch {
    return undefined;
  }
  if (!id || id.startsWith("-") || id.includes("/")) return undefined;
  return `/settings?tab=extensions&extension=${encodeURIComponent(id)}`;
}

// ── reading what the hub sent (UI version 2) ─────────────

/** The item types the web draws; any other is skipped (an extension newer than the web). */
export const KNOWN_ITEMS: readonly string[] = [
  "text", "metric", "progress", "row", "button", "link", "field", "divider", "chart", "stat_grid", "segmented",
  "markdown", "table", "empty", "loading", // UI version 3
  "header", "breadcrumb", "file_tree", "code", "diff", "comment_thread", "composer", // UI version 4
  "image", "grid", "carousel", // UI version 6
];

/** An item the web can draw: a known type with what it needs to be drawn at all (a chart with points, a grid with stats). */
function drawable(it: unknown): it is UIItem {
  if (!isObject(it) || typeof it.type !== "string" || !KNOWN_ITEMS.includes(it.type)) return false;
  switch (it.type) {
    case "chart": {
      const series = it.series;
      return Array.isArray(series) && series.length > 0 && series.every((s) => isObject(s) && Array.isArray(s.points) && s.points.length > 0 && s.points.every((p) => isObject(p) && typeof p.value === "number" && Number.isFinite(p.value)));
    }
    case "stat_grid":
      return Array.isArray(it.stats) && it.stats.length > 0 && it.stats.every(isObject);
    case "segmented":
      return typeof it.name === "string" && !!it.name && isObject(it.action) && Array.isArray(it.choices) && it.choices.length > 0;
    case "markdown":
      return typeof it.text === "string";
    case "table":
      return Array.isArray(it.columns) && it.columns.length > 0 && it.columns.every((c) => isObject(c) && typeof c.title === "string") && Array.isArray(it.rows) && it.rows.every(Array.isArray);
    case "empty":
    case "header":
      return typeof it.title === "string" && !!it.title;
    case "breadcrumb":
      return Array.isArray(it.crumbs) && it.crumbs.length > 0 && it.crumbs.every((c) => isObject(c) && typeof c.title === "string");
    case "file_tree":
      return Array.isArray(it.entries) && it.entries.every((e) => isObject(e) && typeof e.path === "string");
    case "code":
      return typeof it.text === "string";
    case "diff":
      return typeof it.path === "string" && (it.hunks === undefined || (Array.isArray(it.hunks) && it.hunks.every((h) => isObject(h) && Array.isArray(h.lines) && h.lines.every((l) => typeof l === "string"))));
    case "comment_thread":
      return Array.isArray(it.comments) && it.comments.every((c) => isObject(c) && typeof c.author === "string");
    case "composer":
      return typeof it.name === "string" && !!it.name && Array.isArray(it.buttons) && it.buttons.some((b) => isObject(b) && readAction(b.action));
    // UI version 6: a picture that can be loaded (https, or the hub's own path), tiles with titles
    case "image":
      return imageLoadable(it.src as UIImageSource | undefined) && typeof it.alt === "string" && !!it.alt;
    case "grid":
    case "carousel":
      return Array.isArray(it.tiles) && it.tiles.some((t) => isObject(t) && typeof t.title === "string" && !!t.title);
    default:
      return true;
  }
}

/**
 * The view as the web draws it: items it does not know left out, a section they emptied gone (one with a footer
 * stays), like the hub does for an older app. Only a view that does not read at all is an error.
 */
/** `html`: keep html components (an assistant card that runs them, #4215); an extension's view never draws one. */
export function readView(raw: unknown, opts: { html?: boolean } = {}): UIView {
  if (!isObject(raw) || !Array.isArray(raw.sections)) throw new Error(t("lib-extension-ui.unreadable_view"));
  const sections: UISection[] = [];
  // an html component's own checks are the card's (ui/HtmlCard.tsx)
  const keeps = (it: unknown): it is UIItem => drawable(it) || (!!opts.html && (it as UIItem)?.type === "html");
  for (const s of raw.sections) {
    if (!isObject(s)) continue;
    const items = Array.isArray(s.items) ? s.items : [];
    const kept = items.filter(keeps).map(readItem);
    const footer = typeof s.footer === "string" ? s.footer : undefined;
    const more = isObject(s.more) && readAction(s.more.action) ? { ...(typeof s.more.title === "string" && s.more.title ? { title: s.more.title } : {}), action: readAction(s.more.action)! } : undefined;
    if (items.length > 0 && kept.length === 0 && !footer && !more) continue;
    sections.push({
      ...(typeof s.title === "string" ? { title: s.title } : {}),
      ...(footer ? { footer } : {}),
      items: kept,
      ...(typeof s.key === "string" && s.key ? { key: s.key } : {}),
      ...(more ? { more } : {}),
      ...(s.pane === "side" ? { pane: "side" } : {}),
    });
  }
  const view: UIView = { sections };
  if (typeof raw.title === "string") view.title = raw.title;
  if (typeof raw.state === "string") view.state = raw.state;
  if (typeof raw.refresh_after === "number") view.refresh_after = raw.refresh_after;
  if (isObject(raw.search) && readAction(raw.search.action)) {
    view.search = {
      action: readAction(raw.search.action)!,
      ...(typeof raw.search.placeholder === "string" ? { placeholder: raw.search.placeholder } : {}),
      ...(typeof raw.search.value === "string" ? { value: raw.search.value } : {}),
    };
  }
  if (raw.layout === "split") view.layout = "split";
  const tb = raw.tabs;
  if (isObject(tb) && typeof tb.name === "string" && tb.name && readAction(tb.action) && Array.isArray(tb.items)) {
    const items = tb.items.flatMap((i) =>
      isObject(i) && typeof i.value === "string" && i.value
        ? [{ value: i.value, ...(typeof i.title === "string" ? { title: i.title } : {}), ...(typeof i.badge === "string" && i.badge ? { badge: i.badge } : {}) }]
        : [],
    );
    if (items.length > 1) view.tabs = { name: tb.name, value: typeof tb.value === "string" ? tb.value : items[0].value, items, action: readAction(tb.action)! };
  }
  // UI version 5: the pending review's bar
  const rv = raw.review;
  if (isObject(rv) && typeof rv.name === "string" && rv.name && Array.isArray(rv.buttons)) {
    const buttons = rv.buttons.flatMap((b) => (isObject(b) && typeof b.title === "string" && readAction(b.action) ? [{ title: b.title, action: readAction(b.action)!, ...(typeof b.tone === "string" ? { tone: b.tone as UITone } : {}) }] : []));
    if (buttons.length)
      view.review = {
        count: typeof rv.count === "number" && rv.count > 0 ? Math.floor(rv.count) : 0,
        name: rv.name,
        buttons,
        ...(typeof rv.placeholder === "string" ? { placeholder: rv.placeholder } : {}),
        ...(readAction(rv.discard) ? { discard: readAction(rv.discard) } : {}),
      };
  }
  const f = raw.filter;
  if (isObject(f) && typeof f.name === "string" && f.name && readAction(f.action) && Array.isArray(f.choices)) {
    const choices = f.choices.filter((c): c is UIChoice => isObject(c) && typeof c.value === "string");
    if (choices.length > 1) {
      view.filter = {
        name: f.name,
        choices,
        action: readAction(f.action)!,
        ...(typeof f.title === "string" ? { title: f.title } : {}),
        ...(typeof f.value === "string" ? { value: f.value } : {}),
      };
    }
  }
  return view;
}

/** An item with its actions read; an empty's button (`action: {title, action}`) under `button`. */
function readItem(it: UIItem): UIItem {
  const raw = it as unknown as Record<string, unknown>;
  if (it.type === "empty") {
    const b = raw.button ?? raw.action;
    const out: UIItem = { ...it, action: undefined, button: undefined };
    if (isObject(b) && typeof b.title === "string" && readAction(b.action)) out.button = { title: b.title, action: readAction(b.action)! };
    return out;
  }
  const out: UIItem = { ...it };
  if (isObject(raw.action)) out.action = readAction(raw.action);
  // UI version 4: the other places an action sits, each read the same way
  for (const k of ["expand", "load", "comment", "reply", "context", "mark_viewed", "draft"] as const) if (k in raw) out[k] = readAction(raw[k]);
  // UI version 6: a row's control and extra actions, a grid's or a carousel's tiles
  readV6(out as unknown as Record<string, unknown>, raw);
  if ("line_url" in raw && !isHttps(String(raw.line_url).replaceAll("{start}", "1").replaceAll("{end}", "1"))) delete out.line_url;
  if (Array.isArray(raw.crumbs)) out.crumbs = (raw.crumbs as Record<string, unknown>[]).map((c) => ({ title: String(c.title), ...(readAction(c.action) ? { action: readAction(c.action) } : {}) }));
  if (Array.isArray(raw.buttons))
    out.buttons = (raw.buttons as unknown[]).flatMap((b) => (isObject(b) && typeof b.title === "string" && readAction(b.action) ? [{ title: b.title, action: readAction(b.action)!, ...(typeof b.tone === "string" ? { tone: b.tone as UITone } : {}) }] : []));
  if (Array.isArray(raw.threads))
    out.threads = (raw.threads as unknown[]).flatMap((th) => (isObject(th) && Array.isArray(th.comments) && typeof th.line === "number" ? [{ ...(th as unknown as Thread), reply: readAction(th.reply), resolve: readAction(th.resolve) }] : []));
  return out;
}

/** How long until a view is drawn again, in ms: refresh_after held to 30 s … a day; 0 never. */
export function refreshDelay(view: UIView | null | undefined): number {
  const s = view?.refresh_after;
  if (typeof s !== "number" || !Number.isFinite(s) || s <= 0) return 0;
  return Math.min(86400, Math.max(30, s)) * 1000;
}

// ── slots ───────────────────────────────────────────────

/** A nav_page's icons (docs/extensions/extensions.md "UI slots"); each app draws its own. `plugin` is `extension` under its name
 *  before #1844 (the hub still sends it). */
export const UI_ICONS = ["extension", "plugin", "chart", "gauge", "list", "bell", "doc", "calendar", "money", "branch", "code", "pull_request", "merge", ...NEW_ICONS_6] as const;
export type UIIcon = (typeof UI_ICONS)[number];

/** An icon the web knows, else extension. */
export const uiIcon = (name?: string): UIIcon => ((UI_ICONS as readonly string[]).includes(name ?? "") ? (name as UIIcon) : "extension");

/** The slots the web draws, besides an extension's own pages in its settings. */
export const SLOTS = ["nav_page", "overview_card", "machine_section", "inbox_item"] as const;
export type SlotName = (typeof SLOTS)[number];

/** The places of one placement, in the hub's order; a placement the web does not know is never asked for. */
export const placesOf = (list: readonly UIPlace[], placement: SlotName) => list.filter((u) => u.placement === placement);

/**
 * The extension page that draws an inbox item's body (inbox_item): the one whose connector brought the item in (its
 * `source`). None: the item's own detail.
 */
export const inboxItemPlace = (list: readonly UIPlace[], source?: string): UIPlace | undefined =>
  source ? list.find((u) => u.placement === "inbox_item" && !!u.connector && u.connector === source) : undefined;

/** The plugin_ui event: a nav_page's badge changed (null clears it). */
export interface BadgeEvent {
  extension: string;
  id: string;
  badge?: UIBadge | null;
}

/** The list with one badge changed; the same list when nothing did. */
export function applyBadge(list: UIPlace[], ev: BadgeEvent): UIPlace[] {
  const badge = ev.badge && typeof ev.badge.text === "string" && ev.badge.text ? { text: ev.badge.text, ...(ev.badge.tone ? { tone: ev.badge.tone } : {}) } : null;
  let changed = false;
  const out = list.map((u) => {
    if (u.extension !== ev.extension || u.id !== ev.id) return u;
    if ((u.badge?.text ?? "") === (badge?.text ?? "") && (u.badge?.tone ?? "") === (badge?.tone ?? "")) return u;
    changed = true;
    return { ...u, badge };
  });
  return changed ? out : list;
}

// ── charts ──────────────────────────────────────────────

/** A number written short: 950, 1.2K, 3.4M, 2B; small ones with at most two decimals. */
export function compactNumber(v: number): string {
  if (!Number.isFinite(v)) return "—";
  const a = Math.abs(v);
  const sign = v < 0 ? "-" : "";
  const short = (x: number) => {
    const d = x >= 100 ? 0 : x >= 10 ? 1 : 2;
    return String(Number(x.toFixed(d)));
  };
  // a value that rounds up into the next unit is written in it (999,950 → 1M, not 1000K)
  for (const [n, u] of [[1e12, "T"], [1e9, "B"], [1e6, "M"], [1e3, "K"]] as const) {
    if (a >= n * 0.9995) return sign + short(a / n) + u;
  }
  return sign + short(a);
}

/** A point's value as the chart writes it: the extension's display, else a short number with the unit. */
export const pointText = (p: ChartPoint, unit?: string) => p.display || `${compactNumber(p.value)}${unit ? ` ${unit}` : ""}`;

/** A linear scale from a domain to a range. */
export const linear = (d0: number, d1: number, r0: number, r1: number) => (v: number) => (d1 === d0 ? (r0 + r1) / 2 : r0 + ((v - d0) / (d1 - d0)) * (r1 - r0));

/**
 * Round ticks around the values, zero always in (bars grow from it): the axis's bottom, top and its ticks. `zero`
 * false (UI version 6: a candlestick's prices): only around the values.
 */
export function niceTicks(min: number, max: number, count = 4, zero = true): { lo: number; hi: number; ticks: number[] } {
  let lo = zero ? Math.min(0, min) : min;
  let hi = zero ? Math.max(0, max) : max;
  if (!Number.isFinite(lo) || !Number.isFinite(hi)) return { lo: 0, hi: 1, ticks: [0, 1] };
  if (hi === lo) hi = lo + 1;
  const raw = (hi - lo) / Math.max(1, count);
  const mag = 10 ** Math.floor(Math.log10(raw));
  const step = ([1, 2, 2.5, 5, 10].find((m) => m * mag >= raw - 1e-12) ?? 10) * mag;
  lo = Math.floor(lo / step + 1e-9) * step;
  hi = Math.ceil(hi / step - 1e-9) * step;
  const ticks: number[] = [];
  for (let v = lo; v <= hi + step / 2; v += step) ticks.push(Number(v.toPrecision(12)));
  return { lo, hi, ticks };
}

/** Each label's span of the bar (or point) at a label; stacked: positives on positives, negatives under negatives. */
export interface BarSpan {
  series: number;
  from: number;
  to: number;
  /** The outermost of its stack (or any grouped bar): its free end is rounded. */
  end: boolean;
}

export function barSpans(series: ChartSeries[], stacked: boolean): BarSpan[][] {
  const n = series[0]?.points.length ?? 0;
  const out: BarSpan[][] = [];
  for (let i = 0; i < n; i++) {
    const col: BarSpan[] = [];
    let up = 0;
    let down = 0;
    series.forEach((s, k) => {
      const v = s.points[i]?.value ?? 0;
      if (!stacked) col.push({ series: k, from: 0, to: v, end: true });
      else if (v >= 0) col.push({ series: k, from: up, to: (up += v), end: false });
      else col.push({ series: k, from: down, to: (down += v), end: false });
    });
    if (stacked) {
      // the last non-empty span on each side carries the rounded end
      const top = col.filter((b) => b.to > b.from).pop();
      const bottom = col.filter((b) => b.to < b.from).pop();
      if (top) top.end = true;
      if (bottom) bottom.end = true;
    }
    out.push(col);
  }
  return out;
}

/** The lowest and highest a chart reaches (stacked: the sums). */
export function chartExtent(series: ChartSeries[], kind: string, stacked: boolean): [number, number] {
  const vals =
    kind === "bar" && stacked
      ? barSpans(series, true).flatMap((c) => c.map((b) => b.to))
      : kind === "area" && stacked
        ? areaBands(series, true).flatMap((s) => s.map((b) => b.to))
        : series.flatMap((s) => s.points.map((p) => p.value));
  return vals.length ? [Math.min(...vals), Math.max(...vals)] : [0, 1];
}

/** About how wide a label is drawn, in px: CJK one em, the rest about 0.6 em. */
export function labelWidth(text: string, px: number): number {
  let w = 0;
  for (const ch of text) w += /[\u2e80-\u9fff\uac00-\ud7af\uff00-\uffef]/.test(ch) ? px : px * 0.6;
  return w;
}

/**
 * The labels to write under a chart so none overlap: every k-th (the smallest k that fits), the last one too when it
 * fits (else it takes the place of the one before). `xs` are their centers; the first and last are drawn flush with the
 * edges of [0, width].
 */
export function thinLabels(labels: string[], xs: number[], width: number, px = 10.5, gap = 8): number[] {
  const n = labels.length;
  if (!n) return [];
  const span = (i: number): [number, number] => {
    const w = labelWidth(labels[i], px);
    const a = Math.min(Math.max(0, xs[i] - w / 2), Math.max(0, width - w));
    return [a, a + w];
  };
  const fits = (list: number[]) => list.every((i, j) => j === 0 || span(list[j - 1])[1] + gap <= span(i)[0]);
  for (let k = 1; k <= n; k++) {
    const list: number[] = [];
    for (let i = 0; i < n; i += k) list.push(i);
    if (!fits(list)) continue;
    if (list[list.length - 1] !== n - 1) {
      const withLast = [...list, n - 1];
      if (fits(withLast)) return withLast;
      const swapped = [...list.slice(0, -1), n - 1];
      if (fits(swapped)) return swapped;
    }
    return list;
  }
  return [n - 1];
}

/** A compact chart writes only the first and the last label, when both fit; else the last. */
export function edgeLabels(labels: string[], width: number, px = 10.5, gap = 8): number[] {
  const n = labels.length;
  if (n <= 1) return n ? [0] : [];
  return labelWidth(labels[0], px) + labelWidth(labels[n - 1], px) + gap <= width ? [0, n - 1] : [n - 1];
}

const TONE_COLOR: Record<UITone, string> = { neutral: "var(--ink-3)", positive: "var(--green)", caution: "var(--orange)", danger: "var(--red)" };

/**
 * The colors of a chart's series: a tone is its role color (an unknown one neutral); the rest take the web's series
 * colors in order (the first the main one), counted among the series without a tone.
 */
export function seriesColors(series: ChartSeries[]): string[] {
  let slot = 0;
  // UI version 6: a named color is the web's token of it (an unknown one takes the next of the palette)
  return series.map((s) => (colorOf(s.color) ? hue(s.color) : s.tone ? TONE_COLOR[toneOf(s.tone)] : `var(--series-${(slot++ % 4) + 1})`));
}

/**
 * The colors of a donut's or a pie's slices: the series' named color for the first, then the palette (the series colors
 * and then the named hues, so eight slices never repeat).
 */
export function sliceColors(n: number): string[] {
  const more = ["teal", "amber", "indigo", "orange"];
  return Array.from({ length: n }, (_, i) => (i < 4 ? `var(--series-${i + 1})` : `var(--hue-${more[(i - 4) % more.length]})`));
}
