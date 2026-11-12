// Child sessions as cards in their parent's chat (#5120): the exchange a card goes in and where among its steps, and that
// the placement holds still while nothing moves it.
import { describe, expect, it } from "vitest";
import { childCall, childCardKey, childPlaces, childSessionOf, exchangeStarts, isChildSessionStep, placeChildren, withChildSessions, type ChildPlace } from "./child-cards";
import { sameValue } from "./stable-lines";
import { chatLines, exchangeSteps, segments, type ChatLine, type Step } from "./turns";
import type { Item, Session } from "./types";
import { workBlocks, type WorkGroupNode } from "./work-groups";

const T0 = Date.parse("2026-11-12T10:00:00Z");
const ts = (s: number) => new Date(T0 + s * 1000).toISOString();
const row = (sid: string, extra: Partial<Session> = {}): Session => ({ machine_id: "m", session_id: sid, title: sid, cwd: "/w", status: "idle", state: "idle", folded_under: "p", ...extra });
const step = (id: string, seq: number, s?: number): Step => ({ kind: "tool", id, use: { id, seq, ...(s !== undefined ? { ts: ts(s) } : {}), role: "assistant", kind: "tool_use", tool_name: "Bash", tool_use_id: `c-${id}` }, images: [] });
const ids = (steps: Step[]) => steps.map((s) => s.id);

describe("childPlaces", () => {
  it("places a child by its start, else by when it was first seen updated, and keeps that time", () => {
    const seen = new Map<string, string>();
    expect(childPlaces([row("a", { started_at: ts(5), updated_at: ts(50) }), row("b", { updated_at: ts(20) })], seen)).toEqual([
      { sid: "a", at: ts(5) },
      { sid: "b", at: ts(20) },
    ]);
    // b works on: its updates do not move its card
    expect(childPlaces([row("b", { updated_at: ts(90) })], seen)).toEqual([{ sid: "b", at: ts(20) }]);
  });
});

describe("placeChildren", () => {
  const ex = [{ id: "e1", start: ts(0) }, { id: "e2", start: ts(100) }, { id: "e3", start: ts(200) }];
  const kid = (sid: string, s: number): ChildPlace => ({ sid, at: ts(s) });

  it("puts each child in the exchange going on when it started, oldest first", () => {
    const out = placeChildren(ex, [kid("b", 150), kid("a", 120), kid("c", 10)], false);
    expect(out.get("e1")?.map((c) => c.sid)).toEqual(["c"]);
    expect(out.get("e2")?.map((c) => c.sid)).toEqual(["a", "b"]);
    expect(out.has("e3")).toBe(false);
  });

  it("one that started after the last exchange started is in the latest one", () => {
    expect(placeChildren(ex, [kid("late", 9000)], false).get("e3")?.map((c) => c.sid)).toEqual(["late"]);
  });

  it("one that started before the loaded history waits for it, then goes in the first exchange", () => {
    expect(placeChildren(ex, [kid("old", -500)], false).size).toBe(0);
    expect(placeChildren(ex, [kid("old", -500)], true).get("e1")?.map((c) => c.sid)).toEqual(["old"]);
  });

  it("one without a time goes in the latest exchange; an empty chat draws none", () => {
    expect(placeChildren(ex, [{ sid: "x", at: "" }], false).get("e3")?.map((c) => c.sid)).toEqual(["x"]);
    expect(placeChildren([], [kid("a", 5)], true).size).toBe(0);
  });

  it("an exchange without a known start is passed over", () => {
    expect(placeChildren([{ id: "e1", start: ts(0) }, { id: "e2" }], [kid("a", 50)], false).get("e1")?.map((c) => c.sid)).toEqual(["a"]);
  });

  it("holds the same while nothing moved, in whatever order the children come", () => {
    const a = placeChildren(ex, [kid("a", 120), kid("b", 10)], false);
    const b = placeChildren(ex, [kid("b", 10), kid("a", 120)], false);
    expect(sameValue(a, b)).toBe(true);
    expect(sameValue(placeChildren(ex, [kid("a", 220), kid("b", 10)], false), a)).toBe(false);
  });
});

