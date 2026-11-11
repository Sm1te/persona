import { describe, expect, it } from "vitest";
import { canShowTerminal, composerSlot, otherView, readView, rememberView, resolveView, terminalTarget, viewOnlyRow } from "./session-view";
import type { Session } from "./types";

function memory() {
  const m = new Map<string, string>();
  return { getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => void m.set(k, v) };
}

function agent(o: Partial<Session> = {}): Session {
  return { machine_id: "m1", session_id: "a1", title: "t", cwd: "/code", status: "idle", state: "idle", ...o } as Session;
}

describe("which view an agent's page draws", () => {
  it("takes the address's, then the one remembered, then the conversation", () => {
    expect(resolveView("term", "chat", true)).toBe("term");
    expect(resolveView(undefined, "term", true)).toBe("term");
    expect(resolveView(undefined, undefined, true)).toBe("chat");
  });
  it("is the conversation when there is no terminal to show, whatever was asked", () => {
    expect(resolveView("term", "term", false)).toBe("chat");
  });
  it("flips", () => {
    expect(otherView("term")).toBe("chat");
    expect(otherView("chat")).toBe("term");
  });
});

describe("the view remembered per agent", () => {
  it("is kept for each agent apart", () => {
    const s = memory();
    rememberView("m1", "a1", "term", s);
    rememberView("m1", "a2", "chat", s);
    expect(readView("m1", "a1", s)).toBe("term");
    expect(readView("m1", "a2", s)).toBe("chat");
    expect(readView("m2", "a1", s)).toBeUndefined();
    rememberView("m1", "a1", "chat", s);
    expect(readView("m1", "a1", s)).toBe("chat");
  });
  it("ignores a store that holds garbage", () => {
    const s = memory();
    s.setItem("persona.sessionView", "{not json");
    expect(readView("m1", "a1", s)).toBeUndefined();
    s.setItem("persona.sessionView", JSON.stringify({ "m1/a1": "shell", "m1/a2": "term" }));
    expect(readView("m1", "a1", s)).toBeUndefined();
    expect(readView("m1", "a2", s)).toBe("term");
  });
  it("lets the oldest go past 300 agents", () => {
    const s = memory();
    for (let i = 0; i < 305; i++) rememberView("m", `a${i}`, "term", s);
    expect(readView("m", "a0", s)).toBeUndefined();
    expect(readView("m", "a304", s)).toBe("term");
  });
  it("survives a store that refuses writes", () => {
    expect(() => rememberView("m", "a", "term", { getItem: () => null, setItem: () => { throw new Error("full"); } })).not.toThrow();
  });
});

describe("where a terminal from a list of panes opens", () => {
  it("an agent's pane: its own page, in the terminal view", () => {
    expect(terminalTarget("m1", "a1", [agent()])).toEqual({ name: "session", mid: "m1", sid: "a1", view: "term" });
    expect(terminalTarget("m1", "a1", [agent({ space_id: "w1", slot_id: "w1:t1" })])).toEqual({ name: "tab", mid: "m1", space: "w1", slot: "w1:t1", sid: "a1", view: "term" });
  });
  it("a plain shell, or an agent it does not know: the 终端 page", () => {
    expect(terminalTarget("m1", "pane:w1:p2", [agent()])).toEqual({ name: "terminal", mid: "m1", id: "pane:w1:p2" });
    expect(terminalTarget("m1", "zzz", [agent()])).toEqual({ name: "terminal", mid: "m1", id: "zzz" });
  });
  it("an agent with no terminal to open stays on the 终端 page", () => {
    expect(terminalTarget("m1", "a1", [agent({ state: "offline" })])).toEqual({ name: "terminal", mid: "m1", id: "a1" });
  });
});

describe("whether a terminal can be shown", () => {
  it("needs a live pane its source lets a terminal open for", () => {
    expect(canShowTerminal(agent())).toBe(true);
    expect(canShowTerminal(agent({ state: "offline" }))).toBe(false);
    expect(canShowTerminal(agent({ source_kind: "codex_history", capabilities: {} as never }))).toBe(false);
    expect(canShowTerminal(undefined)).toBe(false);
  });
});

describe("a stopped Agent's page (#4754)", () => {
  const stopped = agent({ status: "offline", state: "offline", capabilities: { read: true, terminal: true } });

  it("shows its terminal: the shell it left, or Resume", () => {
    expect(canShowTerminal(stopped)).toBe(true);
    expect(canShowTerminal({ ...stopped, status: "interrupted" })).toBe(true);
    // an older node's offline row stays as it was: no terminal to switch to
    expect(canShowTerminal(agent({ status: "offline", state: "offline" }))).toBe(false);
  });

  it("puts the read-only line with Terminal and Resume where the composer goes", () => {
    expect(composerSlot(stopped)).toBe("stopped");
    expect(composerSlot(agent())).toBe("composer");
    expect(composerSlot(agent({ status: "offline", state: "offline" }))).toBe("composer"); // older node: a message resumes it
    expect(composerSlot(undefined)).toBe("composer");
  });

  it("keeps the composer for a side panel's transcript, the assistant's chats and an offline computer", () => {
    expect(composerSlot(stopped, { readOnly: true })).toBe("composer");
    expect(composerSlot(stopped, { assistant: true })).toBe("composer");
    expect(composerSlot(stopped, { offline: true })).toBe("composer");
  });

  it("calls a row view-only when its capabilities refuse sending, a native one included, never an older node's", () => {
    expect(viewOnlyRow(agent({ capabilities: { read: true, terminal: true } }))).toBe(true); // a tab on Claude's agents list
    expect(viewOnlyRow(agent({ source_kind: "codex_history", capabilities: { read: true } }))).toBe(true);
    expect(viewOnlyRow(agent({ status: "offline", state: "offline" }))).toBe(false);
    expect(viewOnlyRow(agent({ capabilities: { read: true, send: true } }))).toBe(false);
    expect(viewOnlyRow(undefined)).toBe(false);
  });
});
