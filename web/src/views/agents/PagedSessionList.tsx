// Bounded collection views: filters/counts belong to the server; only the loaded prefix lives in React.
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactNode } from "react";
import { useVirtualizer } from "@tanstack/react-virtual";
import { t } from "../../i18n";
import { api, errorText } from "../../lib/api";
import { onHubEvent } from "../../lib/store";
import { SessionList, type ListCounts, type ListFilters } from "../../lib/session-list";
import { SESSION_LIST_METRICS } from "../../ui/list-metrics";
import { SearchField } from "../../ui/SearchField";
import { STATE_LABEL, stateCount } from "../../lib/state-labels";
import type { AgentState } from "../../lib/types";
import { Button, cx, Menu, sidebarFoldMotion, useMenu, useNow, type MenuItem } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { SegmentedTrack } from "../../ui/Segmented";
import { StatusMark } from "../../ui/StatusMark";
import { baseName } from "../../lib/format";
import { removeStoredValue, storedValue, storeValue } from "../../lib/compatibility";
import { ACTIVE_WINDOWS, activeWindow, splitByActivity, windowStart, type ActiveWindow } from "../../lib/session-activity";
import { useFolded } from "../../lib/use-folded";

export interface PagedSessionListProps<T> {
  root: SessionList<T>;
  path: string;
  identity: (row: T) => string;
  compare: (a: T, b: T) => number;
  removeIdentity?: (id: string) => string;
  event: "sessions_list" | "agents";
  renderRow: (row: T) => ReactNode;
  personal?: boolean;
  label?: (dimension: string, value: string) => string;
  /** "By repo" is the full, client-side tree (Sidebar's By repo), not a paged filter: offered only when the caller has it. */
  onRepo?: () => void;
  /** "Active within" (#4236): a row's last activity in ms, the key `compare` orders by, newest first. Given, the list
   *  offers the window menu and folds the rows active before the window into one Earlier group at its end. */
  activeAt?: (row: T) => number;
  /** A row that needs the user, by the rule the server counts `state.needs_you` with: counted on the Earlier header. */
  needsYou?: (row: T) => boolean;
  /** The open row: the Earlier group opens by itself while it holds that row. */
  isOpen?: (row: T) => boolean;
}

// The chosen "Active within" window, kept per list on this browser.
const windowKey = (personal: boolean) => personal ? "persona.personalActiveWindow" : "persona.agentActiveWindow";
const windowLabel = (w: ActiveWindow) => t(`paged-agents.window_${w || "any"}`);
// the Earlier header's place among the rows: a key no row identity has
const EARLIER_KEY = "\u0000earlier";

/** A counts query in a stable key order, so the same scope reached through different filters compares equal. */
function countsQuery(filters: ListFilters): string {
  const params = new URLSearchParams({ counts: "1" });
  for (const [key, value] of Object.entries(filters).sort(([a], [b]) => a.localeCompare(b))) if (value) params.set(key, value);
  return params.toString();
}

export function scrollParent(el: HTMLElement | null): HTMLElement | null {
  for (let p = el?.parentElement; p; p = p.parentElement) {
    if (/auto|scroll/.test(getComputedStyle(p).overflowY)) return p;
  }
  return null;
}

