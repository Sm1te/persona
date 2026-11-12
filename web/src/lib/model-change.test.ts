import { describe, expect, it } from "vitest";
import { bareModel, markModelChanges, modelIdentity, replyModel, replyModelName } from "./model-change";
import { chatLines, type ChatLine } from "./turns";
import type { Item } from "./types";

const OPUS = "claude-opus-5-5";
const SONNET = "claude-sonnet-4-5-20250929";

const user = (seq: number, text = "next", extra: Partial<Item> = {}): Item => ({ id: `u${seq}#0`, seq, role: "user", kind: "text", text, ...extra });
const said = (seq: number, model?: string, text = "an answer"): Item => ({ id: `a${seq}#0`, seq, role: "assistant", kind: "text", text, ...(model ? { model } : {}) });
const call = (seq: number, model?: string): Item => ({ id: `t${seq}#0`, seq, role: "assistant", kind: "tool_use", tool_use_id: `call${seq}`, tool_name: "Bash", tool_input: { command: "ls" }, ...(model ? { model } : {}) });
const result = (seq: number): Item => ({ id: `r${seq}#0`, seq, role: "user", kind: "tool_result", tool_use_id: `call${seq - 1}`, text: "ok" });

/** Each line as the test reads it: a segment's user text (or "-") and its mark, other lines by kind. */
function marks(lines: ChatLine[]): string[] {
  return lines.map((l) => (l.kind === "segment" ? `${l.seg.user?.text ?? "-"}${l.seg.modelChange ? ` [${l.seg.modelChange.from} → ${l.seg.modelChange.to}]` : ""}` : l.kind));
}

describe("replyModelName", () => {
  it("names Claude's models as Claude Code does and keeps other ids readable", () => {
    expect(replyModelName(OPUS)).toBe("Opus 5.5");
    expect(replyModelName(SONNET)).toBe("Sonnet 4.5");
    expect(replyModelName("claude-opus-5-5[1m]")).toBe("Opus 5.5 1M");
    expect(replyModelName("gpt-6.1-sol")).toBe("6.1 Sol"); // as the model lists name a raw GPT id
    expect(replyModelName("glm-5.1")).toBe("glm-5.1");
  });

  it("reads through a gateway's, Bedrock's and Vertex's way of writing an id", () => {
    expect(replyModelName("us.anthropic.claude-sonnet-4-5-20250929-v1:0")).toBe("Sonnet 4.5");
    expect(replyModelName("claude-sonnet-4-5@20250929")).toBe("Sonnet 4.5");
    expect(replyModelName("vendor/glm-5.1")).toBe("glm-5.1");
    expect(replyModelName("openrouter/anthropic/claude-opus-5-5")).toBe("Opus 5.5");
  });
});

describe("bareModel", () => {
  it.each([
    ["claude-sonnet-4-5", "claude-sonnet-4-5"],
    ["claude-sonnet-4-5-20250929", "claude-sonnet-4-5"],
    ["us.anthropic.claude-sonnet-4-5-20250929-v1:0", "claude-sonnet-4-5"],
    ["eu.anthropic.claude-sonnet-4-5-20250929-v1:0", "claude-sonnet-4-5"],
    ["global.anthropic.claude-sonnet-4-5-20250929-v1:0", "claude-sonnet-4-5"],
    ["anthropic.claude-sonnet-4-5-20250929-v2:0", "claude-sonnet-4-5"],
    ["claude-sonnet-4-5@20250929", "claude-sonnet-4-5"],
    ["publishers/anthropic/models/claude-sonnet-4-5@20250929", "claude-sonnet-4-5"],
    ["vendor/GLM-5.1", "GLM-5.1"],
    ["claude-opus-5-5-20260101[1m]", "claude-opus-5-5[1m]"],
    ["deepseek-v3", "deepseek-v3"], // a version in the name is the model's, not Bedrock's suffix
    ["gpt-6.1-sol", "gpt-6.1-sol"],
  ])("%s → %s", (id, bare) => expect(bareModel(id)).toBe(bare));
});

describe("modelIdentity", () => {
  it("gives every spelling of one model the same canonical id, and another model another", () => {
    const sonnet = ["claude-sonnet-4-5", "claude-sonnet-4-5-20250929", "us.anthropic.claude-sonnet-4-5-20250929-v1:0", "claude-sonnet-4-5@20250929", "CLAUDE-SONNET-4-5"];
    expect(new Set(sonnet.map((id) => modelIdentity(id).canonical))).toEqual(new Set(["claude-sonnet-4-5"]));
    expect(modelIdentity("claude-opus-5-5").canonical).not.toBe(modelIdentity("claude-sonnet-4-5").canonical);
    expect(modelIdentity("claude-opus-5-5[1m]").canonical).not.toBe(modelIdentity("claude-opus-5-5").canonical); // modelShort names it "1M"
  });

  it("works each id out once, and keeps no more than 256 of them", () => {
    const first = modelIdentity("memo-probe-0");
    expect(modelIdentity("memo-probe-0")).toBe(first);
    for (let i = 1; i <= 300; i++) modelIdentity(`memo-probe-${i}`);
    expect(modelIdentity("memo-probe-300")).toBe(modelIdentity("memo-probe-300"));
    expect(modelIdentity("memo-probe-0")).not.toBe(first); // the oldest went to make room
    expect(modelIdentity("memo-probe-0")).toEqual(first);
  });
});

