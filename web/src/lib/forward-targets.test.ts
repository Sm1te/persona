import { describe, expect, it } from "vitest";
import { FORWARD_MEMORY_LIMIT, forgetForwards, forwardSessions, forwardTargets, PERSONA_TARGET, rememberForward, sessionTarget } from "./forward-memory";
import { forwardSections, takesForward, targetDetail } from "./forward-targets";
import type { Session } from "./types";

// Forward to's lists (#3899, iOS ForwardTests' targets): the same sessions give the same lists as on iOS.

const session = (machine: string, id: string, updated: number, fields: Partial<Session> = {}): Session =>
  ({ machine_id: machine, session_id: id, title: id, cwd: `/w/${id}`, status: "idle", state: "idle", updated_at: new Date(updated * 1000).toISOString(), ...fields }) as Session;

const all = [
  session("macbook", "here", 50),
  session("macbook", "tests", 100, { agent: "codex", space_name: "weather" }),
  session("studio", "photos", 200, { status: "working", state: "working" }),
  session("studio", "ended", 300, { status: "offline", state: "offline" }),
  session("server", "notes", 400, { assistant: true }),
  session("server", "main", 500, { assistant: true }),
  session("server", "health", 600, { assistant: true, space: "health" }),
  session("nas", "away", 700),
  session("macbook", "archived", 800, { archived: true }),
  session("macbook", "readonly", 900, { source_kind: "codex_history", capabilities: { read: true } }),
];
const machines = [
  { id: "macbook", name: "MacBook Pro" },
  { id: "studio", name: "Mac Studio" },
  { id: "nas", name: "NAS" },
];

const sections = (recents: string[] = [], query = "") =>
  forwardSections({ all, from: "macbook/here", recents, main: "server/main", machines, query, online: (mid) => mid !== "nas", conversationTitle: (s) => s.title });
const ids = (s: ReturnType<typeof sections>) => s.flatMap((x) => x.sessions).map((x) => x.session_id);

describe("Forward to's lists", () => {
  it("are each computer's live Agents, latest first", () => {
    const s = sections();
    expect(s.map((x) => x.title)).toEqual(["MacBook Pro", "Mac Studio"]);
    // not this chat, not an ended Agent, not an offline computer's, no Persona conversation (Persona has its row),
    // nothing archived or read-only
    expect(ids(s)).toEqual(["tests", "photos"]);
  });

  it("start with the recent targets, each listed once", () => {
    const r = sections(["studio/photos", "server/health", "macbook/here", "gone/x", "server/main", "server/notes"]);
    expect(r[0]).toMatchObject({ kind: "recent" });
    expect(r[0].sessions.map((x) => x.session_id)).toEqual(["photos", "notes"]);
    expect(ids(r.slice(1))).toEqual(["tests"]);
  });

  it("show at most five recent targets", () => {
    const many = Array.from({ length: 8 }, (_, i) => session("macbook", `r${i}`, 1000 + i));
    const r = forwardSections({ all: many, from: "x/y", recents: many.map((s) => `macbook/${s.session_id}`), machines, query: "", online: () => true, conversationTitle: (s) => s.title });
    expect(r[0].sessions).toHaveLength(5);
  });

  it("search also finds ended Agents and Persona's other conversations, never Health, the main chat or an offline computer's", () => {
    expect(ids(sections([], "mac studio"))).toEqual(["ended", "photos"]);
    expect(ids(sections([], "notes"))).toEqual(["notes"]);
    expect(ids(sections([], "codex weather"))).toEqual(["tests"]);
    for (const q of ["health", "main", "away", "here", "archived"]) expect(sections([], q)).toEqual([]);
  });

  it("list a chat the page holds twice once", () => {
    const twice = forwardSections({ all: [...all, all[1]], from: "macbook/here", recents: [], machines, query: "", online: () => true, conversationTitle: (s) => s.title });
    expect(ids(twice).filter((x) => x === "tests")).toHaveLength(1);
  });

  it("describe a row by who answers and its project, and its computer where rows mix them", () => {
    expect(targetDetail(all[1])).toBe("Codex · weather");
    expect(targetDetail(all[2], "Mac Studio")).toBe("Claude · photos · Mac Studio");
    expect(targetDetail(all[4])).toBe("Persona");
    expect(takesForward(all[0], "macbook/here", true, () => true)).toBe(false);
  });
});

class MemoryStorage {
  private m = new Map<string, string>();
  getItem(k: string) { return this.m.get(k) ?? null; }
  setItem(k: string, v: string) { this.m.set(k, v); }
  removeItem(k: string) { this.m.delete(k); }
}

describe("the recent targets", () => {
  it("are kept latest first, ids only, at most eight, and forgotten on sign-out", () => {
    const store = new MemoryStorage() as unknown as Storage;
    rememberForward(sessionTarget("m", "a"), store);
    rememberForward(PERSONA_TARGET, store);
    rememberForward(sessionTarget("m", "b"), store);
    rememberForward(sessionTarget("m", "a"), store);
    expect(forwardTargets(store)).toEqual(["session:m/a", "session:m/b", "persona"]);
    expect(forwardSessions(store)).toEqual(["m/a", "m/b"]);
    for (let i = 0; i < 20; i++) rememberForward(sessionTarget("m", `s${i}`), store);
    expect(forwardTargets(store)).toHaveLength(FORWARD_MEMORY_LIMIT);
    forgetForwards(store);
    expect(forwardTargets(store)).toEqual([]);
  });

  it("skip what does not read", () => {
    const store = new MemoryStorage() as unknown as Storage;
    store.setItem("forward.recentTargets", JSON.stringify(["persona", 3, "new", "session:", "session:m/a"]));
    expect(forwardTargets(store)).toEqual(["persona", "session:m/a"]);
    store.setItem("forward.recentTargets", "{not json");
    expect(forwardTargets(store)).toEqual([]);
  });
});