export function PagedSessionList<T>({ root, path, identity, compare, removeIdentity, event, renderRow, personal = false, label, onRepo, activeAt, needsYou, isOpen }: PagedSessionListProps<T>) {
  const [query, setQuery] = useState("");
  const [q, setQ] = useState("");
  const [state, setState] = useState("");
  const [dimension, setDimension] = useState<"machine" | "project" | "group" | "">("");
  const [value, setValue] = useState("");
  const [machine, setMachine] = useState("");
  const [provider, setProvider] = useState("");
  const [providerQuery, setProviderQuery] = useState("");
  useEffect(() => {
    const timer = setTimeout(() => { setQ(query.trim()); setProvider(providerQuery.trim()); }, SESSION_LIST_METRICS.searchDelay);
    return () => clearTimeout(timer);
  }, [query, providerQuery]);
  const filters: ListFilters = useMemo(() => ({
    ...(q ? { q } : {}), ...(state ? { state } : {}), ...(provider ? { provider } : {}),
    ...(dimension && value ? { [dimension === "machine" ? "machine_id" : dimension]: value } : {}),
    ...(dimension === "group" && machine ? { machine_id: machine } : {}),
  }), [q, state, dimension, value, machine, provider]);
  const filtered = Object.keys(filters).length > 0;
  const summaryMode = root.isSummary();
  // a hub that folds child sessions (#5120): every read and count of this list leaves them out, as the root's do
  const rootsMode = root.isRoots();
  const countsPath = rootsMode ? `${path}?roots=1&` : `${path}?`;
  const list = useMemo(() => filtered ? new SessionList<T>(path, identity, compare, undefined, removeIdentity, { summary: summaryMode, roots: rootsMode, pageCounts: () => root.usesPageCounts() }) : root, [filtered, path, identity, compare, removeIdentity, summaryMode, rootsMode, root, filters]);
  const page = useSyncExternalStore(list.subscribe, list.getSnapshot, list.getSnapshot);
  const [facets, setFacets] = useState<ListCounts | null>(null);
  const [facetError, setFacetError] = useState("");
  const [scopeCounts, setScopeCounts] = useState<ListCounts | null>(null);
  const [scopeError, setScopeError] = useState("");
  const scopeQuery = useMemo(() => {
    const scope = { ...filters };
    delete scope.state;
    return countsQuery(scope);
  }, [filters]);
  const rootPage = useSyncExternalStore(root.subscribe, root.getSnapshot, root.getSnapshot);
  // Counts without state keep other state tabs reachable; machine scopes disambiguate computer-local groups.
  const facetFilters = useMemo(() => ({ ...(q ? { q } : {}), ...(provider ? { provider } : {}), ...(machine && dimension === "group" ? { machine_id: machine } : {}) }), [q, provider, machine, dimension]);
  // Unscoped counts are the root's own: follow them. Scoped counts are read on their own, now and once per hint; they
  // do not follow the root's counts, which land for that same hint (a second read of the same counts every time).
  const facetScoped = Object.keys(facetFilters).length > 0;
  const facetQuery = useMemo(() => countsQuery(facetFilters), [facetFilters]);
  // With no state tab and no browsed value the filtered list's own per-hint counts read is the facet query: follow it.
  const facetsFromList = filtered && countsQuery(filters) === facetQuery;
  useEffect(() => {
    if (facetScoped) return;
    setFacets(rootPage.counts);
    setFacetError("");
  }, [facetScoped, rootPage.counts]);
  useEffect(() => {
    // Keep the last counts while the list loads; a failed read shows as the list's own error.
    if (!facetsFromList) return;
    if (page.counts) setFacets(page.counts);
    setFacetError("");
  }, [facetsFromList, page.counts]);
  useEffect(() => {
    if (!facetScoped || facetsFromList) return;
    let live = true;
    let sequence = 0;
    const controller = new AbortController();
    const read = () => {
      const request = ++sequence;
      void api<ListCounts>(`${countsPath}${facetQuery}`, { signal: controller.signal }).then((counts) => {
        if (live && request === sequence && counts.state && counts.group && counts.project && counts.machine) { setFacets(counts); setFacetError(""); }
      }, (e) => { if (live && request === sequence) setFacetError(errorText(e)); });
    };
    read();
    const off = onHubEvent(event, read);
    return () => { live = false; controller.abort(); off(); };
  }, [countsPath, event, facetScoped, facetsFromList, facetQuery]);
  useEffect(() => {
    // Keep the last counts while a refresh or a new scope is loading, including a root rebaseline.
    if (scopeQuery !== "counts=1") return;
    if (rootPage.counts) setScopeCounts(rootPage.counts);
    setScopeError("");
  }, [scopeQuery, rootPage.counts]);
  // Without a browsed value the scope is the facets' query: one read serves both.
  const scopeFromFacets = scopeQuery !== "counts=1" && scopeQuery === facetQuery;
  useEffect(() => {
    if (!scopeFromFacets) return;
    if (facets) setScopeCounts(facets);
    setScopeError("");
  }, [scopeFromFacets, facets]);
  useEffect(() => {
    if (scopeQuery === "counts=1" || scopeFromFacets) return;
    let sequence = 0;
    const abort = new AbortController();
    const read = () => {
      const request = ++sequence;
      void api<ListCounts>(`${countsPath}${scopeQuery}`, { signal: abort.signal }).then((counts) => {
        if (request === sequence && counts.state) { setScopeCounts(counts); setScopeError(""); }
      }, (e) => { if (request === sequence) setScopeError(errorText(e)); });
    };
    read();
    const off = onHubEvent(event, read);
    return () => { sequence++; abort.abort(); off(); };
  }, [scopeQuery, scopeFromFacets, countsPath, event]);
  useEffect(() => {
    if (filtered) void list.setFilters(filters).catch(() => {});
    else if (!list.getSnapshot().loaded) void list.load().catch(() => {});
    const off = filtered ? onHubEvent(event, () => list.invalidate()) : () => {};
    return () => { off(); if (filtered) list.reset(); };
  }, [list, filters, filtered, event]);

  // "Active within" (#4236): the rows active before the window fold into Earlier, after the recent ones. The split
  // moves with the clock; the list itself, its total and its filters stay the server's.
  const [win, setWin] = useState<ActiveWindow>(() => activeAt ? activeWindow(storedValue(windowKey(personal))) : "");
  const chooseWindow = (w: ActiveWindow) => {
    setWin(w);
    if (w) storeValue(windowKey(personal), w);
    else removeStoredValue(windowKey(personal));
  };
  const clock = useNow(60_000, Boolean(activeAt && win));
  const start = activeAt ? windowStart(win, clock) : null;
  // the Needs you chip's number for this scope: the server's, by the same rule as the chip
  const needsYouTotal = state ? (state === "needs_you" ? page.total : 0) : scopeCounts?.state?.needs_you;
  const split = activeAt && start !== null
    ? splitByActivity(page.rows, activeAt, start, { total: page.total, hasMore: page.hasMore, needsYou: needsYou ?? (() => false), needsYouTotal })
    : null;
  const earlier = split?.reached && split.earlier > 0 ? split : null;
  const [folded, toggleEarlier] = useFolded(personal ? "personal-sessions-earlier" : "sessions-earlier", true, {
    holds: Boolean(earlier && isOpen && page.rows.slice(earlier.recent).some(isOpen)),
  });
  // folded, the list stops at its header: no further pages are read until it opens
  const foldedAway = Boolean(earlier && folded);
  const hasMore = page.hasMore && !foldedAway;
  const itemCount = (foldedAway ? earlier!.recent : page.rows.length) + (earlier ? 1 : 0);
  // the Earlier header sits between the recent rows and the earlier ones; null is the header
  const rowAt = (i: number): T | null => !earlier ? page.rows[i] : i === earlier.recent ? null : page.rows[i > earlier.recent ? i - 1 : i];

  const container = useRef<HTMLDivElement>(null);
  const [margin, setMargin] = useState(0);
  const getScrollElement = useCallback(() => scrollParent(container.current), []);
  const virtual = useVirtualizer({
    count: itemCount,
    getScrollElement,
    estimateSize: () => SESSION_LIST_METRICS.estimate,
    getItemKey: (i) => { const row = rowAt(i); return row === null ? EARLIER_KEY : identity(row); },
    overscan: SESSION_LIST_METRICS.overscan,
    scrollMargin: margin,
  });
  useLayoutEffect(() => {
    const el = container.current;
    const parent = getScrollElement();
    if (!el || !parent) return;
    const measure = () => setMargin(el.getBoundingClientRect().top - parent.getBoundingClientRect().top + parent.scrollTop);
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(parent);
    if (el.parentElement) ro.observe(el.parentElement);
    return () => ro.disconnect();
  }, [getScrollElement, page.loaded, dimension, facetError]);
  const totalSize = virtual.getTotalSize();
  const scrolled = useRef(false);
  useEffect(() => {
    scrolled.current = false;
    const parent = getScrollElement();
    if (parent) parent.scrollTop = 0;
  }, [list, getScrollElement]);
  useEffect(() => {
    const parent = getScrollElement();
    if (!parent) return;
    const maybeMore = () => {
      if (scrolled.current && hasMore && !page.loading && !page.error &&
          parent.scrollTop + parent.clientHeight >= margin + totalSize - SESSION_LIST_METRICS.overscan * SESSION_LIST_METRICS.estimate) {
        void list.more().catch(() => {});
      }
    };
    const scroll = () => { scrolled.current = true; maybeMore(); };
    parent.addEventListener("scroll", scroll, { passive: true });
    maybeMore();
    return () => parent.removeEventListener("scroll", scroll);
  }, [list, getScrollElement, hasMore, page.loading, page.error, margin, totalSize]);

  const counts = facets ?? rootPage.counts;
  const stateTotals = scopeCounts?.state ?? {};
  // the most used groups first: the menu lists a bounded number of them
  const groups = useMemo(() => dimension ? Object.entries(counts?.[dimension] ?? {}).filter(([key]) => key).sort(([a, x], [b, y]) => y - x || a.localeCompare(b)).slice(0, SESSION_LIST_METRICS.groupOptions) : [], [dimension, counts]);
  const machines = useMemo(() => Object.entries(rootPage.counts?.machine ?? {}).filter(([id]) => id).sort(([a, x], [b, y]) => y - x || a.localeCompare(b)), [rootPage.counts]);
  const dimensions: { key: typeof dimension | "repo"; label: string }[] = personal
    ? [{ key: "", label: t("paged-agents.recent") }, { key: "group", label: t("paged-agents.account") }]
    : [{ key: "", label: t("paged-agents.recent") }, { key: "machine", label: t("paged-agents.machine") }, { key: "project", label: t("paged-agents.project") }, { key: "group", label: t("paged-agents.group") },
      ...(onRepo ? [{ key: "repo" as const, label: t("lib-agent-groups.seg_repo") }] : [])];
  const groupName = (key: string) => dimension === "project" ? baseName(key) || key : label?.(dimension, key) ?? key;
  const allOf = t(dimension === "machine" ? "paged-agents.all_machines" : dimension === "project" ? "paged-agents.all_projects" : personal ? "paged-agents.all_accounts" : "paged-agents.all_spaces");
  const chooseDimension = (d: typeof dimension | "repo") => {
    // The repo tree cannot be a paged filter (it needs every session): it switches to the full By repo view.
    if (d === "repo") { onRepo?.(); return; }
    setDimension(d); setValue(""); setMachine("");
  };
  const states = personal ? ["unread", "idle"] : ["needs_you", "working", "unread", "idle", "offline", "unknown"];
  return (
    <div data-paged-sessions={personal ? "personal" : "runtime"} data-loaded-sessions={page.rows.length} data-has-more={page.hasMore}>
      <div className="space-y-2 px-1 py-2">
        <SearchField value={query} onChange={setQuery} placeholder={t("paged-agents.search")} />
        <SegmentedTrack role="radiogroup" label={t("paged-agents.group_by")} className="flex gap-0.5 rounded-full p-0.5" thumbClassName="rounded-full bg-surface shadow-btn">
          {dimensions.map((d) => {
            const on = d.key === dimension;
            return (
              <button key={d.key || "recent"} type="button" role="radio" aria-checked={on} data-selected={on} onClick={() => chooseDimension(d.key)}
                className={cx("touch-hit relative z-[1] flex h-6 min-w-0 flex-auto items-center justify-center whitespace-nowrap rounded-full px-1.5 text-[11.5px] transition-colors duration-150", on ? "font-medium text-ink" : "text-ink-2 hover:text-ink")}>
                <span className="truncate">{d.label}</span>
              </button>
            );
          })}
        </SegmentedTrack>
        <div role="radiogroup" aria-label={t("sidebar.quick_filters")} className="flex flex-wrap gap-0.5">
          {["", ...states.filter((s) => (stateTotals[s] ?? 0) > 0 || state === s)].map((s) => {
            const on = state === s;
            const n = s && scopeCounts ? stateTotals[s] ?? 0 : undefined;
            const name = s ? STATE_LABEL[s as AgentState] : t("paged-agents.all");
            return (
              <button key={s} type="button" role="radio" aria-checked={on} aria-label={n === undefined ? name : `${name} ${n}`} onClick={() => setState(s)}
                className={cx("touch-hit relative z-10 flex h-6 items-center gap-1 whitespace-nowrap rounded-full px-[7px] text-[11.5px] transition-[background-color,color,box-shadow] duration-150", on ? "bg-surface font-medium text-ink shadow-btn" : "text-ink-2 hover:bg-hover hover:text-ink")}>
                {s && <StatusMark state={s as AgentState} size="sm" />}
                {name}
                {n !== undefined && <span className={cx("tabular-nums", on ? "text-ink-2" : "text-ink-3")}>{n}</span>}
              </button>
            );
          })}
        </div>
        {(dimension || personal) && <div className="flex flex-wrap gap-1">
          {dimension === "group" && !personal && <PickChip label={t("paged-agents.machine")} header={t("paged-agents.machine")} value={machine} shown={machine ? label?.("machine", machine) ?? machine : t("paged-agents.all_machines")}
            items={[{ key: "", label: t("paged-agents.all_machines") }, ...machines.map(([id, n]) => ({ key: id, label: label?.("machine", id) ?? id, hint: String(n) }))]}
            onChoose={(id) => { setMachine(id); setValue(""); }} />}
          {dimension && <PickChip label={t("paged-agents.group_filter")} header={dimensions.find((d) => d.key === dimension)?.label} value={value} shown={value ? groupName(value) : allOf} grow
            items={[{ key: "", label: allOf }, ...groups.map(([key, n]) => ({ key, label: groupName(key), hint: String(n), detail: dimension === "project" && groupName(key) !== key ? key : undefined }))]}
            onChoose={setValue} />}
          {personal && <SearchField value={providerQuery} onChange={setProviderQuery} placeholder={t("paged-agents.provider")} tone="inset" icon={false} className="min-w-0 flex-1" />}
        </div>}
        <div className="flex min-w-0 items-center justify-between gap-2">
          <p className="min-w-0 truncate text-xs text-ink-3" data-session-total>{t("paged-agents.count", { count: page.total })}</p>
          {activeAt && <ActiveWindowMenu value={win} onChange={chooseWindow} />}
        </div>
      </div>
      {(facetError || scopeError) && <p role="alert" className="px-2 text-xs text-red">{facetError || scopeError}</p>}
      <div ref={container} data-virtual-sessions className="relative" style={{ height: totalSize }}>
        {virtual.getVirtualItems().map((item) => {
          const row = rowAt(item.index);
          return (
            <div key={item.key} ref={virtual.measureElement} data-index={item.index} {...(row === null ? { "data-earlier": "" } : { "data-session-row": "" })} className="absolute top-0 left-0 w-full" style={{ transform: `translateY(${item.start - margin}px)` }}>
              {row === null ? <EarlierHeader count={earlier!.earlier} needs={earlier!.earlierNeedsYou} window={win} folded={folded} onToggle={toggleEarlier} /> : renderRow(row)}
            </div>
          );
        })}
      </div>
      {page.loading && <p role="status" className="px-2 py-2 text-xs text-ink-3">{t("sidebar.loading")}</p>}
      {page.error && <div className="px-2 py-2"><p role="alert" className="text-xs text-red">{page.error}</p><Button onClick={() => void list.retry().catch(() => {})}>{t("sidebar.retry")}</Button></div>}
      {page.loaded && !page.loading && !page.error && !page.rows.length && <p className="px-2 py-2 text-xs text-ink-3">{t("sidebar.no_match")}</p>}
      {hasMore && !page.loading && !page.error && <Button onClick={() => void list.more().catch(() => {})}>{t("paged-agents.more")}</Button>}
    </div>
  );
}

