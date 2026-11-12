// @vitest-environment jsdom
// What an opened child card reads of the child's transcript (#5120): its last exchange folded, its first message from the
// head when that exchange is not its first, and a last tool folded into the work from the fold's last lines.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setToken } from "./api";
import { FOLD_TAIL_LINES, firstAsk, foldAfterLastCall, HEAD_LINES, lastToolName, latestWords, readChildDetail } from "./child-detail";
import type { Item } from "./types";

const it_ = (id: string, seq: number, role: Item["role"], kind: Item["kind"], extra: Partial<Item> = {}): Item => ({ id: `${id}#0`, seq, role, kind, ...extra });
const ask = it_("u", 3, "user", "text", { text: '<persona-context kind="context" title="Plan">the plan</persona-context>\nAdd the tests.' });
const call = it_("t", 4, "assistant", "tool_use", { tool_name: "Bash", tool_use_id: "c1", tool_input: { command: "npm test" } });
const result = it_("r", 5, "user", "tool_result", { tool_use_id: "c1", text: "ok" });
const words = it_("a", 6, "assistant", "text", { text: "All three pass." });
const fold = it_("fold:w", 7, "assistant", "fold", { fold: { items: 40, tools: 20, thoughts: 2, through_seq: 60 } });

describe("reading a child's items", () => {
  it("finds what it was asked, its latest words and its last call", () => {
    expect(firstAsk([it_("x", 1, "user", "tool_result", { tool_use_id: "z", text: "not a message" }), ask])).toBe("Add the tests.");
    expect(firstAsk([words])).toBe("");
    expect(latestWords([words, call, it_("e", 9, "assistant", "text", { text: "  " })])).toBe(words);
    expect(lastToolName([call, result, words])).toBe("Bash");
    expect(lastToolName([words])).toBeUndefined();
  });

  it("knows when the last call is folded into the work after the one the page shows", () => {
    expect(foldAfterLastCall([ask, call, result, fold])).toBe(fold);
    expect(foldAfterLastCall([ask, fold, { ...call, seq: 70 }])).toBeUndefined();
    expect(foldAfterLastCall([ask, words])).toBeUndefined();
  });
});

describe("readChildDetail", () => {
  let asked: string[];
  let answers: Record<string, unknown>;
  beforeEach(() => {
    setToken("tok");
    asked = [];
    answers = {};
    vi.stubGlobal("fetch", vi.fn(async (url: string) => {
      const u = new URL(url, "http://x");
      asked.push(`${u.pathname}?${u.searchParams}`);
      const body = answers[u.searchParams.has("turns") ? "page" : u.searchParams.get("from_seq") === "0" ? "head" : "tail"] ?? { items: [] };
      return new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } });
    }));
  });
  afterEach(() => vi.unstubAllGlobals());

  it("one read of its last exchange when that is all there is", async () => {
    answers.page = { items: [ask, call, result, words], has_more: false };
    const d = await readChildDetail("m", "kid", undefined);
    expect(d).toEqual({ prompt: "Add the tests.", result: words, lastTool: "Bash" });
    expect(asked).toEqual(["/api/v1/sessions/m/kid/items?turns=1&fold_work=1&lazy_images=1"]);
  });

  it("an earlier exchange: its first message from the head; a folded last call: from the fold's last lines", async () => {
    answers.page = { items: [{ ...ask, text: "And now the docs." }, fold, words], has_more: true };
    answers.head = { items: [it_("s", 0, "system", "notice", { text: "session" }), it_("h", 2, "user", "text", { text: "Write the regression tests." })] };
    answers.tail = { items: [it_("g", 58, "assistant", "tool_use", { tool_name: "mcp__github__create_issue", tool_use_id: "c9" }), it_("gr", 59, "user", "tool_result", { tool_use_id: "c9" })] };
    const d = await readChildDetail("m", "kid", undefined);
    expect(d.prompt).toBe("Write the regression tests.");
    expect(d.lastTool).toBe("mcp__github__create_issue");
    expect(d.result).toEqual(words);
    expect(asked).toContain(`/api/v1/sessions/m/kid/items?from_seq=0&through_seq=${HEAD_LINES}&lazy_images=1`);
    expect(asked).toContain(`/api/v1/sessions/m/kid/items?from_seq=${60 - FOLD_TAIL_LINES}&through_seq=60&lazy_images=1`);
  });

  it("a head or fold read that fails keeps what the page said", async () => {
    answers.page = { items: [ask, call, result, fold], has_more: true };
    vi.stubGlobal("fetch", vi.fn(async (url: string) => {
      const u = new URL(url, "http://x");
      if (!u.searchParams.has("turns")) return new Response(JSON.stringify({ error: "down" }), { status: 502, headers: { "Content-Type": "application/json" } });
      return new Response(JSON.stringify(answers.page), { headers: { "Content-Type": "application/json" } });
    }));
    const d = await readChildDetail("m", "kid", undefined);
    expect(d).toEqual({ prompt: "Add the tests.", result: undefined, lastTool: "Bash" });
  });
});
