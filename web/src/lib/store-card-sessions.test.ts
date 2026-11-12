// A child session's card on a paging hub (#5120): the Agents list's pages leave child sessions out (roots), so the store
// looks the card's session up once, and the card can be answered wherever it shows.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { connect, getSession, reset } from "./store";
import { interactionCanAnswer } from "./format";
import type { Interaction, Session } from "./types";

class Socket {
  static OPEN = 1;
  static last: Socket;
  readyState = 1;
  onopen?: () => void;
  onmessage?: (event: { data: string }) => void;
  constructor(public url: string) { Socket.last = this; }
  send() {}
  close() { this.readyState = 3; }
  push(value: unknown) { this.onmessage?.({ data: JSON.stringify(value) }); }
}
const child: Session = { machine_id: "demo", session_id: "kid", title: "Add a remember-me option", cwd: "/demo", status: "blocked", state: "needs_you", updated_at: "2026-01-01T00:00:00Z", source_kind: "runtime", capabilities: { read: true, approve: true }, folded_under: "lead" };
const card: Interaction = { id: "ix1", machine_id: "demo", session_id: "kid", kind: "permission", tool_name: "Bash", folded_under: "lead" };
let lookups: URL[];

beforeEach(() => {
  vi.useFakeTimers();
  lookups = [];
  vi.stubGlobal("WebSocket", Socket);
  vi.stubGlobal("location", { protocol: "http:", host: "demo" });
  vi.stubGlobal("localStorage", { getItem: () => "demo-token" });
  vi.stubGlobal("window", { setTimeout, clearTimeout });
  vi.stubGlobal("fetch", vi.fn(async (path: string) => {
    const url = new URL(path, "http://demo");
    if (url.pathname === "/api/v1/sessions" && url.searchParams.has("session_id")) {
      lookups.push(url);
      return Response.json({ sessions: url.searchParams.get("session_id") === "kid" ? [child] : [], remove: [], total: 1, has_more: false, revision: "r1" });
    }
    if (url.pathname === "/api/v1/sessions") {
      if (url.searchParams.has("counts")) return Response.json({ total: 1, machine: {}, project: {}, group: {}, state: {}, revision: "r1" });
      return Response.json({ sessions: [{ machine_id: "demo", session_id: "lead", title: "lead", cwd: "/demo", status: "idle", state: "idle", updated_at: "2026-01-01T00:00:00Z", waiting: false, children: { total: 1, needs_you: 1 } }], remove: [], total: 1, has_more: false, revision: "r1" });
    }
    return Response.json({});
  }));
});
afterEach(() => { reset(); vi.useRealTimers(); vi.unstubAllGlobals(); });
// a push reaches the listeners once per frame, or after 100 ms without one (store.ts notifySoon)
const settle = () => vi.advanceTimersByTimeAsync(200);

describe("a child session's card on a paging hub", () => {
  it("looks the card's session up once, so it can be answered outside its parent's page", async () => {
    connect();
    Socket.last.push({ type: "hello", features: ["sessions_list", "sessions_lookup", "sessions_hierarchy"] });
    await settle();
    expect(getSession("demo", "kid")).toBeUndefined();
    Socket.last.push({ type: "interaction", interaction: card });
    await settle();
    expect(lookups).toHaveLength(1);
    expect(lookups[0].searchParams.get("machine_id")).toBe("demo");
    expect(interactionCanAnswer(card, getSession("demo", "kid"))).toBe(true);
    // the same card again asks nothing more
    Socket.last.push({ type: "interaction", interaction: { ...card, summary: "npm run migrate" } });
    await settle();
    expect(lookups).toHaveLength(1);
  });

  it("asks nothing for a card whose session is listed, or without exact lookups", async () => {
    connect();
    Socket.last.push({ type: "hello", features: ["sessions_list"] });
    await settle();
    Socket.last.push({ type: "interaction", interaction: card });
    await settle();
    expect(lookups).toHaveLength(0);
  });
});