/** The list's "Active within" display choice (#4236): a quiet menu beside the count, naming the window it keeps. */
function ActiveWindowMenu({ value, onChange }: { value: ActiveWindow; onChange: (w: ActiveWindow) => void }) {
  const menu = useMenu();
  const items: MenuItem[] = ACTIVE_WINDOWS.map((w, i) => ({ key: w || "any", label: windowLabel(w), radio: true, checked: w === value, divider: i === 1, onSelect: () => onChange(w) }));
  return <>
    <button type="button" aria-haspopup="menu" aria-expanded={Boolean(menu.anchor)} aria-label={t("paged-agents.active_within_value", { window: windowLabel(value) })} title={t("paged-agents.active_within")} onClick={menu.open}
      className={cx("touch-hit relative z-10 flex h-6 min-w-0 shrink-0 items-center gap-1 whitespace-nowrap rounded-[6px] px-1.5 text-[11.5px] transition-colors duration-150 hover:bg-hover hover:text-ink", value ? "font-medium text-ink" : "text-ink-3")}>
      <Icon name="clock" size={11} className="shrink-0" />
      <span className="truncate">{windowLabel(value)}</span>
      <Icon name="chevron" size={11} className="shrink-0 text-ink-3" />
    </button>
    {menu.anchor && <Menu items={items} anchor={menu.anchor} onClose={menu.close} header={t("paged-agents.active_within")} align="right" width={200} />}
  </>;
}

