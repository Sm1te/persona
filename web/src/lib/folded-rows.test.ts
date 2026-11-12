import { describe, expect, it } from "vitest";
import { childCards, foldedRow, foldRows, homeKey, sessionCard } from "./folded-rows";
import type { Interaction, Session } from "./types";

const row = (sid: string, extra: Partial<Session> = {}): Session => ({ machine_id: "m", session_id: sid, title: sid, cwd: "/w", status: "idle", state: "idle", ...extra });

describe("folded rows (#5120)", () => {
  it("keeps a row without children, or whose folded state is its own", () => {
    const plain = row("a");
    expect(foldedRow(plain)).toBe(plain);
    const unread = row("p", { state: "unread", children: { total: 1, working: 1 } });
    expect(foldedRow(unread)).toBe(unread);
  });

  it("gives a parent the same folded copy until the hub sends a new row", () => {
    const parent = row("p", { children: { total: 2, working: 1 } });
    const copy = foldedRow(parent);
    expect(copy).not.toBe(parent);
    expect(copy.state).toBe("working");
    expect(foldedRow(parent)).toBe(copy);
    expect(foldedRow(copy)).toBe(copy);
    const next = { ...parent, children: { total: 2, needs_you: 1 } };
    expect(foldedRow(next).state).toBe("needs_you");
  });

  it("drops the children of a listed parent, keeps the list itself when nothing folds", () => {
    const rows = [row("a"), row("b")];
    expect(foldRows(rows)).toBe(rows);
    const parent = row("p", { state: "unread", children: { total: 1, working: 1 } });
    const folded = foldRows([parent, row("c", { folded_under: "p" }), row("x", { folded_under: "gone" })]);
    expect(folded.map((s) => s.session_id)).toEqual(["p", "x"]);
    expect(folded[0]).toBe(parent);
  });

  it("shows a listed parent folded, keeps the other rows, and gives the same list the same folded list", () => {
    const parent = row("p", { children: { total: 2, working: 1 } });
    const rows = [parent, row("c1", { folded_under: "p", state: "working" }), row("c2", { folded_under: "p" }), row("x")];
    const out = foldRows(rows);
    expect(out.map((s) => s.session_id)).toEqual(["p", "x"]);
    expect(out[0].state).toBe("working");
    expect(out[1]).toBe(rows[3]);
    expect(foldRows(rows)).toBe(out);
  });

  it("a parent on another machine is not the parent", () => {
    const rows = [row("c", { folded_under: "gone" }), row("p", { machine_id: "other" }), row("d", { folded_under: "p" })];
    expect(foldRows(rows).map((s) => s.session_id)).toEqual(["c", "p", "d"]);
  });

  it("puts a child's card on its parent", () => {
    const card = { id: "1", machine_id: "m", session_id: "c", folded_under: "p" } as Interaction;
    const own = { id: "2", machine_id: "m", session_id: "p" } as Interaction;
    const elsewhere = { id: "3", machine_id: "n", session_id: "c", folded_under: "p" } as Interaction;
    expect([card, own].map(homeKey)).toEqual(["m/p", "m/p"]);
    expect(childCards([card, own, elsewhere], "m", "p").map((ix) => ix.id)).toEqual(["1"]);
  });
});

describe("sessionCard", () => {
  const card = (id: string, sid: string, folded_under?: string): Interaction => ({ id, machine_id: "m", session_id: sid, kind: "question", ...(folded_under ? { folded_under } : {}) });
  it("is the session's own card first, else one of its children's", () => {
    expect(sessionCard([card("c", "kid", "p"), card("o", "p")], "m", "p")?.id).toBe("o");
    expect(sessionCard([card("c", "kid", "p")], "m", "p")?.id).toBe("c");
    expect(sessionCard([card("c", "kid", "q")], "m", "p")).toBeUndefined();
  });
});
