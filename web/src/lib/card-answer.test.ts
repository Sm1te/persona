// answerCard (#4464): the one answer for a card, from its own buttons and from a notification's Allow / Deny. The approve
// check, what it sends, and how each hub answer comes back.
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "./api";
import type { Interaction, Session } from "./types";

let session: Session | undefined;
const apiOp = vi.fn();
const ended: unknown[][] = [];
vi.mock("./ops", () => ({ apiOp: (...a: unknown[]) => apiOp(...a) }));
vi.mock("./store", () => ({ getSession: () => session, endInteraction: (...a: unknown[]) => ended.push(a) }));

const { answerCard } = await import("./card-answer");
const { allowBody, denyBody } = await import("./quick-answers");

const ix: Interaction = { id: "p1", machine_id: "m", session_id: "a", kind: "permission", tool_name: "Bash" };

beforeEach(() => {
  session = { machine_id: "m", session_id: "a" } as Session;
  apiOp.mockReset();
  ended.length = 0;
});

describe("answerCard", () => {
  it("answers a desktop async question without granting permission approvals", async () => {
    session = { machine_id: "m", session_id: "a", source_kind: "codex_history", capabilities: { read: true, send: true, approve: false, answer_questions: true } } as Session;
    const question: Interaction = { ...ix, kind: "question", tool_name: "request_user_input_async", questions: [{ question: "Which color?", options: [{ label: "Blue" }] }] };
    const body = { answers: { "Which color?": "Blue" } };
    apiOp.mockResolvedValue({});
    expect(await answerCard(question, body)).toEqual({ ok: true, already: false });
    expect(apiOp).toHaveBeenCalledWith("/interactions/p1/resolve", { method: "POST", json: body });
    apiOp.mockClear();
    for (const card of [ix, { ...question, tool_name: "request_user_input" }, { ...question, kind: "permission" as const }, { ...question, kind: "plan" as const }]) {
      expect(await answerCard(card, body)).toEqual({ ok: false, error: "此会话不允许审批" });
    }
    expect(apiOp).not.toHaveBeenCalled();
  });

  it("会话不让批：不发，说为什么", async () => {
    session = { machine_id: "m", session_id: "a", source_kind: "codex_history", capabilities: { approve: false } } as Session;
    expect(await answerCard(ix, allowBody())).toEqual({ ok: false, error: "此会话不允许审批" });
    session = undefined;
    expect(await answerCard(ix, allowBody())).toEqual({ ok: false, error: "此会话不允许审批" });
    expect(apiOp).not.toHaveBeenCalled();
  });

  it("答上：发到卡片的 resolve，卡片记下答了什么", async () => {
    apiOp.mockResolvedValue({});
    expect(await answerCard(ix, denyBody())).toEqual({ ok: true, already: false });
    expect(apiOp).toHaveBeenCalledWith("/interactions/p1/resolve", { method: "POST", json: { allow: false, message: "用户拒绝了" } });
    expect(ended).toEqual([["p1", { ix, resolution: { allow: false, message: "用户拒绝了" }, already: false, handedBack: false }]]);
  });

  it("leaves a handed-back receipt for a fresh hand back, as for an answer", async () => {
    apiOp.mockResolvedValue({ ok: true });
    expect(await answerCard(ix, { dismiss: true })).toEqual({ ok: true, already: false });
    expect(apiOp).toHaveBeenCalledWith("/interactions/p1/resolve", { method: "POST", json: { dismiss: true } });
    expect(ended).toEqual([["p1", { ix, resolution: { dismiss: true }, already: false, handedBack: true }]]);
  });

  it("别处已经答过：按 hub 说的答案记下，already", async () => {
    apiOp.mockResolvedValue({ already: true, resolution: { allow: true } });
    expect(await answerCard(ix, denyBody())).toEqual({ ok: true, already: true });
    expect(ended).toEqual([["p1", { ix, resolution: { allow: true }, already: true, handedBack: false }]]);
  });

  it("410：卡已经结束，收起，说结束的原因", async () => {
    apiOp.mockRejectedValue(new ApiError(410, "gone", { status: "expired" }));
    const r = await answerCard(ix, allowBody());
    expect(r.ok).toBe(false);
    expect(ended).toEqual([["p1"]]);
  });

  it("prompt_changed 和别的错误：不收卡，说原因", async () => {
    apiOp.mockRejectedValue(new ApiError(409, "changed", { code: "prompt_changed" }));
    expect(await answerCard(ix, allowBody())).toEqual({ ok: false, error: "电脑上的对话框变了，已按新的屏幕重新生成" });
    apiOp.mockRejectedValue(new ApiError(500, "boom", {}));
    expect(await answerCard(ix, allowBody())).toEqual({ ok: false, error: expect.stringMatching(/^没送到：.+（HTTP 500: boom）$/) });
    expect(ended).toEqual([]);
  });
});

