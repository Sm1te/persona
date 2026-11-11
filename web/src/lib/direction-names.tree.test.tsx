// @vitest-environment jsdom
// The sidebar's 常驻 names and counts (lib/direction-names.ts) are taken from the one tree lib/direction-tree.ts reads
// (#4352): a sidebar and a screen drawing the tree cost one GET, and a change read for one shows in the other.
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { readDirectionTree, resetDirectionNames, useDirectionNames, useLeaderCounts } from "./direction-names";
import { useDirectionTree } from "./direction-tree";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const tree = (working: number) => ({
  directions: [
    { id: "lead-a", name: "Product", counts: { working, needs_you: 1, stuck: 0, review: 0 }, tasks: [] },
    { id: "main", name: "", secretary: true, counts: { working: 9, needs_you: 9, stuck: 0, review: 0 }, tasks: [] },
  ],
});

let seen: { names?: ReadonlyMap<string, string>; counts?: ReadonlyMap<string, { working: number; needs_you: number }>; tree?: unknown } = {};
function Probe() {
  seen = { names: useDirectionNames("lead-a\u0000Growth"), counts: useLeaderCounts(), tree: useDirectionTree() };
  return null;
}

afterEach(() => {
  resetDirectionNames();
  vi.unstubAllGlobals();
});

describe("the 常驻 names and counts", () => {
  it("come from the one direction tree: one GET for the sidebar and the tree's screens, and follow its reads", async () => {
    let working = 2;
    const fetch = vi.fn(async (url: string) => new Response(JSON.stringify(url.includes("/directions/tree") ? tree(working) : {}), { headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetch);
    const host = document.createElement("div");
    const root = createRoot(host);
    act(() => root.render(<><Probe /><Probe /></>));
    await act(async () => { await readDirectionTree(); });
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(seen.names?.get("lead-a")).toBe("Product");
    expect(seen.counts?.get("lead-a")).toEqual({ working: 2, needs_you: 1 });
    expect(seen.counts?.has("main")).toBe(false);
    expect((seen.tree as { directions: unknown[] }).directions).toHaveLength(2);
    // a directions push: one more GET, and the counts follow
    working = 0;
    await act(async () => { await readDirectionTree(true); });
    expect(fetch).toHaveBeenCalledTimes(2);
    expect(seen.counts?.get("lead-a")).toEqual({ working: 0, needs_you: 1 });
    act(() => root.unmount());
  });

  it("fall back to the leaders list on a hub without the tree", async () => {
    const fetch = vi.fn(async (url: string) => (url.includes("/directions/tree") ? new Response("{}", { status: 404 }) : new Response(JSON.stringify({ leaders: [{ id: "lead-a", name: "Old name" }] }), { headers: { "Content-Type": "application/json" } })));
    vi.stubGlobal("fetch", fetch);
    const host = document.createElement("div");
    const root = createRoot(host);
    act(() => root.render(<Probe />));
    await vi.waitFor(() => expect(seen.names?.get("lead-a")).toBe("Old name"));
    expect(seen.tree).toBeNull();
    expect(seen.counts?.size).toBe(0);
    act(() => root.unmount());
  });
});
