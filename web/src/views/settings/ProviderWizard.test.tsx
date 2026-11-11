// @vitest-environment jsdom
// Settings › Model providers › Add provider starts from the hub's presets (docs/assistant/onboarding.md 4.3 "Three paths" B): the built-in ones listed
// first, the gateway typed by hand last; a preset takes only the key, tests it by itself with the preset, saves it with
// assign and says what was assigned and what was kept.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
import { forgetProviderNames, loadProviderNames } from "../../lib/provider-names";
import type { ProviderView } from "../../lib/providers";
import { resetReadiness, type Readiness } from "../../lib/readiness";
import { getState } from "../../lib/store";
import { Toasts } from "../../ui/atoms";
import { ProviderWizard } from "./ProviderWizard";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const presets = [
  { id: "builtin/glm", plugin: "builtin", name: "Zhipu GLM", display_name: "智谱 GLM", description: "国内直接开通", kind: "launcher", base_url: "https://open.example.cn/api/anthropic", auth: "bearer", model: "glm-x", signup_url: "https://open.example.cn/keys" },
  { id: "example-gateway/team", plugin: "example-gateway", name: "示例网关", kind: "launcher", base_url: "https://gateway.example.com", model: "example-pro" },
  { id: "example-gateway/api", plugin: "example-gateway", name: "示例 Responses", kind: "api", api: "responses", base_url: "https://api.example.com", model: "example-r" },
];
let calls: { method: string; path: string; body?: Record<string, unknown> }[] = [];
let testOK = true;
// set: what the hub's key test answers instead (a region block, #4526)
let testAnswer: Record<string, unknown> | null = null;
let discovered: Record<string, unknown>[] = [];
let readiness: Readiness | null = null;
// set: readiness answers only once it resolves (a node self-check can take seconds)
let readinessGate: Promise<void> | null = null;
const readinessGets = () => calls.filter((c) => c.path.endsWith("/assistant/readiness")).length;
const step = (id: string, state: string, code?: string) => ({ id, state, ...(code ? { code } : {}) });
const withSteps = (mode: string, steps: Readiness["steps"]): Readiness => ({
  version: 1, mode, ready: mode === "full", next: null, machine: { id: "mbp", name: "小林的 MacBook" }, launcher: "claude", steps, dismissed: false, checked_at: "2026-11-06T09:00:00Z",
});

