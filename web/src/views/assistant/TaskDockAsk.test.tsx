// @vitest-environment jsdom
// The dock's waiting task pill says what it waits on when hovered or read out (#3463), in the task card's words
// (taskAsk); a pill of a task at work has no such label.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { refresh } from "../../lib/store";
import { TaskDock } from "./Tasks";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const now = new Date().toISOString();
const TODOS = [
  { id: "t-wait", title: "Waiting task", origin: "c1", created_at: now, updated_at: now, agents: [{ machine_id: "m1", session_id: "s-wait" }] },
  { id: "t-work", title: "Working task", origin: "c1", created_at: now, updated_at: now, agents: [{ machine_id: "m1", session_id: "s-work" }] },
];
const SESSIONS = [
  { machine_id: "m1", session_id: "s-wait", state: "needs_you", title: "a", updated_at: now },
  { machine_id: "m1", session_id: "s-work", state: "working", title: "b", updated_at: now },
];
const INTERACTIONS = [{ id: "ix1", machine_id: "m1", session_id: "s-wait", kind: "permission", tool_name: "Bash", tool_input: { command: "swift test" } }];

let host: HTMLDivElement;
let root: Root;

beforeEach(async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn((url: string) => {
      const u = String(url);
      const body = u.includes("/todos") ? { todos: TODOS } : u.includes("/interactions") ? { interactions: INTERACTIONS } : u.includes("/sessions") ? { sessions: SESSIONS } : {};
      return Promise.resolve(new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } }));
    }),
  );
  await refresh();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  act(() => root.render(<TaskDock conversation="c1" />));
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
});

const pill = (title: string) => [...host.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent?.includes(title))!;

describe("the dock's task pills", () => {
  it("says what a waiting task waits on", () => {
    const b = pill("Waiting task");
    expect(b.getAttribute("title")).toBe("Waiting task：在等你批准：swift test");
    expect(b.getAttribute("aria-label")).toBe("Waiting task：在等你批准：swift test");
  });

  it("adds nothing to a task at work", () => {
    const b = pill("Working task");
    expect(b.hasAttribute("title")).toBe(false);
    expect(b.hasAttribute("aria-label")).toBe(false);
  });
});