describe("withChildSessions", () => {
  const steps = [step("a", 1, 0), step("b", 2, 10), step("c", 3, 30)];

  it("puts each card after the last step that started before it, in the order the children started", () => {
    const out = withChildSessions(steps, [{ sid: "k1", at: ts(5) }, { sid: "k2", at: ts(20) }, { sid: "k3", at: ts(25) }]);
    expect(ids(out)).toEqual(["a", "child:k1", "b", "child:k2", "child:k3", "c"]);
    const card = out[1] as Extract<Step, { kind: "tool" }>;
    expect(isChildSessionStep(card)).toBe(true);
    expect(childSessionOf(card)).toBe("k1");
    // a card the chat's tool rows never draw, known by its own call (what a link brings into view)
    expect(card.use).toMatchObject({ tool_name: "Agent", tool_use_id: childCall("k1"), seq: 1, ts: ts(0) });
    expect(isChildSessionStep(steps[0])).toBe(false);
  });

  it("before every step, at the start; after them, at the end; nothing placed, the same array", () => {
    expect(ids(withChildSessions(steps, [{ sid: "k", at: ts(-5) }]))).toEqual(["child:k", "a", "b", "c"]);
    expect(ids(withChildSessions(steps, [{ sid: "k", at: ts(99) }]))).toEqual(["a", "b", "c", "child:k"]);
    expect(ids(withChildSessions([], [{ sid: "k", at: ts(1) }]))).toEqual(["child:k"]);
    expect(withChildSessions(steps, undefined)).toBe(steps);
    expect(withChildSessions(steps, [])).toBe(steps);
  });

  it("steps without a time are passed over: the card follows the last step known to have started before it", () => {
    expect(ids(withChildSessions([step("a", 1, 0), step("x", 2), step("c", 3, 30)], [{ sid: "k", at: ts(10) }]))).toEqual(["a", "child:k", "x", "c"]);
  });

  it("a card at the start of the work leaves the group's key, and so what the reader opened, as it was", () => {
    const without = workBlocks(steps, { live: false });
    const withKid = workBlocks(withChildSessions(steps, [{ sid: "k", at: ts(-5) }]), { live: false });
    const g = (n: typeof without) => n.find((x): x is WorkGroupNode => x.kind === "group");
    expect(g(withKid)?.id).toBe(g(without)?.id);
    expect(g(withKid)?.children.length).toBe(4);
  });

  it("a child that started long after the exchange's steps neither splits its work nor lengthens it", () => {
    const group = (n: ReturnType<typeof workBlocks>) => n.filter((x): x is WorkGroupNode => x.kind === "group");
    const without = group(workBlocks(steps, { live: false }));
    const late = group(workBlocks(withChildSessions(steps, [{ sid: "k", at: ts(9000) }]), { live: false }));
    expect(late.length).toBe(1);
    expect(late[0].durationMs).toBe(without[0].durationMs);
  });

  it("a nested child (a child's own child, folded under the same session) is a card of its own, by its start", () => {
    const out = withChildSessions(steps, [{ sid: "kid", at: ts(5) }, { sid: "grandkid", at: ts(12) }]);
    expect(ids(out)).toEqual(["a", "child:kid", "b", "child:grandkid", "c"]);
  });
});

describe("childCardKey", () => {
  it("is the chat's scope and the child", () => {
    expect(childCardKey("m/p:", "k")).toBe("m/p:child:k");
  });
});

describe("exchangeStarts", () => {
  const it_ = (id: string, seq: number, role: Item["role"], kind: Item["kind"], s: number, extra: Partial<Item> = {}): Item => ({ id: `${id}#0`, seq, role, kind, ts: ts(s), ...extra });
  it("lists the exchanges the chat draws with their starts, a quiet round's too", () => {
    const items = [
      it_("u1", 1, "user", "text", 0, { text: "first" }),
      it_("a1", 2, "assistant", "text", 5, { text: "done" }),
      it_("u2", 3, "user", "text", 100, { text: "second" }),
      it_("a2", 4, "assistant", "text", 110, { text: "done too" }),
    ];
    const lines = chatLines(items);
    const starts = exchangeStarts(lines);
    expect(starts.map((e) => e.start)).toEqual([ts(0), ts(100)]);
    const segs = segments(items);
    expect(starts.map((e) => e.id)).toEqual(segs.map((s) => s.id));
    expect(exchangeSteps(segs[0]).length).toBeGreaterThan(0);
    const nested: ChatLine[] = [{ kind: "supervision", id: "r", live: false, lines }];
    expect(exchangeStarts(nested)).toEqual(starts);
  });
});
