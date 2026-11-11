// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Machine, Session } from "./types";

import { buildDirectionAgentTree } from "./direction-agent-tree";
import {
  directionStateName,
  directionTitle,
  directionTreeGroups,
  directionTreeNow,
  groupsByDirection,
  leaderDirections,
  normalizeTree,
  refreshDirectionTree,
  resetDirectionTree,
  directionTreeAwaitsHello,
  readDirectionTreeOnce,
  shownSecretary,
  type DirectionTree,
} from "./direction-tree";

const counts = (working = 0, needs_you = 0, stuck = 0, review = 0) => ({ working, needs_you, stuck, review });

const session = (mid: string, sid: string, state: Session["state"] = "working", extra: Partial<Session> = {}): Session => ({ machine_id: mid, session_id: sid, title: sid, cwd: "/code/app", status: "", state, ...extra });

const TREE: DirectionTree = {
  directions: [
    {
      id: "lead-a",
      name: "Product",
      counts: counts(1, 1),
      tasks: [
        { id: "t1", title: "Ask", state: "needs_you", agents: [{ machine_id: "m", session_id: "s1", title: "one", state: "needs_you", children: [{ machine_id: "m", session_id: "s2", title: "two", state: "working" }] }] },
        { id: "t2", title: "Build", state: "working", agents: [{ machine_id: "m", session_id: "gone", title: "gone", state: "", children: [{ machine_id: "m", session_id: "s3", title: "three", state: "working" }] }] },
      ],
    },
    { id: "lead-b", name: "Infra", counts: counts(), tasks: [] },
    { id: "main", name: "", secretary: true, counts: counts(1), tasks: [{ id: "t3", title: "Own", state: "working", agents: [] }] },
  ],
};