describe("replyModel", () => {
  it("is the model of the exchange's latest assistant item that names one", () => {
    const [withAnswer, steps] = chatLines([user(1, "a"), call(2, OPUS), result(3), said(4, SONNET), user(5, "b"), call(6, OPUS), result(7), call(8)]);
    expect(withAnswer.kind === "segment" && replyModel(withAnswer.seg)).toBe(SONNET);
    expect(steps.kind === "segment" && replyModel(steps.seg)).toBe(OPUS);
  });

  it("counts work done after the last words: a fallback's tool calls make the exchange that model's", () => {
    const [line] = chatLines([user(1, "a"), said(2, OPUS, "Looking."), call(3, SONNET), result(4), call(5, SONNET), result(6)]);
    expect(line.kind === "segment" && line.seg.answer?.model).toBe(OPUS); // the words on screen are still Opus's
    expect(line.kind === "segment" && replyModel(line.seg)).toBe(SONNET);
  });

  it("orders blocks of one line by their index", () => {
    const blocks: Item[] = [user(1, "a"), { ...call(2, OPUS), id: "t2#0" }, { ...call(2, SONNET), id: "t2#1", tool_use_id: "call2b" }];
    const [line] = chatLines(blocks);
    expect(line.kind === "segment" && replyModel(line.seg)).toBe(SONNET);
  });
});

describe("model change divider placement", () => {
  it("marks the exchange whose reply came from another model than the previous visible reply", () => {
    const lines = chatLines([user(1, "first"), said(2, OPUS), user(3, "second"), said(4, OPUS), user(5, "third"), said(6, SONNET), user(7, "fourth"), said(8, SONNET)]);
    expect(marks(lines)).toEqual(["first", "second", `third [${OPUS} → ${SONNET}]`, "fourth"]);
  });

  it("puts nothing where a model is unknown, and compares across it with the last known one", () => {
    // older nodes send no model; Claude Code's own lines (an API error) name none
    const lines = chatLines([user(1, "first"), said(2, OPUS), user(3, "second"), said(4), user(5, "third"), said(6, SONNET)]);
    expect(marks(lines)).toEqual(["first", "second", `third [${OPUS} → ${SONNET}]`]);
    expect(marks(chatLines([user(1, "first"), said(2), user(3, "second"), said(4, SONNET)]))).toEqual(["first", "second"]);
  });

  it("never marks inside an exchange: one mark at most, before the reply, and no new lines", () => {
    // a refusal fallback moves the turn to another model halfway: the exchange is marked once, by its answer's model
    const items = [user(1, "first"), said(2, OPUS), user(3, "second"), call(4, OPUS), result(5), call(6, SONNET), result(7), said(8, SONNET)];
    const lines = chatLines(items);
    expect(marks(lines)).toEqual(["first", `second [${OPUS} → ${SONNET}]`]);
    expect(lines).toHaveLength(chatLines(items.map(({ model: _, ...it }) => it)).length);
  });

  it("treats a dated id and its alias as one model", () => {
    expect(marks(chatLines([user(1, "first"), said(2, OPUS), user(3, "second"), said(4, `${OPUS}-20260101`)]))).toEqual(["first", "second"]);
  });

  it("draws nothing between spellings of one model (alias, pinned, Bedrock, Vertex, a gateway's), and one at a real change", () => {
    const spellings = ["claude-sonnet-4-5", SONNET, "us.anthropic.claude-sonnet-4-5-20250929-v1:0", "claude-sonnet-4-5@20250929", "anthropic/claude-sonnet-4-5"];
    const items = spellings.flatMap((model, k) => [user(k * 2 + 1, `q${k}`), said(k * 2 + 2, model)]);
    expect(marks(chatLines(items))).toEqual(["q0", "q1", "q2", "q3", "q4"]);
    const changed = chatLines([...items, user(20, "q5"), said(21, "us.anthropic.claude-opus-5-5-v1:0")]);
    expect(marks(changed).at(-1)).toBe("q5 [anthropic/claude-sonnet-4-5 → us.anthropic.claude-opus-5-5-v1:0]");
    expect(marks(chatLines([user(1, "a"), said(2, "vendor/glm-5.1"), user(3, "b"), said(4, "glm-5.1"), user(5, "c"), said(6, "vendor/glm-5.2")])))
      .toEqual(["a", "b", "c [glm-5.1 → vendor/glm-5.2]"]);
    // the names keep the id's case, so only the canonical id says these are one model
    expect(marks(chatLines([user(1, "a"), said(2, "GLM-5.1"), user(3, "b"), said(4, "glm-5.1")]))).toEqual(["a", "b"]);
  });

  it("marks an exchange by the model of its latest work, not its earlier words", () => {
    const lines = chatLines([user(1, "first"), said(2, OPUS), user(3, "second"), said(4, OPUS, "Looking."), call(5, SONNET), result(6)]);
    expect(marks(lines)).toEqual(["first", `second [${OPUS} → ${SONNET}]`]);
  });

  it("skips folded rounds: they are no visible reply", () => {
    const supervise = { injected: { kind: "system", state: "supervise", line: "Review a task" } } as Partial<Item>;
    const lines = chatLines([user(1, "first"), said(2, OPUS), user(3, "check", supervise), said(4, "claude-haiku-4-5"), user(5, "second"), said(6, OPUS)]);
    expect(marks(lines)).toEqual(["first", "supervision", "second"]);
  });

  it("marks the live exchange as soon as the new model's first step is in", () => {
    const lines = chatLines([user(1, "first"), said(2, OPUS), user(3, "second"), call(4, SONNET)], { live: true });
    expect(marks(lines)).toEqual(["first", `second [${OPUS} → ${SONNET}]`]);
  });

  it("leaves the lines as they are when no model changes", () => {
    const lines = chatLines([user(1, "first"), said(2, OPUS), user(3, "second"), said(4, OPUS)]);
    const again = markModelChanges(lines);
    expect(again.every((l, i) => l === lines[i])).toBe(true);
  });
});
