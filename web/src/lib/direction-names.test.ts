import { describe, expect, it } from "vitest";
import { countsLabel, directionLabel, directionNames, leaderCounts, totalCounts } from "./direction-names";

describe("direction names", () => {
  it("keep the current leaders' names by id, leaving out ended ones and blank names", () => {
    const names = directionNames([
      { id: "a", name: "Product" },
      { id: "b", name: "  " },
      { id: "c", name: "Old", ended: true },
      { id: "", name: "Nobody" },
    ]);
    expect([...names]).toEqual([["a", "Product"]]);
    expect(directionNames(undefined).size).toBe(0);
  });

  it("label a row with its name, else its title", () => {
    const names = new Map([["a", "Product"]]);
    expect(directionLabel(names, "a", "1110 | explore | x")).toBe("Product");
    expect(directionLabel(names, "b", "Trip plan")).toBe("Trip plan");
  });
});

describe("a lead's counts (#4331)", () => {
  const tt = (key: string, o: { count: number }) => `${key === "sidebar.leader_working" ? "在做" : "等你"} ${o.count}`;
  it("are read for current leaders only", () => {
    const c = leaderCounts([{ id: "a", counts: { working: 2, needs_you: 1 } }, { id: "b", ended: true, counts: { working: 1, needs_you: 0 } }, { id: "c" }]);
    expect([...c.entries()]).toEqual([["a", { working: 2, needs_you: 1 }]]);
  });
  it("say the parts that are not zero, nothing when both are", () => {
    expect(countsLabel({ working: 2, needs_you: 1 }, tt)).toBe("在做 2 · 等你 1");
    expect(countsLabel({ working: 0, needs_you: 3 }, tt)).toBe("等你 3");
    expect(countsLabel({ working: 0, needs_you: 0 }, tt)).toBe("");
    expect(countsLabel(undefined, tt)).toBe("");
  });
  it("add up for the folded section", () => {
    const c = new Map([["a", { working: 2, needs_you: 1 }], ["b", { working: 1, needs_you: 0 }]]);
    expect(totalCounts(c, ["a", "b", "gone"])).toEqual({ working: 3, needs_you: 1 });
  });
});