describe("direction tree", () => {
  afterEach(() => {
    resetDirectionTree();
    vi.unstubAllGlobals();
  });

  it("is made safe to draw: missing lists empty, unknown states working, zero counts filled in", () => {
    const tree = normalizeTree({ directions: [{ id: "a", name: "A", counts: { working: 2 }, tasks: [{ id: "x", title: "X", state: "weird" }, { title: "no id" }] }, { name: "no id" }] });
    expect(tree.directions).toHaveLength(1);
    expect(tree.directions[0].counts).toEqual(counts(2));
    expect(tree.directions[0].tasks).toEqual([{ id: "x", title: "X", state: "working", agents: [] }]);
    expect(normalizeTree(null)).toEqual({ directions: [] });
    // a count that is not a finite number is 0, never drawn as "null"
    expect(normalizeTree({ directions: [{ id: "b", counts: { working: null, needs_you: "3", stuck: Infinity, review: 1 } }] }).directions[0].counts).toEqual(counts(0, 0, 0, 1));
  });

  it("groups by direction only with a leader; the secretary's group shows only with a leader and a task", () => {
    expect(groupsByDirection(TREE)).toBe(true);
    expect(leaderDirections(TREE).map((d) => d.id)).toEqual(["lead-a", "lead-b"]);
    expect(shownSecretary(TREE)?.id).toBe("main");
    const alone: DirectionTree = { directions: [TREE.directions[2]] };
    expect(groupsByDirection(alone)).toBe(false);
    expect(shownSecretary(alone)).toBeUndefined();
    const idle: DirectionTree = { directions: [TREE.directions[0], { ...TREE.directions[2], tasks: [] }] };
    expect(shownSecretary(idle)).toBeUndefined();
    expect(groupsByDirection(null)).toBe(false);
    expect(groupsByDirection(undefined)).toBe(false);
  });

  it("says the words of the contract", () => {
    expect(directionStateName("needs_you")).toBe("等你");
    expect(directionStateName("review")).toBe("待验收");
    expect(directionStateName("stuck")).toBe("卡住");
    expect(directionStateName("working")).toBe("在做");
    expect(directionTitle(TREE.directions[2])).toBe("书记官");
    expect(directionTitle({ id: "x", name: "", counts: counts() }, "Title")).toBe("Title");
  });

  it("builds the Agents tree: direction › task › agent › child, agents it does not hold last, empty directions left out", () => {
    const all = [session("m", "s1", "needs_you"), session("m", "s2"), session("m", "s3"), session("m", "loose", "idle")];
    const tree = buildDirectionAgentTree(TREE, all, [{ id: "m", online: true } as Machine], undefined, Date.parse("2026-11-11T00:00:00Z"));
    expect(tree.projects.map((p) => p.key)).toEqual(["direction-agents:lead-a"]);
    const [a] = tree.projects;
    expect(a.direction).toEqual({ counts: counts(1, 1), secretary: false });
    expect(a.todos.map((td) => [td.todoId, td.taskState, td.state])).toEqual([["t1", "needs_you", "needs_you"], ["t2", "working", "working"]]);
    // s2 nests under s1; the agent the list lacks gives its place to its child
    expect(a.todos[0].agents.map((n) => [n.session.session_id, n.children.map((c) => c.session.session_id)])).toEqual([["s1", ["s2"]]]);
    expect(a.todos[1].agents.map((n) => n.session.session_id)).toEqual(["s3"]);
    expect(a.state).toBe("needs_you");
    expect(tree.loose.flatMap((g) => g.sessions.map((s) => s.session_id))).toEqual(["loose"]);
  });

  it("puts the secretary's agents in a last 书记官 group, and drops a task none of whose agents the list holds", () => {
    const withSecretary: DirectionTree = {
      directions: [
        TREE.directions[0],
        {
          ...TREE.directions[2],
          tasks: [
            { id: "t3", title: "Own", state: "working", agents: [{ machine_id: "m", session_id: "s4", title: "four", state: "working" }] },
            { id: "t4", title: "Filtered", state: "stuck", agents: [{ machine_id: "m", session_id: "hidden", title: "hidden", state: "working" }] },
          ],
        },
      ],
    };
    const all = [session("m", "s1", "needs_you"), session("m", "s2"), session("m", "s3"), session("m", "s4"), session("m", "loose", "idle")];
    const tree = buildDirectionAgentTree(withSecretary, all, [{ id: "m", online: true } as Machine], undefined, Date.parse("2026-11-11T00:00:00Z"));
    expect(tree.projects.map((p) => p.key)).toEqual(["direction-agents:lead-a", "direction-agents:main"]);
    const secretary = tree.projects.at(-1)!;
    expect(secretary.name).toBe("书记官");
    expect(secretary.direction).toEqual({ counts: counts(1), secretary: true });
    expect(secretary.todos.map((td) => td.todoId)).toEqual(["t3"]);
    expect(secretary.todos[0].agents.map((n) => n.session.session_id)).toEqual(["s4"]);
    // a listed agent the tree does not hold stays in the loose groups
    expect(tree.loose.flatMap((g) => g.sessions.map((s) => s.session_id))).toEqual(["loose"]);
  });

  it("is read once for everyone asking while a read is out, again after it for a change; a 404 is no tree", async () => {
    let status = 200;
    const fetch = vi.fn(async () => (status === 200 ? new Response(JSON.stringify(TREE), { status: 200, headers: { "Content-Type": "application/json" } }) : new Response(JSON.stringify({ error: "not found" }), { status: 404, headers: { "Content-Type": "application/json" } })));
    vi.stubGlobal("fetch", fetch);
    // hello, the sidebar's names and a screen drawing the tree all at once: one GET
    const [a, b, c] = await Promise.all([readDirectionTreeOnce(), readDirectionTreeOnce(), readDirectionTreeOnce()]);
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(String((fetch.mock.calls[0] as unknown[])[0])).toContain("/api/v1/assistant/directions/tree");
    expect(a).toBe(b);
    expect(b).toBe(c);
    expect(directionTreeGroups()).toBe(true);
    // a directions push reads it again
    await refreshDirectionTree();
    expect(fetch).toHaveBeenCalledTimes(2);
    status = 404;
    expect(await refreshDirectionTree()).toBeNull();
    expect(directionTreeGroups()).toBe(false);
    expect(fetch).toHaveBeenCalledTimes(3);
  });

  it("serves a hello with a read sent since the socket opened, and reads again for one sent before", async () => {
    const fetch = vi.fn(async () => new Response(JSON.stringify(TREE), { status: 200, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetch);
    const opened = Date.now();
    await readDirectionTreeOnce(); // the first screen's read
    await readDirectionTreeOnce(opened); // the hello right after
    expect(fetch).toHaveBeenCalledTimes(1);
    await readDirectionTreeOnce(Date.now() + 1000); // a later reconnect's hello
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it("waits for a hello on its way instead of reading beside it", async () => {
    const fetch = vi.fn(async () => new Response(JSON.stringify(TREE), { status: 200, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetch);
    directionTreeAwaitsHello(true); // the socket is connecting
    const screen = readDirectionTreeOnce(); // the first screen, the sidebar's names
    await Promise.resolve();
    expect(fetch).not.toHaveBeenCalled();
    const hello = readDirectionTreeOnce(Date.now()); // the hello's own read
    directionTreeAwaitsHello(false);
    expect(await screen).toBe(await hello);
    expect(fetch).toHaveBeenCalledTimes(1);
    // a socket that closed before its hello: whoever waited reads it then
    resetDirectionTree();
    directionTreeAwaitsHello(true);
    const waiting = readDirectionTreeOnce();
    directionTreeAwaitsHello(false);
    expect((await waiting)?.directions).toHaveLength(3);
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it("is no tree when the answer has no directions (a layer in front of an older hub)", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("{}", { status: 200, headers: { "Content-Type": "application/json" } })));
    expect(await readDirectionTreeOnce()).toBeNull();
    expect(directionTreeNow()).toBeNull();
  });

  it("treats a failed first read as no tree, and a later read replaces it", async () => {
    let status = 500;
    const fetch = vi.fn(async () => new Response(JSON.stringify(status === 200 ? TREE : { error: "boom" }), { status, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetch);
    await refreshDirectionTree();
    expect(directionTreeNow()).toBeNull();
    status = 200;
    await refreshDirectionTree();
    expect(directionTreeGroups()).toBe(true);
    // a later failure keeps the tree it had
    status = 500;
    await refreshDirectionTree();
    expect(directionTreeGroups()).toBe(true);
  });

  it("asks once more after a read when a push comes while it is out", async () => {
    let answer: (r: Response) => void = () => {};
    const fetch = vi.fn(() => new Promise<Response>((r) => (answer = r)));
    vi.stubGlobal("fetch", fetch);
    const first = refreshDirectionTree();
    void refreshDirectionTree();
    void refreshDirectionTree();
    await vi.waitFor(() => expect(fetch).toHaveBeenCalledTimes(1));
    answer(new Response(JSON.stringify(TREE), { status: 200, headers: { "Content-Type": "application/json" } }));
    await first;
    await vi.waitFor(() => expect(fetch).toHaveBeenCalledTimes(2));
  });

  it("keeps the newer tree when two reads out at once answer in reverse order", async () => {
    const answers: ((r: Response) => void)[] = [];
    const fetch = vi.fn(() => new Promise<Response>((r) => answers.push(r)));
    vi.stubGlobal("fetch", fetch);
    const json = (tree: DirectionTree) => new Response(JSON.stringify(tree), { status: 200, headers: { "Content-Type": "application/json" } });
    const older = readDirectionTreeOnce(); // sent before the socket opened
    await vi.waitFor(() => expect(fetch).toHaveBeenCalledTimes(1));
    const newer = readDirectionTreeOnce(Date.now() + 1000); // the hello's, sent after
    await vi.waitFor(() => expect(fetch).toHaveBeenCalledTimes(2));
    const renamed: DirectionTree = { directions: [{ ...TREE.directions[0], name: "Newer" }, ...TREE.directions.slice(1)] };
    answers[1](json(renamed));
    await newer;
    expect(directionTreeNow()?.directions[0].name).toBe("Newer");
    answers[0](json(TREE));
    // the stale answer is not applied; its caller gets the current tree
    expect((await older)?.directions[0].name).toBe("Newer");
    expect(directionTreeNow()?.directions[0].name).toBe("Newer");
  });

  it("keeps the newer tree when an older read fails after it", async () => {
    const answers: ((r: Response) => void)[] = [];
    vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>((r) => answers.push(r))));
    const older = readDirectionTreeOnce();
    await vi.waitFor(() => expect(answers).toHaveLength(1));
    const newer = readDirectionTreeOnce(Date.now() + 1000);
    await vi.waitFor(() => expect(answers).toHaveLength(2));
    answers[1](new Response(JSON.stringify(TREE), { status: 200, headers: { "Content-Type": "application/json" } }));
    await newer;
    answers[0](new Response(JSON.stringify({ error: "not found" }), { status: 404, headers: { "Content-Type": "application/json" } }));
    await older;
    expect(directionTreeGroups()).toBe(true);
  });
});
