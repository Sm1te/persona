import { api, ApiError, errorText } from "./api";
import { t } from "../i18n";
import { sameValue } from "./stable-lines";

export const SESSIONS_LIST_FEATURE = "sessions_list";
export const PAGE_SIZE = 50;
/** UTF-8 lexical order follows Unicode code points, not locale or JavaScript's surrogate-unit order. */
export function compareListIds(a: string, b: string): number {
  let left = 0, right = 0;
  while (left < a.length && right < b.length) {
    const ac = a.codePointAt(left)!, bc = b.codePointAt(right)!;
    if (ac !== bc) return ac - bc;
    left += ac > 0xffff ? 2 : 1;
    right += bc > 0xffff ? 2 : 1;
  }
  return a.length - b.length;
}
const JOURNAL_LIMIT = 16_384;
const validCounts = (counts: ListCounts | undefined): counts is ListCounts => !!counts && typeof counts.total === "number" &&
  typeof counts.revision === "string" && !!counts.machine && !!counts.project && !!counts.group && !!counts.state;
const FILTER_KEYS = ["state", "machine_id", "project", "group", "source", "provider", "q", "parent_session_id"] as const;

export type ListFilters = Partial<Record<typeof FILTER_KEYS[number], string>>;
export interface ListCounts {
  total: number;
  machine: Record<string, number>;
  project: Record<string, number>;
  group: Record<string, number>;
  state: Record<string, number>;
  revision: string;
}
export interface ListPage<T> {
  sessions: T[];
  remove: string[];
  has_more: boolean;
  next_cursor?: string;
  revision: string;
  total: number;
  /** Only when the read asked `with_counts=1` (`sessions_page_counts`): what `counts=1` with the same filters returns. */
  counts?: ListCounts;
}
export interface ListSnapshot<T> {
  rows: T[];
  loaded: boolean;
  loading: boolean;
  error: string | null;
  total: number;
  counts: ListCounts | null;
  hasMore: boolean;
  supported: boolean | null;
}

export interface SessionListOptions<T> {
  summary?: boolean;
  /** Receives only this completed delta's net changes, including unloaded identities. Must not throw. */
  onDelta?: (upsert: T[], remove: string[]) => void;
  /** Whether the hub answers `with_counts=1` (`sessions_page_counts`, #4858): a refresh then reads its counts with the
   * page, one request instead of two. A page that comes back without them still gets the separate counts read. */
  pageCounts?: () => boolean;
  /** Read the list without the child sessions folded under a listed one (`roots=1`, `sessions_hierarchy`, #5120): rows,
   * totals and counts then hold each parent once, counted by its folded state. Only for a hub that advertises it. */
  roots?: boolean;
}

type PreparedDelta<T> = { overlay: Map<string, T | null>; changes: Map<string, T | null>; revision: string; total: number };
type Work = { generation: number; load: boolean; more: boolean; kind: "load" | "more" | null; promise?: Promise<void> };
class Rebaseline extends Error {}

/** A lazy, bounded window over either session collection. Rows and snapshots are structurally shared.
 * `compare` must use the collection's newest-first transport order, not a presentation state/pin order.
 * Rejected reads also set `error`; invalidation is fire-and-forget and records errors without rejecting globally.
 */
export class SessionList<T> {
  private snapshot: ListSnapshot<T> = this.empty();
  private listeners = new Set<() => void>();
  private filters: ListFilters = {};
  private generation = 0;
  private work: Work = { generation: 0, load: false, more: false, kind: null };
  // The frozen cursor never follows the delta checkpoint. Counts are not a row checkpoint either.
  private cursor?: string;
  private frozenRevision?: string;
  private checkpoint?: string;
  private frozen = new Map<string, T>();
  // Including unmaterialized identities is essential: a later frozen page may carry their stale copies.
  private overlay = new Map<string, T | null>();
  private prefixSize = PAGE_SIZE;
  private invalidationQueued = false;
  private lastFailure: "load" | "more" = "load";
  private summary: boolean;
  private roots: boolean;
  private onDelta?: (upsert: T[], remove: string[]) => void;
  private pageCounts: () => boolean;

