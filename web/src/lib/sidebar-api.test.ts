import { afterEach, describe, expect, it, vi } from "vitest";
import { AGENTS_FOLD, cycleGrouping, GROUPING_KEY, groupingLabel, nextGrouping, onFoldAll, onGrouping, setAgentsFolded, setAllFolded, stepIndex } from "./sidebar-api";

afterEach(() => vi.unstubAllGlobals());

describe("folding every group", () => {
  it("tells every listener, until it is taken back", () => {
    const seen: boolean[] = [];
    const off = onFoldAll((f) => seen.push(f));
    setAllFolded(true);
    setAllFolded(false);
    off();
    setAllFolded(true);
    expect(seen).toEqual([true, false]);
  });

  it("moves only one block's groups when asked for its scope, every group otherwise (#3027)", () => {
    const agents: boolean[] = [];
    const other: boolean[] = [];
    const offA = onFoldAll((f) => agents.push(f), AGENTS_FOLD);
    const offB = onFoldAll((f) => other.push(f));
    setAllFolded(true, AGENTS_FOLD);
    setAllFolded(false);
    offA();
    offB();
    expect(agents).toEqual([true, false]);
    expect(other).toEqual([false]);
  });

  it("opens the spaces and tab groups folded by hand that are not drawn when the Agents list opens all", () => {
    const kept = new Map<string, string>([["fold:space:mac/a", "1"], ["fold:tree:mac/ws/1", "1"], ["fold:tree:mac", "0"], ["fold:persona", "1"], ["fold:term:m:mac", "1"], ["other", "1"]]);
    const store = { get length() { return kept.size; }, key: (i: number) => [...kept.keys()][i] ?? null, getItem: (k: string) => kept.get(k) ?? null, setItem: (k: string, v: string) => void kept.set(k, v) };
    const seen: boolean[] = [];
    const off = onFoldAll((f) => seen.push(f), AGENTS_FOLD);
    setAgentsFolded(true, store);
    expect(kept.get("fold:space:mac/a")).toBe("1"); // folding leaves what isn't drawn as it is
    setAgentsFolded(false, store);
    off();
    expect(seen).toEqual([true, false]);
    expect(Object.fromEntries(kept)).toEqual({ "fold:space:mac/a": "0", "fold:tree:mac/ws/1": "0", "fold:tree:mac": "0", "fold:persona": "1", "fold:term:m:mac": "1", other: "1" });
  });
});

describe("the sidebar's grouping", () => {
  it("goes round the groupings", () => {
    expect(nextGrouping("todo")).toBe("machine");
    expect(nextGrouping("machine")).toBe("repo");
    expect(nextGrouping("state")).toBe("direction");
    expect(nextGrouping("direction")).toBe("todo");
  });
  it("skips the ones a hub does not offer", () => {
    expect(nextGrouping("state", ["machine", "repo", "space", "state"])).toBe("machine");
    expect(nextGrouping("todo", ["machine", "repo"])).toBe("repo");
  });
  it("keeps the choice in this browser, as the menu does, and tells the sidebar", () => {
    const store = new Map<string, string>([[GROUPING_KEY, "repo"]]);
    vi.stubGlobal("localStorage", { getItem: (k: string) => store.get(k) ?? null, setItem: (k: string, v: string) => void store.set(k, v) });
    const seen: string[] = [];
    const off = onGrouping((g) => seen.push(g));
    expect(cycleGrouping()).toBe("space");
    expect(store.get(GROUPING_KEY)).toBe("space");
    expect(seen).toEqual(["space"]);
    off();
    expect(groupingLabel("space")).toBe("按项目");
  });
  it("starts from by todo when nothing was chosen", () => {
    vi.stubGlobal("localStorage", { getItem: () => null, setItem: () => {} });
    expect(cycleGrouping()).toBe("machine");
  });
});

describe("walking the rows", () => {
  it("steps to the next and previous, wrapping round", () => {
    expect(stepIndex(5, 1, 1)).toBe(2);
    expect(stepIndex(5, 1, -1)).toBe(0);
    expect(stepIndex(5, 4, 1)).toBe(0);
    expect(stepIndex(5, 0, -1)).toBe(4);
  });
  it("starts at the first going down and the last going up when no row is open", () => {
    expect(stepIndex(5, -1, 1)).toBe(0);
    expect(stepIndex(5, -1, -1)).toBe(4);
  });
  it("has no row to step to in an empty list", () => {
    expect(stepIndex(0, -1, 1)).toBe(-1);
  });
});
