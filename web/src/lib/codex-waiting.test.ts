// A Codex opened with no first message (docs/integrations/codex.md "Starting without a first message"): its page
// follows its pane to the thread the first message began.
import { describe, expect, it } from "vitest";
import { codexThreadOf, waitsForFirstMessage } from "./codex-waiting";
import { rowLine } from "./row-line";
import type { Session } from "./types";

const s = (o: Partial<Session>): Session => ({ machine_id: "mac", session_id: "s1", title: "proj", cwd: "/code/proj", status: "idle", agent: "codex", ...o });
const waiting = s({ session_id: "c0de0000-1111-4222-8333-444455556666", pane_id: "w1:p2", awaiting_first_message: true });

describe("codexThreadOf", () => {
  it("is the Codex the same pane lists once the row went", () => {
    const thread = s({ session_id: "019a0000-0000-7000-8000-000000000001", pane_id: "w1:p2", status: "working" });
    expect(codexThreadOf([thread], waiting)).toBe(thread);
  });

  it("is nothing while the row waits, for another pane, or an agent that stopped", () => {
    expect(codexThreadOf([waiting], waiting)).toBeUndefined();
    expect(codexThreadOf([s({ session_id: "t2", pane_id: "w1:p3" })], waiting)).toBeUndefined();
    expect(codexThreadOf([s({ session_id: "t3", pane_id: "w1:p2", status: "offline" })], waiting)).toBeUndefined();
    expect(codexThreadOf([s({ session_id: "t4", pane_id: "w1:p2", agent: "claude" })], waiting)).toBeUndefined();
    expect(codexThreadOf([s({ session_id: "t5", pane_id: "w1:p2" })], { session_id: waiting.session_id })).toBeUndefined();
  });
});

describe("a waiting Codex's row", () => {
  it("says it waits for its first message", () => {
    expect(waitsForFirstMessage(waiting)).toBe(true);
    expect(waitsForFirstMessage(s({}))).toBe(false);
    expect(rowLine(waiting)?.text).toBe("等第一条消息");
  });
});