  constructor(
    private path: string,
    private identity: (row: T) => string,
    private compare: (a: T, b: T) => number,
    private onRows?: (rows: T[]) => void,
    private removeIdentity: (id: string) => string = (id) => id,
    options: SessionListOptions<T> = {},
  ) {
    this.summary = options.summary ?? true;
    this.roots = options.roots ?? false;
    this.onDelta = options.onDelta;
    this.pageCounts = options.pageCounts ?? (() => false);
  }

  isSummary(): boolean { return this.summary; }
  /** Whether every read sends `roots=1`: a list or counts read over the same collection sends it too. */
  isRoots(): boolean { return this.roots; }
  /** For a list over the same collection, e.g. a filtered copy of this one. */
  usesPageCounts(): boolean { return this.pageCounts(); }

  /** Changing projection invalidates its bound cursors, but preserves filters and does not start a read. */
  setSummary(value: boolean): void {
    if (this.summary === value) return;
    this.summary = value;
    this.reset();
  }

  /** Leaving the folded children out (or taking them back) changes every row, total and cursor: like setSummary. */
  setRoots(value: boolean): void {
    if (this.roots === value) return;
    this.roots = value;
    this.reset();
  }

  getSnapshot = (): ListSnapshot<T> => this.snapshot;
  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => { this.listeners.delete(listener); };
  };

  setFilters(filters: ListFilters): Promise<void> {
    const next: ListFilters = {};
    for (const key of FILTER_KEYS) if (filters[key]) next[key] = filters[key];
    if (!sameValue(next, this.filters)) {
      this.filters = next;
      this.reset();
      return this.load();
    }
    return this.snapshot.loaded ? (this.work.promise ?? Promise.resolve()) : this.load();
  }

  load(): Promise<void> {
    return this.enqueue("load");
  }

  more(): Promise<void> {
    if (!this.snapshot.loaded) return this.load();
    if (!this.snapshot.hasMore || this.snapshot.supported === false) return Promise.resolve();
    return this.enqueue("more");
  }

  retry(): Promise<void> {
    // A delta failure must not be mistaken for a next-page failure just because a frozen cursor still exists.
    return this.lastFailure === "more" && this.snapshot.counts ? this.more() : this.load();
  }

  invalidate(): void {
    if (this.invalidationQueued) return;
    this.invalidationQueued = true;
    const generation = this.generation;
    queueMicrotask(() => {
      if (!this.current(generation)) return;
      this.invalidationQueued = false;
      // Unlike an explicit duplicate load, a hint during a read needs one trailing read.
      this.work.load = true;
      void this.enqueue("load").catch(() => {});
    });
  }

  reset(): void {
    this.lastFailure = "load";
    this.generation++;
    this.work = { generation: this.generation, load: false, more: false, kind: null };
    this.invalidationQueued = false;
    this.clearBaseline();
  }

  private empty(): ListSnapshot<T> {
    return { rows: [], loaded: false, loading: false, error: null, total: 0, counts: null, hasMore: false, supported: null };
  }

  private clearBaseline(): void {
    this.cursor = undefined;
    this.frozenRevision = undefined;
    this.checkpoint = undefined;
    this.frozen = new Map();
    this.overlay = new Map();
    this.prefixSize = PAGE_SIZE;
    this.publish(this.empty());
  }

  private current(generation: number): boolean { return generation === this.generation; }

  private publish(patch: Partial<ListSnapshot<T>>): void {
    const next = { ...this.snapshot, ...patch };
    if (sameValue(this.snapshot.counts, next.counts)) next.counts = this.snapshot.counts;
    if (Object.keys(next).every((key) => Object.is(next[key as keyof ListSnapshot<T>], this.snapshot[key as keyof ListSnapshot<T>]))) return;
    const rowsChanged = next.rows !== this.snapshot.rows;
    this.snapshot = next;
    if (rowsChanged) this.onRows?.(next.rows);
    for (const listener of this.listeners) listener();
  }

  private enqueue(kind: "load" | "more"): Promise<void> {
    const work = this.work;
    if (work.kind !== kind) work[kind] = true;
    if (work.promise) return work.promise;
    // Defer startup so callers in the same turn share the promise, including subscriber callbacks.
    work.promise = Promise.resolve().then(async () => {
      if (!this.current(work.generation)) return;
      this.publish({ loading: true, error: null });
      try {
        while (this.current(work.generation) && (work.load || work.more)) {
          const next = work.load ? "load" : "more";
          work[next] = false;
          work.kind = next;
          if (next === "load") await this.refresh(work.generation);
          else await this.readMore(work.generation);
        }
      } catch (error) {
        if (!this.current(work.generation)) return;
        this.lastFailure = work.kind ?? "load";
        work.load = work.more = false;
        this.publish({ error: errorText(error) });
        throw error;
      } finally {
        work.kind = null;
        work.promise = undefined;
        if (this.current(work.generation)) this.publish({ loading: false });
      }
    });
    return work.promise;
  }

  private query(extra: Record<string, string>): string {
    const [path, existing = ""] = this.path.split("?");
    const params = new URLSearchParams(existing);
    for (const key of [...FILTER_KEYS, "summary", "limit", "counts", "with_counts", "cursor", "since", "roots"]) params.delete(key);
    for (const key of FILTER_KEYS) if (this.filters[key]) params.set(key, this.filters[key]!);
    if (this.roots) params.set("roots", "1");
    for (const [key, value] of Object.entries(extra)) params.set(key, value);
    return `${path}?${params}`;
  }

  private unsupported(error: unknown): boolean {
    return error instanceof ApiError && (error.status === 404 || (
      [400, 405, 501].includes(error.status) && (
        ["unsupported", "unsupported_feature", "not_supported"].includes(error.code ?? "") || /^unsupported\b/i.test(error.message)
      )
    ));
  }

  private expired(error: unknown): boolean {
    return error instanceof ApiError && error.status === 409 && ["cursor_expired", "revision_expired"].includes(error.code ?? "");
  }

  private async page(generation: number, extra: Record<string, string> = {}): Promise<ListPage<T> | null> {
    let response: Partial<ListPage<T>>;
    try {
      response = await api<Partial<ListPage<T>>>(this.query({ summary: this.summary ? "1" : "0", limit: String(PAGE_SIZE), ...extra }));
    } catch (error) {
      if (!this.current(generation)) return null;
      if (!this.unsupported(error)) throw error;
      this.publish({ supported: false, loaded: true, hasMore: false });
      return null;
    }
    if (!this.current(generation)) return null;
    if (typeof response.has_more !== "boolean" || typeof response.revision !== "string" || typeof response.total !== "number" || !Array.isArray(response.remove)) {
      const rows = Array.isArray(response.sessions) ? response.sessions : [];
      this.publish({ rows: this.stabilize(rows), loaded: true, supported: false, total: rows.length, hasMore: false });
      return null;
    }
    if (!Array.isArray(response.sessions) || !response.revision || (response.has_more && !response.next_cursor)) {
      throw new Error(t("paged-agents.invalid_page"));
    }
    return response as ListPage<T>;
  }

  private async readCounts(generation: number): Promise<void> {
    try {
      const counts = await api<ListCounts>(this.query({ counts: "1" }));
      if (!this.current(generation)) return;
      // Legacy hubs may ignore counts and answer with sessions. Never use that as a row baseline.
      if (validCounts(counts)) this.publish({ counts });
    } catch (error) {
      if (!this.current(generation)) return;
      if (!this.unsupported(error)) throw error;
      this.publish({ supported: false });
    }
  }

  /** The counts that came with a page (`with_counts`), or the separate read when they did not. */
  private async pageCountsOrRead(generation: number, page: ListPage<T>): Promise<void> {
    if (validCounts(page.counts)) this.publish({ counts: page.counts });
    else await this.readCounts(generation);
  }

  private async refresh(generation: number, retry = true): Promise<void> {
    const withCounts = this.pageCounts();
    const results = await Promise.allSettled([
      this.checkpoint && this.snapshot.supported !== false ? this.readDelta(generation, withCounts) : this.readFirst(generation, withCounts),
      withCounts ? undefined : this.readCounts(generation),
    ]);
    if (!this.current(generation)) return;
    const errors = results.flatMap((result) => result.status === "rejected" ? [result.reason] : []);
    // Capability/epoch recovery must not hide an independent authentication or network failure.
    const genuine = errors.findIndex((error) => !(error instanceof Rebaseline) && !this.expired(error));
    if (genuine !== -1) throw errors[genuine];
    if (retry && errors.length) {
      this.clearBaseline();
      if (!this.current(generation)) return;
      this.publish({ loading: true });
      await this.refresh(generation, false);
      return;
    }
    if (errors.length) throw errors[0];
    const result = results[0];
    if (result.status === "fulfilled" && result.value) this.commitDelta(generation, result.value);
  }

  private async readFirst(generation: number, withCounts = false): Promise<void> {
    const page = await this.page(generation, withCounts ? { with_counts: "1" } : {});
    if (!page || !this.current(generation)) return;
    if (withCounts) await this.pageCountsOrRead(generation, page);
    if (!this.current(generation)) return;
    this.frozen = new Map(page.sessions.map((row) => [this.identity(row), row]));
    this.overlay = new Map();
    this.prefixSize = PAGE_SIZE;
    this.cursor = page.has_more ? page.next_cursor : undefined;
    this.frozenRevision = page.revision;
    this.checkpoint = page.revision;
    this.publish({ rows: this.visible(), loaded: true, supported: this.snapshot.supported !== false, total: page.total, hasMore: !!this.cursor });
  }

  private async readDelta(generation: number, withCounts = false): Promise<PreparedDelta<T> | null> {
    const since = this.checkpoint!;
    const overlay = new Map(this.overlay);
    const changes = new Map<string, T | null>();
    let cursor: string | undefined;
    let target = since;
    let first = true;
    let total = this.snapshot.total;
    const cursors = new Set<string>();
    do {
      const page = await this.page(generation, { since, ...(cursor ? { cursor } : {}), ...(first && withCounts ? { with_counts: "1" } : {}) });
      if (!page || !this.current(generation)) return null;
      if (first && withCounts) {
        await this.pageCountsOrRead(generation, page);
        if (!this.current(generation)) return null;
      }
      if (!first && target !== page.revision) throw new Error(t("paged-agents.inconsistent_delta"));
      first = false;
      target = page.revision;
      total = page.total;
      for (const id of page.remove) {
        const normalized = this.removeIdentity(id);
        overlay.set(normalized, null);
        changes.set(normalized, null);
      }
      for (const row of page.sessions) {
        const id = this.identity(row);
        overlay.set(id, row);
        changes.set(id, row);
      }
      if (overlay.size > JOURNAL_LIMIT) throw new Rebaseline();
      cursor = page.has_more ? page.next_cursor : undefined;
      if (cursor && cursors.has(cursor)) throw new Error(t("paged-agents.repeated_cursor"));
      if (cursor) cursors.add(cursor);
    } while (cursor);
    return { overlay, changes, revision: target, total };
  }

  private commitDelta(generation: number, delta: PreparedDelta<T>): void {
    // Failed pages or counts leave both checkpoints and callbacks untouched, so retry sees all changes again.
    if (!this.current(generation)) return;
    this.overlay = delta.overlay;
    this.checkpoint = delta.revision;
    const rows = this.visible();
    this.publish({ rows, total: delta.total, hasMore: !!this.cursor || this.candidateCount() > this.prefixSize });
    // Publishing may synchronously reset/switch the list through onRows or a subscriber.
    if (!this.current(generation) || !this.onDelta) return;
    const upsert: T[] = [];
    const remove: string[] = [];
    for (const [id, row] of delta.changes) {
      if (row === null) remove.push(id);
      else upsert.push(row);
    }
    this.onDelta(upsert, remove);
  }

  private async readMore(generation: number): Promise<void> {
    if (!this.snapshot.hasMore || this.snapshot.supported === false) return;
    if (this.cursor) {
      let page: ListPage<T> | null;
      try { page = await this.page(generation, { cursor: this.cursor }); }
      catch (error) {
        if (!this.expired(error)) throw error;
        await this.reread(generation, this.prefixSize + PAGE_SIZE);
        return;
      }
      if (!page || !this.current(generation)) return;
      if (page.revision !== this.frozenRevision) throw new Error(t("paged-agents.inconsistent_page"));
      for (const row of page.sessions) this.frozen.set(this.identity(row), row);
      this.cursor = page.has_more ? page.next_cursor : undefined;
      // This page's revision/total belong to the original frozen view, not the newer delta checkpoint.
    }
    this.prefixSize += PAGE_SIZE;
    this.publish({ rows: this.visible(), hasMore: !!this.cursor || this.candidateCount() > this.prefixSize });
  }

  /** An expired frozen cursor: read a fresh view at least `depth` rows deep and swap it in at once, so the rows on
   * screen stay put instead of falling back to the first page (#4410). A revision change mid-read fails, without retry. */
  private async reread(generation: number, depth: number): Promise<void> {
    const frozen = new Map<string, T>();
    const cursors = new Set<string>();
    let cursor: string | undefined;
    const readRows = async (): Promise<ListPage<T> | null> => {
      let first: ListPage<T> | null = null;
      do {
        const page = await this.page(generation, cursor ? { cursor } : {});
        if (!page || !this.current(generation)) return null;
        if (first && page.revision !== first.revision) throw new Error(t("paged-agents.inconsistent_page"));
        first ??= page;
        for (const row of page.sessions) frozen.set(this.identity(row), row);
        cursor = page.has_more ? page.next_cursor : undefined;
        if (cursor && cursors.has(cursor)) throw new Error(t("paged-agents.repeated_cursor"));
        if (cursor) cursors.add(cursor);
      } while (cursor && frozen.size < depth);
      return first;
    };
    const [first] = await Promise.all([readRows(), this.readCounts(generation)]);
    if (!first || !this.current(generation)) return;
    this.frozen = frozen;
    this.overlay = new Map();
    this.cursor = cursor;
    this.frozenRevision = this.checkpoint = first.revision;
    this.prefixSize = depth;
    this.publish({ rows: this.visible(), total: first.total, hasMore: !!cursor || frozen.size > depth });
  }

  private candidates(): Map<string, T> {
    const rows = new Map(this.frozen);
    for (const [id, row] of this.overlay) {
      if (row === null) rows.delete(id);
      else rows.set(id, row);
    }
    return rows;
  }

  private candidateCount(): number { return this.candidates().size; }

  private visible(): T[] {
    return this.stabilize([...this.candidates().values()].sort(this.compare).slice(0, this.prefixSize));
  }

  private stabilize(rows: T[]): T[] {
    const old = new Map(this.snapshot.rows.map((row) => [this.identity(row), row]));
    const next = rows.map((row) => {
      const had = old.get(this.identity(row));
      return had !== undefined && sameValue(had, row) ? had : row;
    });
    return next.length === this.snapshot.rows.length && next.every((row, i) => row === this.snapshot.rows[i]) ? this.snapshot.rows : next;
  }
}
