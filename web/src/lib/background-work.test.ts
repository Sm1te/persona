import { describe, expect, it } from "vitest";
import { backgroundWork, taskBackgroundWork, waitingOnSubagents } from "./background-work";
import type { Session } from "./types";
import type { Todo } from "./todos";

const session: Session = { machine_id: "fixture-device", session_id: "worker", title: "Fixture", cwd: "/demo", state: "working", status: "working" };
const flow = { task_id: "wf", status: "running", running: true, phase: "Review", agents_done: 5, agents_started: 6 };

describe("background work projections", () => {
  it("summary zeros override stale detail arrays instead of resurrecting completed work", () => {
    expect(backgroundWork({ ...session, waiting: false, background_tasks_count: 0, background_count: 7,
      workflows: [flow], subagents: [{ id: "sub", session_id: "worker@sub", running: true }], background: [{ id: "shell", kind: "shell" }] })).toBeUndefined();
  });

  it("old raw summary counts preserve active work without guessing an independent job count", () => {
    expect(backgroundWork({ ...session, waiting: true, background_count: 7 })?.tasks).toBe(0);
    expect(backgroundWork({ ...session, waiting: true, background_count: 7 })).toBeDefined();
  });

  it("legacy full details classify workflow and subagent entries without inventing shell jobs", () => {
    expect(backgroundWork({ ...session, background: [{ id: "wf", kind: "workflow" }, { id: "sub", kind: "agent" }, { id: "mon", kind: "monitor" }] }))
      .toEqual({ tasks: 1, subagents: 1, workflows: 1, workflow: undefined });
  });

  it("deduplicates identities before an answered-state lookup that returns new objects", () => {
    const binding = { machine_id: session.machine_id, session_id: session.session_id, linked_at: "2026-11-10T08:00:00Z" };
    const todo: Todo = { id: "task", title: "Fixture", created_at: "2026-11-10T08:00:00Z", updated_at: "2026-11-10T08:00:00Z", agents: [binding, binding, { ...binding, session_id: "released", released_at: "2026-11-10T08:00:00Z" }] };
    const lookup = () => ({ ...session, waiting: true, background_tasks_count: 2 });
    expect(taskBackgroundWork(todo, lookup)?.tasks).toBe(2);
    expect(taskBackgroundWork({ ...todo, done_at: "2026-11-10T08:00:00Z" }, lookup)).toBeUndefined();
  });
});

describe("waitingOnSubagents (#5030)", () => {
  const sub = { id: "a", session_id: "worker@a", running: true, background: true };
  it("the turn is over and a background subagent still runs", () => {
    expect(waitingOnSubagents({ ...session, background_working: true, subagents: [sub] })).toBe(true);
    expect(waitingOnSubagents({ ...session, background_working: true }, [sub])).toBe(true);
  });
  it("not while the turn runs, not for a foreground or finished one, not when a workflow says it first", () => {
    expect(waitingOnSubagents({ ...session, subagents: [sub] })).toBe(false);
    expect(waitingOnSubagents({ ...session, background_working: true, subagents: [{ ...sub, background: false }] })).toBe(false);
    expect(waitingOnSubagents({ ...session, background_working: true, subagents: [{ ...sub, running: false }] })).toBe(false);
    expect(waitingOnSubagents({ ...session, background_working: true, subagents: [sub], workflows: [flow] })).toBe(false);
    expect(waitingOnSubagents(undefined)).toBe(false);
  });
});
