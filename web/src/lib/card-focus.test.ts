import { describe, expect, it } from "vitest";
import { cardState, focusedCard, focusedFirst, requestRoute } from "./card-focus";

describe("card focus", () => {
  const cards = [{ id: "a" }, { id: "b" }, { id: "c" }];
  it("moves the tapped card to the front and keeps the others' order", () => {
    expect(focusedFirst(cards, "c").map((c) => c.id)).toEqual(["c", "a", "b"]);
  });
  it("returns the same list when the card is first, not there, or none was tapped", () => {
    expect(focusedFirst(cards, "a")).toBe(cards);
    expect(focusedFirst(cards, "gone")).toBe(cards);
    expect(focusedFirst(cards, undefined)).toBe(cards);
  });
  it("reads the card only from the entry that carries it", () => {
    expect(focusedCard(cardState("b"))).toBe("b");
    expect(focusedCard(null)).toBeUndefined();
    expect(focusedCard({ overlay: "x" })).toBeUndefined();
  });

  it("opens a card where it is answered: its session, a child's parent, a worker's own link (its lead's Team, #5180)", () => {
    const ix = { id: "i", machine_id: "mac", session_id: "s" };
    expect(requestRoute(ix)).toEqual({ name: "session", mid: "mac", sid: "s" });
    expect(requestRoute({ ...ix, folded_under: "p" })).toEqual({ name: "session", mid: "mac", sid: "p" });
    expect(requestRoute({ ...ix, folded_under: "lead", folded_as: "worker" })).toEqual({ name: "session", mid: "mac", sid: "s" });
    expect(requestRoute({ ...ix, session_id: "" })).toEqual({ name: "request", id: "i" });
  });
});
