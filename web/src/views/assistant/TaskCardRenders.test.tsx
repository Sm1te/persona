// @vitest-environment jsdom
// A task card in the conversation renders again for a push about its own agent, its card or its machine, and not for
// one about another agent, another agent's card or another machine: its store selector works out what it draws
// (status, lead agent, question, place) instead of taking the whole session list, the cards and the machines.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
// every render of a TaskCard asks for its lead agent's workflows once: counted
const renders = vi.hoisted(() => ({ n: 0 }));
vi.mock("../../lib/workflows", async (load) => {
  const real = await load<typeof import("../../lib/workflows")>();
  return {
    ...real,
    runningWorkflows: (s: Parameters<typeof real.runningWorkflows>[0]) => {
      renders.n++;
      return real.runningWorkflows(s);
    },
  };
});
import { applyMachine, endInteraction, getState, refresh, reset, upsertSession } from "../../lib/store";
import { TaskCard } from "./Tasks";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const now = new Date().toISOString();
const TODOS = [{ id: "t1", title: "My task", origin: "c1", created_at: now, updated_at: now, agents: [{ machine_id: "m1", session_id: "s1", cwd: "/code/app" }] }];
const MACHINES = [
  { id: "m1", name: "MacBook", online: true },
  { id: "m2", name: "Studio", online: true },
];
const SESSIONS = [
  { machine_id: "m1", session_id: "s1", state: "needs_you", status: "needs_you", title: "a", updated_at: now },
  { machine_id: "m1", session_id: "s2", state: "working", status: "working", title: "b", updated_at: now },
];
const INTERACTIONS = [
  { id: "ix1", machine_id: "m1", session_id: "s1", kind: "permission", tool_name: "Bash", tool_input: { command: "swift test" } },
  { id: "ix2", machine_id: "m1", session_id: "s2", kind: "permission", tool_name: "Bash", tool_input: { command: "npm test" } },
];

let host: HTMLDivElement;
let root: Root;

beforeEach(async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn((url: string) => {
      const u = String(url);
      const body = u.includes("/todos") ? { todos: TODOS } : u.includes("/interactions") ? { interactions: INTERACTIONS } : u.includes("/sessions") ? { sessions: SESSIONS } : u.includes("/machines") ? { machines: MACHINES } : {};
      return Promise.resolve(new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } }));
    }),
  );
  await refresh();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  act(() => root.render(<TaskCard todoId="t1" conversation="c1" />));
  renders.n = 0;
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  reset();
  vi.unstubAllGlobals();
});

const session = (sid: string) => getState().sessions.m1.find((s) => s.session_id === sid)!;

describe("a task card", () => {
  it("draws its agent's question and where it runs", () => {
    expect(host.textContent).toContain("在等你批准：swift test");
    expect(host.textContent).toContain("MacBook · app");
  });

  it("does not render again for another agent, its card or another machine", () => {
    act(() => upsertSession({ ...session("s2"), title: "b again", last_text: "still going" }));
    act(() => endInteraction("ix2"));
    act(() => applyMachine({ id: "m2", name: "Studio 2", online: true }));
    expect(renders.n).toBe(0);
  });

  it("renders again for its own agent, its own card and its machine", () => {
    act(() => applyMachine({ id: "m1", name: "MacBook Pro", online: true }));
    expect(renders.n).toBe(1);
    expect(host.textContent).toContain("MacBook Pro · app");

    act(() => endInteraction("ix1"));
    expect(renders.n).toBe(2);
    expect(host.textContent).not.toContain("swift test");

    act(() => upsertSession({ ...session("s1"), state: "working", status: "working", activity: "Running swift build" }));
    expect(renders.n).toBe(3);
    expect(host.textContent).toContain("Running swift build");
  });
});
