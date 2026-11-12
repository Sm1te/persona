// A hub that folds child sessions (`sessions_hierarchy`, #5120): every read of the Agents list and its counts asks for
// the parents only (`roots=1`), and a list that did not ask before starts over.
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "./api";
import { type ListCounts, type ListPage, SessionList } from "./session-list";

vi.mock("./api", async (original) => ({ ...await original<typeof import("./api")>(), api: vi.fn() }));

type Row = { id: string; rank: number };
const rows = (n: number, start = 0): Row[] => Array.from({ length: n }, (_, i) => ({ id: String(start + i), rank: 1_000 - start - i }));
const page = (sessions: Row[], patch: Partial<ListPage<Row>> = {}): ListPage<Row> => ({ sessions, remove: [], has_more: false, revision: "r1", total: sessions.length, ...patch });
const counts: ListCounts = { total: 60, machine: {}, project: {}, group: {}, state: { needs_you: 1, idle: 59 }, revision: "r1" };
const params = (path: string) => Object.fromEntries(new URL(path, "https://example.com").searchParams);
const make = (roots?: boolean) => new SessionList<Row>("/sessions", (r) => r.id, (a, b) => b.rank - a.rank, undefined, undefined, { roots, pageCounts: () => false });
const calls = () => vi.mocked(api).mock.calls.map(([path]) => params(path));

beforeEach(() => {
  vi.mocked(api).mockReset().mockImplementation((path) => Promise.resolve(params(path).counts ? counts
    : params(path).cursor ? page(rows(10, 50), { revision: "r1" })
    : params(path).since ? page([], { revision: "r2", total: 60 })
    : page(rows(50), { has_more: true, next_cursor: "c1", total: 60 })) as ReturnType<typeof api>);
});

describe("SessionList roots (#5120)", () => {
  it("sends nothing new by default", async () => {
    const list = make();
    await list.load();
    expect(list.isRoots()).toBe(false);
    expect(calls().every((p) => !("roots" in p))).toBe(true);
  });

  it("sends roots=1 with the first page, its counts, the next page and the deltas", async () => {
    const list = make(true);
    await list.load();
    await list.more();
    list.invalidate();
    await vi.waitFor(() => expect(calls().some((p) => p.since)).toBe(true));
    const all = calls();
    expect(all.length).toBeGreaterThanOrEqual(4);
    expect(all.every((p) => p.roots === "1")).toBe(true);
    expect(all.some((p) => p.counts === "1")).toBe(true);
    expect(all.some((p) => p.cursor === "c1")).toBe(true);
  });

  it("starts over when the hub starts or stops folding", async () => {
    const list = make();
    await list.load();
    expect(list.getSnapshot().rows).toHaveLength(50);
    list.setRoots(true);
    expect(list.isRoots()).toBe(true);
    expect(list.getSnapshot()).toMatchObject({ rows: [], loaded: false });
    vi.mocked(api).mockClear();
    await list.load();
    expect(calls().every((p) => p.roots === "1" && !p.since)).toBe(true);
    // the same value again keeps what it has
    list.setRoots(true);
    expect(list.getSnapshot().loaded).toBe(true);
  });
});