const json = (v: unknown, status = 200) => new Response(JSON.stringify(v), { status, headers: { "Content-Type": "application/json" } });
function hub(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
  const path = String(input).split("?")[0]!;
  const method = init?.method ?? "GET";
  const body = init?.body ? JSON.parse(String(init.body)) : undefined;
  calls.push({ method, path, body });
  if (path.endsWith("/assistant/readiness")) return (readinessGate ?? Promise.resolve()).then(() => (readiness ? json(readiness) : json({ error: "not found" }, 404)));
  if (path.endsWith("/launchers/presets")) return Promise.resolve(json({ presets }));
  if (path.endsWith("/launchers/discover")) return Promise.resolve(json({ models: discovered }));
  if (path.endsWith("/launchers/test") && testAnswer) return Promise.resolve(json(testAnswer));
  if (path.endsWith("/launchers/test")) return Promise.resolve(json(testOK ? { ok: true, ms: 321, model: "glm-x" } : { ok: false, ms: 80, model: "glm-x", error: "Key 不对（401）" }));
  // a gateway typed by hand: named as the hub would (#5029)
  if (path.endsWith("/launchers") && method === "POST" && !body?.preset)
    return Promise.resolve(json({ launcher: { name: body?.name || "relay-example-com", display_name: body?.display_name || "relay.example.com" } }));
  if (path.endsWith("/launchers") && method === "POST")
    return Promise.resolve(json({ launcher: { name: "glm", display_name: "智谱 GLM", has_key: true }, assigned: ["assistant"], kept: [{ purpose: "agent.new", reason: "customized" }] }));
  return Promise.resolve(json({}));
}

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 6; i++) await act(async () => { await Promise.resolve(); });
};
const text = () => document.body.textContent ?? "";
const button = (label: string) => [...document.body.querySelectorAll("button")].find((b) => b.textContent?.includes(label));
function type(input: HTMLInputElement, value: string) {
  const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!;
  act(() => {
    set.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
}
const keyInput = () => document.body.querySelector<HTMLInputElement>('input[type="password"]')!;
// The key auto-test debounce (AUTO_TEST_MS in ProviderWizard.tsx); timers are fake, so this costs no real time.
const AUTO_TEST_MS = 700;
const advance = async (ms: number) => {
  await act(async () => { await vi.advanceTimersByTimeAsync(ms); });
};
const keyTests = () => calls.filter((c) => c.path.endsWith("/launchers/test"));
const waitAutoTest = async () => {
  await advance(AUTO_TEST_MS);
  await flush();
};

beforeEach(() => {
  // only timers are faked: promises, fetch and React's scheduling keep running for real
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
  Object.assign(getState(), { features: ["builtin_presets"] });
  calls = [];
  testOK = true;
  testAnswer = null;
  discovered = [];
  readiness = null;
  readinessGate = null;
  resetReadiness();
  vi.stubGlobal("fetch", vi.fn(hub));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

async function open() {
  const saved = vi.fn();
  act(() => root.render(<ProviderWizard existing="new" onClose={() => {}} onSaved={saved} />));
  await flush();
  return saved;
}

describe("ProviderWizard from presets", () => {
  it("lists the hub's presets with the custom gateway last and no hard-coded Anthropic", async () => {
    await open();
    const rows = [...document.body.querySelectorAll("ul li")].map((li) => li.textContent ?? "");
    expect(rows).toHaveLength(4);
    expect(rows[0]).toContain("智谱 GLM");
    expect(rows[1]).toContain("示例网关");
    expect(rows[2]).toContain("示例 Responses");
    expect(rows[3]).toContain("自定义兼容网关");
    expect(text()).not.toContain("Anthropic 官方");
  });

  it("takes only the key, tests it with the preset by itself, and saves it with assign", async () => {
    const saved = await open();
    act(() => button("智谱 GLM")!.click());
    expect(text()).not.toContain("鉴权方式");
    const link = document.body.querySelector<HTMLAnchorElement>('a[href="https://open.example.cn/keys"]');
    expect(link?.target).toBe("_blank");
    expect(link?.rel).toContain("noopener");

    type(keyInput(), "sk-test-123456");
    await waitAutoTest();
    const tests = keyTests();
    expect(tests).toHaveLength(1);
    expect(tests[0]!.body).toEqual({ preset: "builtin/glm", api_key: "sk-test-123456", auth: "bearer" });
    expect(text()).toContain("Key 能用");

    await act(async () => button("保存")!.click());
    await flush();
    const post = calls.find((c) => c.method === "POST" && c.path.endsWith("/launchers"));
    expect(post?.body).toEqual({ api_key: "sk-test-123456", preset: "builtin/glm", assign: ["assistant", "agent.new"] });
    expect(saved).toHaveBeenCalled();
    expect(text()).toContain("智谱 GLM 接好了");
    expect(text()).toContain("现在用它：主对话");
    expect(text()).toContain("新开的 Agent：没有改");
  });

  it("tests the key only once it has stopped changing for the whole debounce", async () => {
    await open();
    act(() => button("智谱 GLM")!.click());
    type(keyInput(), "sk-test-1234");
    await advance(AUTO_TEST_MS - 1);
    expect(keyTests()).toHaveLength(0);
    // another keystroke starts the wait over
    type(keyInput(), "sk-test-123456");
    await advance(AUTO_TEST_MS - 1);
    expect(keyTests()).toHaveLength(0);
    // the last millisecond of the wait sends it
    await advance(1);
    await flush();
    expect(keyTests()).toHaveLength(1);
    expect(keyTests()[0]!.body).toMatchObject({ api_key: "sk-test-123456" });
    expect(text()).toContain("Key 能用");
  });

  it("says why a key failed and leaves assign out when turned off", async () => {
    testOK = false;
    await open();
    act(() => button("智谱 GLM")!.click());
    type(keyInput(), "sk-wrong-123456");
    await waitAutoTest();
    expect(text()).toContain("Key 不对（401）");
    expect(button("重新测试")).toBeDefined();

    act(() => document.body.querySelector<HTMLButtonElement>('button[role="switch"]')!.click());
    await act(async () => button("保存")!.click());
    await flush();
    const post = calls.find((c) => c.method === "POST" && c.path.endsWith("/launchers"));
    expect(post?.body).not.toHaveProperty("assign");
    expect(text()).toContain("到「模型配置」里选");
  });

  // #4526: the region, not the key, is the problem (#4482): no retesting, a way back to the list, and the provider is
  // not put on the main conversation and new agents unless asked for.
  it("offers another provider instead of retesting when the region is blocked, and does not assign by default", async () => {
    testAnswer = { ok: false, ms: 120, model: "glm-x", code: "region_blocked", error: "Anthropic 不对这个地区提供服务：换 Key 也没用，换一个在这里能直接用的供应商" };
    await open();
    act(() => button("智谱 GLM")!.click());
    type(keyInput(), "sk-test-123456");
    await waitAutoTest();
    expect(text()).toContain("换 Key 也没用");
    expect(button("重新测试")).toBeUndefined();
    expect(button("换一家")).toBeDefined();
    const toggle = document.body.querySelector<HTMLButtonElement>('button[role="switch"]')!;
    expect(toggle.getAttribute("aria-checked")).toBe("false");

    await act(async () => button("保存")!.click());
    await flush();
    const post = calls.find((c) => c.method === "POST" && c.path.endsWith("/launchers"));
    expect(post?.body).not.toHaveProperty("assign");
  });

  it("still assigns a region-blocked provider when the switch is turned on", async () => {
    testAnswer = { ok: false, ms: 120, code: "region_blocked", error: "Anthropic 不对这个地区提供服务" };
    await open();
    act(() => button("智谱 GLM")!.click());
    type(keyInput(), "sk-test-123456");
    await waitAutoTest();
    act(() => document.body.querySelector<HTMLButtonElement>('button[role="switch"]')!.click());
    await act(async () => button("保存")!.click());
    await flush();
    const post = calls.find((c) => c.method === "POST" && c.path.endsWith("/launchers"));
    expect(post?.body).toMatchObject({ assign: ["assistant", "agent.new"] });
  });

  it("goes back to the providers from a blocked region's 换一家", async () => {
    testAnswer = { ok: false, ms: 120, code: "region_blocked", error: "Anthropic 不对这个地区提供服务" };
    await open();
    act(() => button("智谱 GLM")!.click());
    type(keyInput(), "sk-test-123456");
    await waitAutoTest();
    act(() => button("换一家")!.click());
    await flush();
    const rows = [...document.body.querySelectorAll("ul li")].map((li) => li.textContent ?? "");
    expect(rows).toHaveLength(4);
    expect(keyInput()).toBeNull();
  });

  it("does not carry a provider's key to the next one picked after 换一家", async () => {
    testAnswer = { ok: false, ms: 120, code: "region_blocked", error: "Anthropic 不对这个地区提供服务" };
    await open();
    act(() => button("智谱 GLM")!.click());
    type(keyInput(), "sk-test-123456");
    await waitAutoTest();
    act(() => button("换一家")!.click());
    await flush();
    act(() => button("示例网关")!.click());
    expect(keyInput()!.value).toBe("");
    await waitAutoTest();
    const tests = calls.filter((c) => c.method === "POST" && c.path.endsWith("/launchers/test"));
    expect(tests.map((c) => (c.body as { preset?: string }).preset)).toEqual(["builtin/glm"]);
  });

  it("asks an older hub's preset for the name and sends no assign", async () => {
    Object.assign(getState(), { features: ["channels"] });
    await open();
    act(() => button("示例网关")!.click());
    expect(document.body.querySelector('button[role="switch"]')).toBeNull();
    type(keyInput(), "sk-test-123456");
    await waitAutoTest();
    expect(button("保存")!.disabled).toBe(true);
    type(document.body.querySelector<HTMLInputElement>('input:not([type="password"])')!, "jv_team");
    expect(button("保存")!.disabled).toBe(false);
    await act(async () => button("保存")!.click());
    await flush();
    const post = calls.find((c) => c.method === "POST" && c.path.endsWith("/launchers"));
    expect(post?.body).toEqual({ name: "jv_team", api_key: "sk-test-123456", preset: "example-gateway/team" });
  });

  it("tests a Responses preset using its configured protocol", async () => {
    await open();
    act(() => button("示例 Responses")!.click());
    type(keyInput(), "sk-test-123456");
    await waitAutoTest();
    expect(keyTests()).toHaveLength(1);
    expect(keyTests()[0]!.body).toMatchObject({ preset: "example-gateway/api", api_key: "sk-test-123456" });
    expect(text()).toContain("Key 能用");
    expect(button("保存")!.disabled).toBe(false);
  });

  it("keeps a direct API off the main conversation: no switch, no assign, no Claude Code warning", async () => {
    Object.assign(getState(), { features: ["builtin_presets", "assistant_readiness"] });
    readiness = withSteps("setup", [step("computer", "ok"), step("claude", "problem", "claude_missing"), step("model", "unknown"), step("first_message", "todo")]);
    await open();
    act(() => button("示例 Responses")!.click());
    expect(document.body.querySelector('button[role="switch"]')).toBeNull();
    // said before saving: uses with no model picked may move to it
    expect(text()).toContain("没有单独选模型的用途，现在可能会改用它");
    type(keyInput(), "sk-test-123456");
    await waitAutoTest();
    await act(async () => button("保存")!.click());
    await flush();
    const post = calls.find((c) => c.method === "POST" && c.path.endsWith("/launchers"));
    expect(post?.body).toEqual({ api_key: "sk-test-123456", preset: "example-gateway/api" });
    expect(text()).toContain("主对话和 Agent 不能用它");
    expect(text()).not.toContain("要让主对话或 Agent 用它");
    expect(document.body.querySelector('[data-testid="wizard-no-claude"]')).toBeNull();
  });

  it("asks for the address and the auth only for the custom gateway", async () => {
    await open();
    act(() => button("自定义兼容网关")!.click());
    expect(text()).toContain("鉴权方式");
    expect(text()).toContain("地址");
  });

  it("lets a custom Responses provider test and discover using its selected protocol", async () => {
    await open();
    act(() => button("自定义兼容网关")!.click());
    act(() => button("直接 API")!.click());
    act(() => button("OpenAI Responses")!.click());
    const inputs = [...document.body.querySelectorAll<HTMLInputElement>("input")];
    type(inputs.find((input) => input.placeholder.includes("my_gateway"))!, "custom");
    type(inputs.find((input) => input.inputMode === "url")!, "https://api.example.com/prefix");
    type(keyInput(), "sk-test-123456");
    type(document.body.querySelector<HTMLInputElement>('input[aria-label="测试用的模型"]')!, "example-r");
    await act(async () => button("测试连接")!.click());
    await act(async () => button("获取模型列表")!.click());
    expect(keyTests()[0]!.body).toMatchObject({ api: "responses", model: "example-r", base_url: "https://api.example.com/prefix" });
    expect(calls.find((c) => c.path.endsWith("/launchers/discover"))?.body).toMatchObject({ api: "responses" });
  });

  it("tests, discovers and saves a Chat Completions provider", async () => {
    await open();
    act(() => button("自定义兼容网关")!.click());
    act(() => button("直接 API")!.click());
    act(() => button("Chat Completions")!.click());
    expect(text()).toContain("Chat Completions 兼容 API 的基础地址");
    expect(text()).not.toContain("鉴权方式");
    const inputs = [...document.body.querySelectorAll<HTMLInputElement>("input")];
    type(inputs.find((input) => input.placeholder.includes("my_gateway"))!, "custom");
    type(inputs.find((input) => input.inputMode === "url")!, "https://api.example.com/v1");
    type(keyInput(), "sk-test-123456");
    type(document.body.querySelector<HTMLInputElement>('input[aria-label="测试用的模型"]')!, "example-chat");
    await act(async () => button("测试连接")!.click());
    await act(async () => button("获取模型列表")!.click());
    expect(keyTests()[0]!.body).toMatchObject({ api: "chat", model: "example-chat", base_url: "https://api.example.com/v1" });
    expect(calls.find((c) => c.path.endsWith("/launchers/discover"))?.body).toMatchObject({ api: "chat" });
    act(() => button("下一步")!.click());
    type(document.body.querySelector<HTMLInputElement>('input[aria-label="手动添加模型 ID"]')!, "example-chat");
    act(() => button("添加")!.click());
    await act(async () => button("保存")!.click());
    expect(calls.find((c) => c.method === "POST" && c.path.endsWith("/launchers"))?.body).toMatchObject({ kind: "api", api: "chat", auth: "api_key", model: "example-chat" });
  });

  it("saves discovered Chat models with inconsistent effort metadata", async () => {
    discovered = [{ id: "example-chat", context_window: 16000, efforts: ["low"], default_effort: "high" }];
    await open();
    act(() => button("自定义兼容网关")!.click());
    act(() => button("直接 API")!.click());
    act(() => button("Chat Completions")!.click());
    const inputs = [...document.body.querySelectorAll<HTMLInputElement>("input")];
    type(inputs.find((input) => input.placeholder.includes("my_gateway"))!, "custom");
    type(inputs.find((input) => input.inputMode === "url")!, "https://api.example.com/v1");
    type(keyInput(), "sk-test-123456");
    await act(async () => button("获取模型列表")!.click());
    act(() => button("下一步")!.click());
    expect(button("保存")!.disabled).toBe(false);
    await act(async () => button("保存")!.click());
    expect(calls.find((c) => c.method === "POST" && c.path.endsWith("/launchers"))?.body).toMatchObject({
      api: "chat", models: [{ id: "example-chat", context_window: 16000 }],
    });
  });

  it("hides unsupported chat options and excludes drafts from test and save", async () => {
    await open();
    act(() => button("自定义兼容网关")!.click());
    act(() => button("直接 API")!.click());
    const inputs = [...document.body.querySelectorAll<HTMLInputElement>("input")];
    type(inputs.find((input) => input.placeholder.includes("my_gateway"))!, "custom");
    type(inputs.find((input) => input.inputMode === "url")!, "https://api.example.com/v1");
    type(keyInput(), "sk-test-123456");
    act(() => button("下一步")!.click());
    type(document.body.querySelector<HTMLInputElement>('input[aria-label="手动添加模型 ID"]')!, "example-chat");
    act(() => button("添加")!.click());
    act(() => button("高级")!.click());
    const header = document.body.querySelector<HTMLTextAreaElement>('textarea[placeholder^="X-Name"]')!;
    const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!;
    act(() => { setter.call(header, "X-Example: value"); header.dispatchEvent(new Event("input", { bubbles: true })); });
    type(document.body.querySelector<HTMLInputElement>('input[placeholder="http://127.0.0.1:7890"]')!, "http://proxy.example.com");
    type(document.body.querySelector<HTMLInputElement>('input[placeholder="localhost,127.0.0.1"]')!, "example.com");
    type(document.body.querySelector<HTMLInputElement>('input[placeholder="3000000"]')!, "1234");
    act(() => button("上一步")!.click());
    act(() => button("Chat Completions")!.click());
    await act(async () => button("测试连接")!.click());
    expect(keyTests()[0]!.body).not.toHaveProperty("headers");
    act(() => button("下一步")!.click());
    expect(text()).toContain("上下文限制、默认思考档位");
    expect(text()).toContain("模型列表中仍可记录各模型的上下文窗口");
    expect(document.body.querySelector('textarea[placeholder^="X-Name"]')).toBeNull();
    expect(document.body.querySelector('input[placeholder="http://127.0.0.1:7890"]')).toBeNull();
    expect(document.body.querySelector('input[placeholder="localhost,127.0.0.1"]')).toBeNull();
    expect(document.body.querySelector('input[placeholder="3000000"]')).toBeNull();
    expect(document.body.querySelector('textarea[placeholder^="NAME=value"]')).toBeNull();
    act(() => button("上一步")!.click());
    act(() => button("Anthropic Messages")!.click());
    act(() => button("下一步")!.click());
    expect(document.body.querySelector<HTMLInputElement>('input[placeholder="3000000"]')?.value).toBe("1234");
    expect(document.body.querySelector<HTMLInputElement>('input[placeholder="http://127.0.0.1:7890"]')?.value).toBe("http://proxy.example.com");
    act(() => button("上一步")!.click());
    act(() => button("Chat Completions")!.click());
    act(() => button("下一步")!.click());
    await act(async () => button("保存")!.click());
    const body = calls.find((c) => c.method === "POST" && c.path.endsWith("/launchers"))?.body;
    expect(body).toMatchObject({ api: "chat" });
    for (const key of ["headers", "proxy", "no_proxy", "env", "timeout_ms", "compact_window", "max_context", "effort"]) expect(body).not.toHaveProperty(key);
  });

  it("keeps model metadata while hiding unused chat thinking controls", async () => {
    act(() => root.render(<ProviderWizard existing={{ name: "chat", kind: "api", api: "chat", base_url: "https://api.example.com", model: "example-chat", has_key: true, models: [{ id: "example-chat", context_window: 16000, efforts: ["high"], default_effort: "high" }] }} onClose={() => {}} onSaved={() => {}} />));
    await flush();
    act(() => button("下一步")!.click());
    expect(document.body.querySelector<HTMLInputElement>('input[aria-label="example-chat 的上下文窗口"]')?.value).toBe("16000");
    expect(document.body.querySelector('[aria-label="example-chat 支持的思考档位"]')).toBeNull();
    expect(document.body.querySelector('[aria-label="example-chat 的默认思考档位"]')).toBeNull();
    expect(text()).toContain("Chat Completions 不使用模型的思考档位设置");
    await act(async () => button("保存")!.click());
    expect(calls.find((c) => c.method === "PUT" && c.path.endsWith("/launchers/chat"))?.body).not.toHaveProperty("models");
  });

  it("preserves existing Chat effort metadata when discovery updates context", async () => {
    discovered = [{ id: "example-chat", context_window: 32000, efforts: ["low"], default_effort: "low" }];
    act(() => root.render(<ProviderWizard existing={{ name: "chat", kind: "api", api: "chat", base_url: "https://api.example.com", model: "example-chat", has_key: true, models: [{ id: "example-chat", efforts: ["high"], default_effort: "high" }] }} onClose={() => {}} onSaved={() => {}} />));
    await flush();
    await act(async () => button("获取模型列表")!.click());
    act(() => button("下一步")!.click());
    await act(async () => button("保存")!.click());
    expect(calls.find((c) => c.method === "PUT" && c.path.endsWith("/launchers/chat"))?.body).toMatchObject({
      models: [{ id: "example-chat", context_window: 32000, efforts: ["high"], default_effort: "high" }],
    });
  });

  it("requires an explicit choice before clearing unsupported stored Chat settings", async () => {
    const existing = { name: "chat", kind: "api" as const, api: "chat" as const, base_url: "https://api.example.com", model: "example-chat", has_key: true, has_headers: true, has_env: true, proxy: "http://proxy.example.com", no_proxy: "example.com", timeout_ms: 1200, compact_window: 100, max_context: 200, effort: "high", opus_model: "tier-a", sonnet_model: "tier-b", haiku_model: "tier-c", fable_model: "tier-d", subagent_force: true };
    act(() => root.render(<ProviderWizard existing={existing} onClose={() => {}} onSaved={() => {}} />));
    await flush();
    expect(text()).toContain("保存时清除这些设置");
    expect(text()).toContain("请先清除已保存的设置并保存供应商，再测试连接或获取模型列表");
    expect(button("测试连接")!.disabled).toBe(true);
    expect(button("获取模型列表")!.disabled).toBe(true);
    act(() => { button("测试连接")!.click(); button("获取模型列表")!.click(); });
    expect(calls.some((c) => /\/launchers\/(test|discover)$/.test(c.path))).toBe(false);
    const checkbox = document.body.querySelector<HTMLInputElement>('input[type="checkbox"]')!;
    expect(checkbox.checked).toBe(false);
    expect(button("保存")!.disabled).toBe(true);
    expect(calls.some((c) => c.method === "PUT")).toBe(false);
    act(() => checkbox.click());
    expect(button("保存")!.disabled).toBe(false);
    expect(button("测试连接")!.disabled).toBe(true);
    expect(button("获取模型列表")!.disabled).toBe(true);
    act(() => { button("测试连接")!.click(); button("获取模型列表")!.click(); });
    expect(calls.some((c) => /\/launchers\/(test|discover)$/.test(c.path))).toBe(false);
    act(() => checkbox.click());
    expect(button("保存")!.disabled).toBe(true);
    act(() => checkbox.click());
    await act(async () => button("保存")!.click());
    expect(calls.find((c) => c.method === "PUT" && c.path.endsWith("/launchers/chat"))?.body).toMatchObject({
      headers: "", env: "", proxy: "", no_proxy: "", timeout_ms: 0, compact_window: 0, max_context: 0,
      effort: "", opus_model: "", sonnet_model: "", haiku_model: "", fable_model: "", subagent_force: false,
    });
  });

  it("keeps the Chat Completions protocol when editing", async () => {
    act(() => root.render(<ProviderWizard existing={{ name: "chat", kind: "api", api: "chat", base_url: "https://api.example.com", model: "example-chat", has_key: true }} onClose={() => {}} onSaved={() => {}} />));
    await flush();
    expect(text()).toContain("Chat Completions 兼容 API 的基础地址");
    expect(text()).not.toContain("鉴权方式");
    await act(async () => button("保存")!.click());
    expect(calls.find((c) => c.method === "PUT" && c.path.endsWith("/launchers/chat"))?.body).toMatchObject({ api: "chat", auth: "api_key" });
  });

  it("leaves a Claude Code that already answers in place unless told otherwise", async () => {
    Object.assign(getState(), { features: ["builtin_presets", "assistant_readiness"] });
    readiness = withSteps("full", [step("computer", "ok"), step("claude", "ok"), step("model", "ok"), step("first_message", "ok")]);
    await open();
    act(() => button("智谱 GLM")!.click());
    expect(document.body.querySelector('button[role="switch"]')?.getAttribute("aria-checked")).toBe("false");
    expect(text()).toContain("小林的 MacBook 上的 Claude Code，已经能用");
    type(keyInput(), "sk-test-123456");
    await waitAutoTest();
    const before = readinessGets();
    await act(async () => button("保存")!.click());
    await flush();
    const post = calls.find((c) => c.method === "POST" && c.path.endsWith("/launchers"));
    expect(post?.body).not.toHaveProperty("assign");
    // saving asks for the readiness again, so the main conversation's card follows
    expect(readinessGets()).toBeGreaterThan(before);
    expect(document.body.querySelector('[data-testid="wizard-no-claude"]')).toBeNull();
  });

  it("waits for a slow readiness before deciding to take over the main conversation", async () => {
    Object.assign(getState(), { features: ["builtin_presets", "assistant_readiness"] });
    readiness = withSteps("full", [step("computer", "ok"), step("claude", "ok"), step("model", "ok"), step("first_message", "ok")]);
    let answer!: () => void;
    readinessGate = new Promise((r) => (answer = r));
    await open();
    act(() => button("智谱 GLM")!.click());
    // not known yet: no switch to read as on
    expect(document.body.querySelector('button[role="switch"]')).toBeNull();
    expect(text()).toContain("正在看主对话现在能不能用");
    type(keyInput(), "sk-test-123456");
    // the key test runs and saving is asked for while the readiness is still out: only the gate releases it
    await waitAutoTest();
    expect(keyTests()).toHaveLength(1);
    expect(document.body.querySelector('button[role="switch"]')).toBeNull();
    await act(async () => button("保存")!.click());
    await flush();
    expect(calls.some((c) => c.method === "POST" && c.path.endsWith("/launchers"))).toBe(false);
    await act(async () => answer());
    await flush();
    const post = calls.find((c) => c.method === "POST" && c.path.endsWith("/launchers"));
    expect(post?.body).not.toHaveProperty("assign");
  });

  it("says Claude Code is missing without promising the main conversation when the key did not go to it", async () => {
    Object.assign(getState(), { features: ["builtin_presets", "assistant_readiness"] });
    readiness = withSteps("setup", [step("computer", "ok"), step("claude", "problem", "claude_missing"), step("model", "unknown"), step("first_message", "todo")]);
    await open();
    act(() => button("智谱 GLM")!.click());
    act(() => (document.body.querySelector('button[role="switch"]') as HTMLButtonElement).click());
    type(keyInput(), "sk-test-123456");
    await waitAutoTest();
    await act(async () => button("保存")!.click());
    await flush();
    const note = document.body.querySelector('[data-testid="wizard-no-claude"]');
    expect(note?.textContent).toContain("Key 存好了；要用它，还得先在 小林的 MacBook 上装 Claude Code。");
  });

  it("says the key is saved but the computer still needs Claude Code", async () => {
    Object.assign(getState(), { features: ["builtin_presets", "assistant_readiness"] });
    readiness = withSteps("setup", [step("computer", "ok"), step("claude", "problem", "claude_missing"), step("model", "unknown"), step("first_message", "todo")]);
    await open();
    act(() => button("智谱 GLM")!.click());
    // nothing answers yet: the new key takes over the defaults
    expect(document.body.querySelector('button[role="switch"]')?.getAttribute("aria-checked")).toBe("true");
    type(keyInput(), "sk-test-123456");
    await waitAutoTest();
    // after saving the hub may no longer say it (a provider of its own reads unknown): what it said before still holds
    readiness = withSteps("setup", [step("computer", "ok"), step("claude", "unknown"), step("model", "unknown"), step("first_message", "todo")]);
    await act(async () => button("保存")!.click());
    await flush();
    const note = document.body.querySelector('[data-testid="wizard-no-claude"]');
    expect(note?.textContent).toContain("Key 存好了；还要在 小林的 MacBook 上装 Claude Code，主对话才能用它。");
    act(() => button("怎么装")!.click());
    expect(text()).toContain("claude.ai/install.sh");
  });
});

// docs/integrations/launchers.md "Device scope and on-demand delivery": the "Devices" step, sent only as changed.
describe("ProviderWizard device scope", () => {
  const scoped = { name: "gw", base_url: "https://gw.example.com", model: "m1", has_key: true, auth: "bearer" as const, models: [{ id: "m1" }], share: "all" as const, delivery: "on_demand" as const, own_service: "skip" as const, key_machines: ["box"] };
  const put = () => calls.find((c) => c.method === "PUT" && c.path.endsWith("/launchers/gw"));
  const scopeKeys = ["share", "scope_machines", "delivery", "own_service", "machine_keys"];
  const labelled = (label: string) => {
    const el = [...document.body.querySelectorAll("label")].find((l) => l.textContent === label);
    return document.getElementById(el!.htmlFor) as HTMLInputElement;
  };
  async function edit(existing: Record<string, unknown>) {
    act(() => root.render(<ProviderWizard existing={existing as never} onClose={() => {}} onSaved={() => {}} />));
    await flush();
  }
  const toDevices = () => {
    act(() => button("下一步")!.click());
    act(() => button("下一步")!.click());
  };

  beforeEach(() => {
    Object.assign(getState(), { features: ["builtin_presets", "launcher_scope"], machines: [{ id: "mbp", name: "MacBook", online: true }, { id: "box", name: "Test computer", online: true }] });
  });
  afterEach(() => {
    Object.assign(getState(), { machines: [] });
  });

  it("leaves the scope fields out of a PUT when they did not change", async () => {
    await edit(scoped);
    toDevices();
    expect(text()).toContain("我的所有电脑");
    await act(async () => button("保存")!.click());
    const body = put()?.body ?? {};
    for (const k of scopeKeys) expect(body, k).not.toHaveProperty(k);
  });

  it("has no Devices step for a provider whose hub has no scope", async () => {
    const { share: _share, delivery: _delivery, own_service: _own, key_machines: _keys, ...plain } = scoped;
    await edit(plain);
    act(() => button("下一步")!.click());
    expect(button("下一步")).toBeUndefined();
    await act(async () => button("保存")!.click());
    for (const k of scopeKeys) expect(put()?.body, k).not.toHaveProperty(k);
  });

  it("sends a computer's key only when one is typed, and \"\" to remove a stored one", async () => {
    await edit(scoped);
    toDevices();
    act(() => button("单台电脑的 Key")!.click());
    expect(text()).toContain("已有单独的 Key");
    type(labelled("MacBook 的 Key"), "sk-mbp-1");
    act(() => button("移除")!.click());
    await act(async () => button("保存")!.click());
    expect(put()?.body).toMatchObject({ machine_keys: { mbp: "sk-mbp-1", box: "" } });
    for (const k of ["share", "scope_machines", "delivery", "own_service"]) expect(put()?.body, k).not.toHaveProperty(k);
  });

  it("blocks saving chosen computers until one is checked", async () => {
    await edit(scoped);
    toDevices();
    act(() => button("指定电脑")!.click());
    expect(text()).toContain("至少选一台电脑");
    expect(button("保存")!.disabled).toBe(true);
    await act(async () => button("保存")!.click());
    expect(put()).toBeUndefined();
    act(() => button("Test computer")!.click());
    act(() => (document.body.querySelector('button[role="switch"][aria-label="每台电脑存一份"]') as HTMLButtonElement).click());
    expect(button("保存")!.disabled).toBe(false);
    await act(async () => button("保存")!.click());
    expect(put()?.body).toMatchObject({ share: "selected", scope_machines: ["box"], delivery: "push" });
    expect(put()?.body).not.toHaveProperty("own_service");
  });

  it("explains a provider saved before scopes existed", async () => {
    await edit({ ...scoped, delivery: "push", own_service: "use", legacy_scope: true, key_machines: [] });
    toDevices();
    expect(text()).toContain("这个供应商是在有电脑范围之前存的");
    await act(async () => button("保存")!.click());
    for (const k of scopeKeys) expect(put()?.body, k).not.toHaveProperty(k);
  });

  it("adds a preset with only the scope fields changed from the defaults", async () => {
    await open();
    act(() => button("智谱 GLM")!.click());
    type(keyInput(), "sk-test-123456");
    await waitAutoTest();
    act(() => button("电脑")!.click());
    act(() => button("不给电脑（只在 hub）")!.click());
    await act(async () => button("保存")!.click());
    await flush();
    const post = calls.find((c) => c.method === "POST" && c.path.endsWith("/launchers"));
    expect(post?.body).toEqual({ api_key: "sk-test-123456", preset: "builtin/glm", assign: ["assistant", "agent.new"], share: "none" });
  });
});

// #5029: a provider has a display name in any language; the startup ID launches and settings refer to is the hub's to
// make up (launcher_auto_name), typed only under More options, and never changes once saved.
describe("ProviderWizard names", () => {
  /** The input a field's label names; null when it is not on screen. */
  const field = (label: string) => {
    const el = [...document.body.querySelectorAll("label")].find((l) => l.textContent === label);
    return el ? (document.getElementById(el.htmlFor) as HTMLInputElement) : null;
  };
  const note = (input: HTMLInputElement) => document.getElementById(input.getAttribute("aria-describedby") ?? "")?.textContent;
  const urlInput = () => document.body.querySelector<HTMLInputElement>('input[inputmode="url"]')!;
  const addModelInput = () => document.body.querySelector<HTMLInputElement>('input[aria-label="手动添加模型 ID"]');
  const alertText = () => document.body.querySelector('[role="alert"]')?.textContent;
  const autoName = () => Object.assign(getState(), { features: ["builtin_presets", "launcher_auto_name"] });
  /** The wizard, with the toasts it leaves. */
  async function show(existing: ProviderView | "new") {
    act(() => root.render(<><ProviderWizard existing={existing} onClose={() => {}} onSaved={() => {}} /><Toasts /></>));
    await flush();
  }
  async function custom() {
    await show("new");
    act(() => button("自定义兼容网关")!.click());
  }
  function connect() {
    type(urlInput(), "https://relay.example.com/v1");
    type(keyInput(), "sk-test-123456");
  }

  it("names a new gateway in any language and leaves its startup ID to the hub", async () => {
    autoName();
    await custom();
    expect(button("更多选项")!.getAttribute("aria-expanded")).toBe("false");
    expect(field("启动 ID")).toBeNull();
    const display = field("显示名")!;
    expect(display.className).not.toContain("font-mono");
    expect(note(display)).toBe("选供应商的地方都显示这个名字，用什么语言都行。");
    expect(display.placeholder).toBe("比如 团队中转");
    connect();
    // left empty, the hub names it after the host
    expect(display.placeholder).toBe("relay.example.com");
    type(display, "我的中转 站");
    act(() => button("下一步")!.click());
    type(addModelInput()!, "m1");
    act(() => button("添加")!.click());
    await act(async () => button("保存")!.click());
    await flush();
    const post = calls.find((c) => c.method === "POST" && c.path.endsWith("/launchers"));
    expect(post?.body).toMatchObject({ name: "", display_name: "我的中转 站", base_url: "https://relay.example.com/v1", model: "m1" });
    expect(text()).toContain("已新增 我的中转 站");
  });

  it("still asks an older hub's gateway for the startup ID, right under the display name", async () => {
    await custom();
    expect(button("更多选项")).toBeUndefined();
    const id = field("启动 ID")!;
    expect(id.placeholder).toBe("比如 my_gateway");
    type(field("显示名")!, "Team relay");
    connect();
    act(() => button("下一步")!.click());
    expect(alertText()).toBe("要填启动 ID");
    type(id, "team_relay");
    act(() => button("下一步")!.click());
    expect(addModelInput()).not.toBeNull();
  });

  it("says what is wrong with a startup ID only once one is typed, and opens More options to show it", async () => {
    autoName();
    await custom();
    connect();
    act(() => button("更多选项")!.click());
    const id = field("启动 ID")!;
    expect(id.placeholder).toBe("留空自动生成");
    expect(id.getAttribute("aria-invalid")).toBeNull();
    expect(note(id)).toBe("字母、数字、_ 和 -。启动命令、Agent 预设和模型配置都按它引用，添加后不能再改。");
    type(id, "1 bad");
    expect(id.getAttribute("aria-invalid")).toBe("true");
    expect(note(id)).toBe("启动 ID 只能用字母、数字、_ 和 -，以字母或 _ 开头");
    act(() => button("更多选项")!.click());
    expect(button("更多选项")!.getAttribute("aria-expanded")).toBe("false");
    act(() => button("下一步")!.click());
    expect(alertText()).toBe("启动 ID 只能用字母、数字、_ 和 -，以字母或 _ 开头");
    expect(button("更多选项")!.getAttribute("aria-expanded")).toBe("true");
    expect(addModelInput()).toBeNull();
  });

  it("stops at a display name over 64 characters without keeping the connection from being checked", async () => {
    autoName();
    await custom();
    connect();
    const display = field("显示名")!;
    type(display, "字".repeat(65));
    expect(display.getAttribute("aria-invalid")).toBe("true");
    expect(note(display)).toBe("显示名最多 64 个字，不能换行");
    expect(button("测试连接")!.disabled).toBe(false);
    expect(button("获取模型列表")!.disabled).toBe(false);
    act(() => button("下一步")!.click());
    expect(alertText()).toBe("显示名最多 64 个字，不能换行");
    type(display, "字".repeat(64));
    expect(display.getAttribute("aria-invalid")).toBeNull();
    act(() => button("下一步")!.click());
    expect(addModelInput()).not.toBeNull();
  });

  it("renames a saved provider in the PUT and shows its startup ID read-only, with why", async () => {
    autoName();
    forgetProviderNames();
    await loadProviderNames();
    await show({ name: "gw", display_name: "Team relay", base_url: "https://gw.example.com", model: "m1", has_key: true, auth: "bearer", models: [{ id: "m1" }] });
    expect(text()).toContain("编辑 Team relay");
    const display = field("显示名")!;
    expect(display.value).toBe("Team relay");
    act(() => button("更多选项")!.click());
    const id = field("启动 ID")!;
    expect(id.disabled).toBe(true);
    expect(id.value).toBe("gw");
    expect(note(id)).toBe("不能改：电脑上的启动命令、Agent 预设和模型配置都按它引用。");
    // cleared, it shows the ID again
    type(display, "");
    expect(display.placeholder).toBe("gw");
    type(display, "团队中转");
    await act(async () => button("保存")!.click());
    await flush();
    const put = calls.find((c) => c.method === "PUT");
    expect(put).toMatchObject({ path: "/api/v1/launchers/gw", body: { display_name: "团队中转" } });
    expect(text()).toContain("已保存 团队中转");
    // other pages read the names again
    const reads = () => calls.filter((c) => c.path.endsWith("/model-config")).length;
    expect(reads()).toBe(1);
    await loadProviderNames();
    expect(reads()).toBe(2);
  });

  it("adds a preset under a display name of one's own, and calls its name field the startup ID", async () => {
    await open();
    act(() => button("智谱 GLM")!.click());
    act(() => button("更多选项")!.click());
    const display = field("显示名")!;
    expect(display.placeholder).toBe("智谱 GLM");
    expect(field("启动 ID")!.placeholder).toBe("留空自动生成");
    type(display, "团队的 GLM");
    type(keyInput(), "sk-test-123456");
    await waitAutoTest();
    await act(async () => button("保存")!.click());
    await flush();
    const post = calls.find((c) => c.method === "POST" && c.path.endsWith("/launchers"));
    expect(post?.body).toEqual({ display_name: "团队的 GLM", api_key: "sk-test-123456", preset: "builtin/glm", assign: ["assistant", "agent.new"] });
  });

  it("says why Save is off when More options folds a mistake away", async () => {
    await open();
    act(() => button("智谱 GLM")!.click());
    act(() => button("更多选项")!.click());
    type(field("显示名")!, "长".repeat(65));
    type(keyInput(), "sk-test-123456");
    await waitAutoTest();
    const alert = () => [...document.body.querySelectorAll('[role="alert"]')].map((a) => a.textContent).join("");
    expect(alert()).toBe("");
    act(() => button("更多选项")!.click());
    expect(alert()).toBe("显示名最多 64 个字，不能换行");
    expect(button("保存")!.disabled).toBe(true);
  });

  it("says a Messages address's trailing /v1 goes by itself, and that other bases are used as typed", async () => {
    await custom();
    const hint = () => note(urlInput());
    const dropped = "Anthropic Messages 兼容网关的基础地址；末尾的 /v1 会自动去掉。";
    expect(hint()).toBe(dropped);
    for (const [kind, want] of [
      ["Codex", "Responses 兼容 API 的基础地址，按填写的原样使用（大多以 /v1 结尾）。"],
      ["Antigravity", "Gemini 兼容 API 的基础地址，按填写的原样使用。"],
      ["omp", dropped],
      ["直接 API", dropped],
      ["OpenAI Responses", "Responses 兼容 API 的基础地址。"],
      ["Chat Completions", "Chat Completions 兼容 API 的基础地址。"],
    ]) {
      act(() => button(kind)!.click());
      expect(hint(), kind).toBe(want);
    }
  });
});
