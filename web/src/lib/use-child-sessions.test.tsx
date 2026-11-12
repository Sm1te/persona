// @vitest-environment jsdom
// The rows a parent's chat draws its children's cards from (#5120): the store's on a hub that pushes every row, the
// hub's `parent_session_id` list (summaries, held for the chat's cards, never put among the store's complete rows) on a
// paging one that folds, none on one that does not.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setToken } from "./api";
import { SESSIONS_HIERARCHY_FEATURE } from "./child-sessions";
import { SESSIONS_LIST_FEATURE } from "./session-list";
import { getSession, getState, reset, upsertSession } from "./store";
import type { Interaction, Session } from "./types";
import { InteractionCard } from "../views/chat/Interactions";
import { heldChild, sameRows, useChildSessions, type ChildSessions } from "./use-child-sessions";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const row = (sid: string, extra: Partial<Session> = {}): Session => ({ machine_id: "m", session_id: sid, title: sid, cwd: "/w", status: "idle", state: "idle", updated_at: "2026-11-12T10:00:00Z", ...extra });
let host: HTMLDivElement;
let root: Root;
let seen: ChildSessions[];
function Probe({ enabled = true, counted = true }: { enabled?: boolean; counted?: boolean }) {
  seen.push(useChildSessions("m", "p", counted, enabled));
  return null;
}
const flush = async () => {
  for (let i = 0; i < 6; i++) await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
};
const last = () => seen[seen.length - 1];

beforeEach(() => {
  setToken("tok");
  seen = [];
  host = document.createElement("div");
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  reset();
  vi.unstubAllGlobals();
});

describe("useChildSessions on a hub that pushes every row", () => {
  it("the rows folded under the session, oldest first, the same array across other sessions' pushes", async () => {
    Object.assign(getState(), { loaded: true });
    upsertSession(row("p"));
    upsertSession(row("b", { folded_under: "p", started_at: "2026-11-12T10:05:00Z" }));
    upsertSession(row("a", { folded_under: "p", started_at: "2026-11-12T10:01:00Z" }));
    upsertSession(row("x", { folded_under: "q" }));
    act(() => root.render(<Probe />));
    expect(last().rows.map((s) => s.session_id)).toEqual(["a", "b"]);
    expect(last().loaded).toBe(true);
    expect(heldChild("m", "a")).toBe(last().rows[0]);
    expect(heldChild("m", "x")).toBeUndefined();
    const before = last().rows;
    const renders = seen.length;
    act(() => upsertSession(row("x", { title: "moved", folded_under: "q" })));
    act(() => upsertSession(row("p", { title: "parent moved" })));
    expect(seen.length).toBe(renders);
    expect(last().rows).toBe(before);
    act(() => upsertSession(row("a", { folded_under: "p", started_at: "2026-11-12T10:01:00Z", state: "working" })));
    expect(last().rows).not.toBe(before);
    expect(last().rows[0].state).toBe("working");
  });

  it("off: none", () => {
    upsertSession(row("a", { folded_under: "p" }));
    act(() => root.render(<Probe enabled={false} />));
    expect(last().rows).toEqual([]);
  });
});

describe("useChildSessions on a paging hub", () => {
  it("reads only this session's children, as summaries, while open; the chat holds them until it closes", async () => {
    Object.assign(getState(), { loaded: true, features: [SESSIONS_LIST_FEATURE, SESSIONS_HIERARCHY_FEATURE] });
    const asked: string[] = [];
    vi.stubGlobal("fetch", vi.fn(async (url: string) => {
      const u = new URL(url, "http://x");
      asked.push(`${u.pathname}?${u.searchParams}`);
      const body = u.searchParams.get("counts") === "1"
        ? { total: 1, machine: {}, project: {}, group: {}, state: {}, revision: "r1" }
        : { sessions: [row("kid", { folded_under: "p", effort: "high", waiting: false })], remove: [], has_more: false, revision: "r1", total: 1 };
      return new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } });
    }));
    act(() => root.render(<Probe />));
    await flush();
    expect(last().rows.map((s) => s.session_id)).toEqual(["kid"]);
    expect(last().loaded).toBe(true);
    const read = asked.find((a) => !a.includes("counts=1"))!;
    expect(read).toContain("parent_session_id=p");
    expect(read).toContain("machine_id=m");
    expect(read).toContain("summary=1");
    expect(heldChild("m", "kid")?.effort).toBe("high");
    // the child's request card on the parent's page can be answered: its row is the one the chat holds
    const ask = { id: "ix_kid", machine_id: "m", session_id: "kid", folded_under: "p", session_title: "kid", kind: "permission", tool_name: "Bash", tool_input: { command: "ls" } } as Interaction;
    const cardHost = document.createElement("div");
    const cardRoot = createRoot(cardHost);
    act(() => cardRoot.render(<InteractionCard ix={ask} />));
    expect(cardHost.querySelector("fieldset")?.disabled).toBe(false);
    act(() => cardRoot.unmount());
    // a summary is not a complete row: the store's complete rows never get it
    expect(getSession("m", "kid")).toBeUndefined();
    act(() => root.unmount());
    expect(heldChild("m", "kid")).toBeUndefined();
    root = createRoot(host);
  });

  it("a session whose row counts no children: nothing asked", async () => {
    Object.assign(getState(), { loaded: true, features: [SESSIONS_LIST_FEATURE, SESSIONS_HIERARCHY_FEATURE] });
    const fetcher = vi.fn();
    vi.stubGlobal("fetch", fetcher);
    act(() => root.render(<Probe counted={false} />));
    await flush();
    expect(last()).toEqual({ rows: [], loaded: true });
    expect(fetcher).not.toHaveBeenCalled();
  });

  it("a paging hub that does not fold: none, and nothing asked", async () => {
    Object.assign(getState(), { loaded: true, features: [SESSIONS_LIST_FEATURE] });
    const fetcher = vi.fn();
    vi.stubGlobal("fetch", fetcher);
    upsertSession(row("kid", { folded_under: "p" }));
    act(() => root.render(<Probe />));
    await flush();
    expect(last().rows).toEqual([]);
    expect(last().loaded).toBe(true);
    expect(fetcher).not.toHaveBeenCalled();
  });
});

describe("sameRows", () => {
  it("keeps the earlier array while it holds the same rows", () => {
    const a = row("a"), b = row("b");
    const prev = [a, b];
    expect(sameRows(prev, [a, b])).toBe(prev);
    expect(sameRows(prev, [b, a])).not.toBe(prev);
    expect(sameRows(undefined, prev)).toBe(prev);
  });
});
