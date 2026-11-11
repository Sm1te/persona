// A waiting task says what it waits on (#3301): its card's kind and what it asks, in the to-dos page's words, so the owner
// does not have to open the task to see it; without a card it says "Waiting for your answer" as before.
import { describe, expect, it } from "vitest";
import type { Interaction, Session } from "../../lib/types";
import type { Todo } from "../../lib/todos";
import { taskAsk, taskInteraction } from "./Tasks";

const ix = (o: Partial<Interaction>): Interaction => ({ id: "ix", machine_id: "m1", session_id: "s1", kind: "question", ...o });
const todo: Todo = {
  id: "t1",
  title: "Example",
  created_at: "2026-11-05T00:00:00Z",
  updated_at: "2026-11-05T00:00:00Z",
  agents: [
    { machine_id: "m1", session_id: "s1" },
    { machine_id: "m1", session_id: "s2" },
    { machine_id: "m1", session_id: "gone", released_at: "2026-11-05T00:00:00Z" },
  ],
} as Todo;

describe("taskAsk", () => {
  it("says the question", () => {
    expect(taskAsk(ix({ questions: [{ question: "用哪个测试框架？" } as never] }))).toBe("在问你：用哪个测试框架？");
  });
  it("says the command a permission is for", () => {
    expect(taskAsk(ix({ kind: "permission", tool_name: "Bash", tool_input: { command: "swift test" } }))).toBe("在等你批准：swift test");
  });
  it("says a plan waits, without its body", () => {
    expect(taskAsk(ix({ kind: "plan", plan: "# 计划\n1. 改代码" }))).toBe("计划等你审批");
  });
  it("drops Markdown and folds lines", () => {
    expect(taskAsk(ix({ questions: [{ question: "**先**做\nA 还是 B？" } as never] }))).toBe("在问你：先做 A 还是 B？");
  });
  it("has nothing to say without a card", () => {
    expect(taskAsk(undefined)).toBeUndefined();
  });
});

describe("taskInteraction", () => {
  const lead = { machine_id: "m1", session_id: "s2" } as Session;
  it("takes the lead agent's card first", () => {
    const a = ix({ id: "a", session_id: "s1" });
    const b = ix({ id: "b", session_id: "s2" });
    expect(taskInteraction(todo, [a, b], lead)?.id).toBe("b");
  });
  it("falls back to another active agent's card", () => {
    expect(taskInteraction(todo, [ix({ id: "a", session_id: "s1" })], lead)?.id).toBe("a");
  });
  it("ignores released agents and other sessions", () => {
    expect(taskInteraction(todo, [ix({ session_id: "gone" }), ix({ session_id: "other" })], lead)).toBeUndefined();
  });
});