it("keeps one answer per card on its way: the same one shares it, another is refused unsent, both free once it ends", async () => {
  let finish!: (e: Error) => void;
  apiOp.mockReturnValueOnce(new Promise((_, no) => { finish = no; }));
  const first = answerCard(ix, allowBody());
  expect(answerCard(ix, allowBody())).toBe(first);
  expect(await answerCard(ix, denyBody())).toEqual({ ok: false, error: "这张卡已有一个答案在发送中" });
  expect(apiOp).toHaveBeenCalledOnce();
  finish(new TypeError("Failed to fetch"));
  expect(await first).toEqual({ ok: false, error: expect.stringMatching(/^没送到：/) });
  apiOp.mockResolvedValueOnce({});
  expect(await answerCard(ix, denyBody())).toEqual({ ok: true, already: false });
  expect(apiOp).toHaveBeenCalledTimes(2);
});

it("sends secret text unchanged and stores only a redacted receipt", async () => {
  const card: Interaction = { ...ix, kind: "screen", secret: true };
  const body = Object.freeze({ text: "synthetic-secret" });
  apiOp.mockResolvedValue({});
  expect(await answerCard(card, body)).toEqual({ ok: true, already: false });
  expect(apiOp).toHaveBeenCalledWith("/interactions/p1/resolve", { method: "POST", json: body });
  expect(ended[0]?.[1]).toMatchObject({ resolution: { text: "<redacted>" } });
  expect(body.text).toBe("synthetic-secret");
});

it("redacts field and elicitation secrets by schema and name without mutating the payload", async () => {
  const card: Interaction = {
    ...ix, kind: "elicitation", fields: [{ key: "hidden", label: "Hidden", secret: true }],
    elicitation: { mode: "form", requested_schema: { type: "object", properties: {
      hidden: { type: "string", format: "password" }, write: { type: "string", writeOnly: true }, visible: { type: "string" },
    } } },
  };
  const values = Object.freeze({ hidden: "synthetic", api_key: "synthetic", visible: "keep" });
  const content = Object.freeze({ hidden: "synthetic", write: "synthetic", TOKEN: "synthetic", visible: "keep", count: 3, enabled: false });
  const body = Object.freeze({ action: "accept", values, content });
  apiOp.mockResolvedValue({});
  expect(await answerCard(card, body)).toEqual({ ok: true, already: false });
  expect(apiOp).toHaveBeenCalledWith("/interactions/p1/resolve", { method: "POST", json: body });
  expect(ended[0]?.[1]).toMatchObject({ resolution: {
    action: "accept", values: { hidden: "<redacted>", api_key: "<redacted>", visible: "<redacted>" },
    content: { hidden: "<redacted>", write: "<redacted>", TOKEN: "<redacted>", visible: "keep", count: 3, enabled: false },
  } });
  expect(body.content.hidden).toBe("synthetic");
  expect(body.values.hidden).toBe("synthetic");
});

it("preserves non-secret text and does not record failed secret answers", async () => {
  apiOp.mockResolvedValue({});
  await answerCard({ ...ix, kind: "screen" }, { text: "ordinary answer" });
  expect(ended[0]?.[1]).toMatchObject({ resolution: { text: "ordinary answer" } });
  ended.length = 0;
  apiOp.mockRejectedValue(new Error("offline"));
  expect((await answerCard({ ...ix, kind: "screen", secret: true }, { text: "synthetic" })).ok).toBe(false);
  expect(ended).toEqual([]);
});

it("answers an explicitly sessionless script card without a session lookup result", async () => {
  session = undefined;
  apiOp.mockResolvedValue({});
  const script = { ...ix, id: "script-request", session_id: "", tool_name: "personad_ask", agent: "script" };
  expect(await answerCard(script, allowBody())).toEqual({ ok: true, already: false });
  expect(apiOp).toHaveBeenCalledWith("/interactions/script-request/resolve", { method: "POST", json: { allow: true } });
});