/** The fold over the sessions last active before the window: how many, and how many of them need you (DESIGN_SYSTEM 3.4). */
function EarlierHeader({ count, needs, window, folded, onToggle }: { count: number; needs: number; window: ActiveWindow; folded: boolean; onToggle: () => void }) {
  return (
    <div className="group/sec flex items-center gap-1 px-2 pt-2 pb-0.5">
      <button type="button" aria-expanded={!folded} onClick={onToggle} title={t("paged-agents.earlier_hint", { count, window: windowLabel(window) })}
        className="relative z-10 flex min-h-7 min-w-0 flex-1 items-center gap-1.5 whitespace-nowrap text-left [@media(pointer:coarse)]:min-h-11">
        <span className="min-w-0 truncate text-[12.5px] font-semibold text-ink-2">{t("paged-agents.earlier")}</span>
        <span className="shrink-0 text-[11px] text-ink-3 tabular-nums">{count}</span>
        <Icon name="chevron" size={10} stroke={2.4} className="shrink-0 text-ink-3 transition-transform" style={{ ...sidebarFoldMotion(!folded), transform: folded ? "rotate(-90deg)" : "none" }} />
      </button>
      {needs > 0 && (
        <span title={stateCount("needs_you", needs)} className="flex h-4 min-w-4 shrink-0 items-center justify-center rounded-full bg-field px-1 text-[10.5px] font-semibold text-attention tabular-nums">
          {needs}<span className="sr-only"> {t("sidebar.needs_you_suffix", { count: needs })}</span>
        </span>
      )}
    </div>
  );
}

