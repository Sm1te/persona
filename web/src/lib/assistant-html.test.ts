import { describe, expect, it } from "vitest";
import { hasHtml, htmlDecision, WEB_HTML_RUNTIME_VERIFIED } from "./assistant-html";
import { htmlRequest, judge, mounted, newBridge, prompted, PROMPTS_MOST, REFUSED_QUIET_MS, unmounted, type HtmlInstance } from "./html-bridge";

const EXAMPLE = Object.values(import.meta.glob("../../../schema/extension/examples/assistant/html.json", { query: "?raw", import: "default", eager: true }) as Record<string, string>)[0];
const inst: HtmlInstance = { nonce: "n1", conversation: "c1", key: "view_1", componentKey: "guide", page: "", version: 3 };

describe("网页端的 html 组件", () => {
  it("runs on the web where the hub allows it and the component has a key: the runtime passed its security cases", () => {
    expect(WEB_HTML_RUNTIME_VERIFIED).toBe(true);
    expect(htmlDecision({ hubAllows: true, componentKey: "guide" })).toEqual({ run: true });
    expect(htmlDecision({ hubAllows: false, componentKey: "guide" })).toEqual({ run: false, reason: "disabled" });
    expect(htmlDecision({ hubAllows: true })).toEqual({ run: false, reason: "no_key" });
    // a runtime that hasn't passed never runs
    expect(htmlDecision({ hubAllows: true, componentKey: "guide", verified: false })).toEqual({ run: false, reason: "runtime_unverified" });
  });

  it("finds html on the root and on named pages", () => {
    expect(hasHtml(JSON.parse(EXAMPLE))).toBe(true);
    expect(hasHtml({ sections: [], pages: { d: { sections: [{ items: [{ type: "html", html: "" }] }] } } })).toBe(true);
    expect(hasHtml({ sections: [{ items: [{ type: "text", text: "<b>" }] }] })).toBe(false);
  });
});

describe("bridge 的消息", () => {
  it("only the instance on screen, at the version it started at", () => {
    let st = mounted(newBridge(), inst);
    expect(judge(st, { nonce: "n1", kind: "reply", text: "继续" }, 3, 0)).toEqual({ ok: true, ask: { kind: "reply", text: "继续" } });
    expect(judge(st, { nonce: "old", kind: "reply", text: "继续" }, 3, 0)).toEqual({ ok: false, reason: "stale" });
    expect(judge(st, { nonce: "n1", kind: "reply", text: "继续" }, 4, 0)).toEqual({ ok: false, reason: "stale" });
    st = mounted(st, { ...inst, nonce: "n2" });
    expect(judge(st, { nonce: "n1", kind: "reply", text: "继续" }, 3, 0)).toMatchObject({ reason: "stale" });
    st = unmounted(st, "n2");
    expect(judge(st, { nonce: "n2", kind: "reply", text: "继续" }, 3, 0)).toMatchObject({ reason: "stale" });
  });

  it("only reply and open, within limits", () => {
    const st = mounted(newBridge(), inst);
    expect(judge(st, { nonce: "n1", kind: "eval", text: "x" }, 3, 0)).toMatchObject({ reason: "shape" });
    expect(judge(st, { nonce: "n1", kind: "reply", text: "x".repeat(2001) }, 3, 0)).toMatchObject({ reason: "too_long" });
    expect(judge(st, { nonce: "n1", kind: "open", url: "javascript:alert(1)" }, 3, 0)).toMatchObject({ reason: "open" });
    expect(judge(st, { nonce: "n1", kind: "open", url: "https://example.com" }, 3, 0)).toMatchObject({ ok: true });
  });

  it("quiet after a refusal, and at most a few prompts (no confirm flood)", () => {
    let st = prompted(mounted(newBridge(), inst), true, 1000);
    const ask = { nonce: "n1", kind: "reply", text: "再来" };
    expect(judge(st, ask, 3, 1000 + REFUSED_QUIET_MS - 1)).toMatchObject({ reason: "quiet" });
    expect(judge(st, ask, 3, 1000 + REFUSED_QUIET_MS)).toMatchObject({ ok: true });
    for (let i = 1; i < PROMPTS_MOST; i++) st = prompted(st, false, 0);
    expect(judge(st, ask, 3, 1e9)).toMatchObject({ reason: "quiet" });
  });

  it("the request: html-actions shape, the instance's version and component key, confirmed", () => {
    expect(htmlRequest({ ...inst, page: "detail" }, { kind: "reply", text: "继续" }, "cid")).toEqual({
      kind: "reply", component_key: "guide", page: "detail", expected_version: 3, text: "继续", client_msg_id: "cid", confirmed: true,
    });
    expect(htmlRequest(inst, { kind: "open", url: "https://example.com" }, "cid")).toEqual({
      kind: "open", component_key: "guide", expected_version: 3, url: "https://example.com", client_msg_id: "cid", confirmed: true,
    });
  });
});

describe("和公共 schema 的 html-actions 示例一致（#2604）", () => {
  const ex = (name: string) =>
    JSON.parse(Object.entries(import.meta.glob("../../../schema/extension/examples/**/AssistantHTMLActionInput.*.json", { query: "?raw", import: "default", eager: true }) as Record<string, string>).find(([p]) => p.endsWith(`/${name}.json`))![1]);

  it("reply and open build the examples exactly; never unconfirmed", () => {
    const reply = ex("AssistantHTMLActionInput.reply");
    expect(htmlRequest({ ...inst, componentKey: reply.component_key, version: reply.expected_version }, { kind: "reply", text: reply.text }, reply.client_msg_id)).toEqual(reply);
    const open = ex("AssistantHTMLActionInput.open");
    expect(htmlRequest({ ...inst, componentKey: open.component_key, page: open.page, version: open.expected_version }, { kind: "open", url: open.url }, open.client_msg_id)).toEqual(open);
    expect(ex("AssistantHTMLActionInput.not-confirmed").confirmed).not.toBe(true);
  });
});