/** A pill that names what is chosen and opens the choices in the design system's menu (no native select). */
function PickChip({ label, header = label, value, shown, items, onChoose, grow = false }: { label: string; header?: string; value: string; shown: string; items: { key: string; label: string; hint?: string; detail?: string }[]; onChoose: (key: string) => void; grow?: boolean }) {
  const menu = useMenu();
  const rows: MenuItem[] = items.map((it, i) => ({ ...it, radio: true, checked: it.key === value, divider: i === 1, onSelect: () => onChoose(it.key) }));
  return <>
    <button type="button" aria-label={label} aria-haspopup="menu" aria-expanded={Boolean(menu.anchor)} title={shown} onClick={menu.open}
      className={cx("touch-hit relative z-10 flex h-6 min-w-0 items-center gap-1 whitespace-nowrap rounded-full bg-inset px-2 text-[11.5px] shadow-hairline transition-colors duration-150 hover:bg-hover", value ? "font-medium text-ink" : "text-ink-2", grow && "flex-1")}>
      <span className="min-w-0 flex-1 truncate text-left">{shown}</span>
      <Icon name="chevron" size={11} className="shrink-0 text-ink-3" />
    </button>
    {menu.anchor && <Menu items={rows} anchor={menu.anchor} onClose={menu.close} header={header} width={260} />}
  </>;
}
