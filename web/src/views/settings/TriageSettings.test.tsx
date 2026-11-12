// @vitest-environment jsdom
// Settings › Session triage (#4491): while the hub has not answered there are no switches to flip; a read that fails says why and
// "Retry" reads again, and is never drawn as "all off" (a flip would PUT off over the switches the hub has on); a hub
// without the route says it does not support it.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

type Pending = { method: string; json?: unknown; resolve: (v: unknown) => void; reject: (e: unknown) => void };
const pending: Pending[] = [];
vi.mock("../../lib/api", async (orig) => ({
  ...(await orig<typeof import("../../lib/api")>()),
  api: (_path: string, init?: { method?: string; json?: unknown }) =>
    new Promise((resolve, reject) => pending.push({ method: init?.method ?? "GET", json: init?.json, resolve, reject })),
}));
const toast = vi.fn();
vi.mock("../../ui/atoms", async (orig) => ({ ...(await orig<typeof import("../../ui/atoms")>()), toast: (...a: unknown[]) => toast(...a) }));

const { TriageSettings } = await import("./TriageSettings");
const { ApiError } = await import("../../lib/api");

let root: Root;
let host: HTMLDivElement;
beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  pending.length = 0;
  toast.mockClear();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

const ON = { enabled: true, auto_continue: true, wake: true, decide: true };
const render = () => act(async () => root.render(<TriageSettings />));
const switches = () => [...host.querySelectorAll<HTMLButtonElement>('[role="switch"]')];
const named = (label: string) => switches().find((b) => b.getAttribute("aria-label") === label);
const retry = () => [...host.querySelectorAll("button")].find((b) => b.textContent === "重试");
const answer = async (i: number, v: unknown) => act(async () => pending[i].resolve(v));
const fail = async (i: number, e: Error) => act(async () => pending[i].reject(e));
const hubSays = (status: number, msg: string) => new ApiError(status, msg, { error: msg }, true);

describe("设置 › 会话分诊", () => {
  it("hub 还没答时只写加载中，没有能点的开关", async () => {
    await render();
    expect(switches()).toHaveLength(0);
    expect(host.textContent).toContain("加载中");
    await answer(0, ON);
    expect(switches()).toHaveLength(8);
    expect(named("会话分诊")!.getAttribute("aria-checked")).toBe("true");
  });

  it("读不到时不画成全关：说为什么，没有开关可点，点重试再读", async () => {
    await render();
    await fail(0, hubSays(503, "hub 没回答"));
    expect(switches()).toHaveLength(0);
    // errorText explains a 503 and keeps the hub's own text after it (#3728)
    expect(host.querySelector('[role="alert"]')?.textContent).toMatch(/^没读到会话分诊的开关：.+（HTTP 503: hub 没回答）$/);
    await act(async () => retry()!.click());
    expect(pending).toHaveLength(2);
    expect(pending[1].method).toBe("GET");
    expect(host.textContent).not.toContain("没读到");
    await answer(1, ON);
    expect(retry()).toBeUndefined();
    expect(named("出错自动继续")!.getAttribute("aria-checked")).toBe("true");
  });

  it("重试读到后改一项，其余几项照 hub 的值发出去", async () => {
    await render();
    await fail(0, hubSays(503, "busy"));
    await act(async () => retry()!.click());
    await answer(1, ON);
    await act(async () => named("到点叫醒")!.click());
    expect(pending[2].method).toBe("PUT");
    expect(pending[2].json).toEqual({ ...ON, wake: false });
  });

  it("hub 不让改：开关回到原来的样子，并提示没改成", async () => {
    await render();
    await answer(0, ON);
    await act(async () => named("要你决定的开卡片")!.click());
    expect(named("要你决定的开卡片")!.getAttribute("aria-checked")).toBe("false"); // optimistic
    await fail(1, hubSays(500, "500"));
    expect(named("要你决定的开卡片")!.getAttribute("aria-checked")).toBe("true");
    expect(toast).toHaveBeenCalledWith(expect.stringMatching(/^没改成：.+（HTTP 500: 500）$/), "error");
  });

  it("重试又失败：写新的原因，还能再点重试", async () => {
    await render();
    await fail(0, hubSays(503, "第一次"));
    await act(async () => retry()!.click());
    await fail(1, new TypeError("Failed to fetch"));
    expect(switches()).toHaveLength(0);
    expect(host.querySelector('[role="alert"]')?.textContent).toBe("没读到会话分诊的开关：连不上 hub");
    await act(async () => retry()!.click());
    expect(pending).toHaveLength(3);
    await answer(2, ON);
    expect(switches()).toHaveLength(8);
  });

  it("tool loop defaults off and saves without changing the other switches", async () => {
    await render();
    await answer(0, ON);
    const loop = named("检测工具调用循环")!;
    expect(loop.getAttribute("aria-checked")).toBe("false");
    await act(async () => loop.click());
    expect(pending[1].json).toEqual({ ...ON, tool_loop: true });
    await answer(1, { ...ON, tool_loop: true });
    expect(loop.getAttribute("aria-checked")).toBe("true");
    await act(async () => named("到点叫醒")!.click());
    expect(pending[2].json).toEqual({ ...ON, tool_loop: true, wake: false });
  });

  it("ask for a closeout defaults off, saves with the hub's other switches, and needs the main switch", async () => {
    await render();
    // another switch the hub has on (closeout_ask) goes back to the hub as it came
    await answer(0, { ...ON, closeout_ask: true });
    const closeout = named("要收尾记录")!;
    expect(closeout.getAttribute("aria-checked")).toBe("false");
    expect(closeout.disabled).toBe(false);
    expect(host.textContent).toContain("会多花它自己模型的一小轮");
    await act(async () => closeout.click());
    expect(pending[1].json).toEqual({ ...ON, closeout_ask: true, closeout: true });
    // an older hub answers without the field: the switch is off again, as it is on that hub
    await answer(1, ON);
    expect(closeout.getAttribute("aria-checked")).toBe("false");
    await act(async () => closeout.click());
    await fail(2, hubSays(500, "500"));
    expect(closeout.getAttribute("aria-checked")).toBe("false");
    expect(toast).toHaveBeenCalledWith(expect.stringMatching(/^没改成：/), "error");
    await act(async () => named("会话分诊")!.click());
    await answer(3, { ...ON, enabled: false });
    expect(named("要收尾记录")!.disabled).toBe(true);
  });

  it("ask for the status afterwards (#3616) defaults off, saves with the hub's other switches, and needs the main switch", async () => {
    await render();
    await answer(0, { ...ON, closeout: true });
    const ask = named("事后问一次进展")!;
    expect(ask.getAttribute("aria-checked")).toBe("false");
    expect(ask.disabled).toBe(false);
    expect(host.textContent).toContain("会花它自己模型的一轮");
    await act(async () => ask.click());
    expect(pending[1].json).toEqual({ ...ON, closeout: true, closeout_ask: true });
    await answer(1, { ...ON, closeout: true, closeout_ask: true });
    expect(ask.getAttribute("aria-checked")).toBe("true");
    await act(async () => ask.click());
    expect(pending[2].json).toEqual({ ...ON, closeout: true, closeout_ask: false });
    await answer(2, { ...ON, closeout: true, closeout_ask: false });
    await act(async () => named("会话分诊")!.click());
    await answer(3, { ...ON, enabled: false, closeout: true });
    expect(named("事后问一次进展")!.disabled).toBe(true);
  });

  it("hub 没有这个接口（太旧）：说不支持，不给重试", async () => {
    await render();
    await fail(0, hubSays(404, "not found"));
    expect(switches()).toHaveLength(0);
    expect(retry()).toBeUndefined();
    expect(host.textContent).toContain("这台 hub 还不支持会话分诊");
  });

  it("会话分诊关着时另外三项不能点；正在保存时四项都不能点", async () => {
    await render();
    await answer(0, { enabled: false, auto_continue: false, wake: false, decide: false });
    expect(named("会话分诊")!.disabled).toBe(false);
    expect(named("要你决定的开卡片")!.disabled).toBe(true);
    expect(named("出错自动继续")!.disabled).toBe(true);
    expect(named("到点叫醒")!.disabled).toBe(true);
    expect(named("检测工具调用循环")!.disabled).toBe(true);
    await act(async () => named("会话分诊")!.click());
    expect(switches().every((b) => b.disabled)).toBe(true);
    await answer(1, { enabled: true, auto_continue: false, wake: false, decide: false });
    expect(named("会话分诊")!.disabled).toBe(false);
    expect(named("到点叫醒")!.disabled).toBe(false);
  });

  it("看过没回时提醒：hub 没给就是开着，不跟会话分诊总开关走，改它时整套一起发出去", async () => {
    await render();
    const off = { enabled: false, auto_continue: false, wake: false, decide: false };
    await answer(0, off);
    const reminder = () => named("看过没回时提醒")!;
    expect(reminder().getAttribute("aria-checked")).toBe("true");
    expect(reminder().disabled).toBe(false);
    expect(host.textContent).toContain("你看过 30 分钟还没回");
    await act(async () => reminder().click());
    expect(pending[1].method).toBe("PUT");
    expect(pending[1].json).toEqual({ ...off, reply_reminder: false });
    await answer(1, { ...off, reply_reminder: false });
    expect(reminder().getAttribute("aria-checked")).toBe("false");
  });

  it("看过没回时提醒：hub 存了关就画成关；hub 给的分钟数写进说明并原样发回", async () => {
    await render();
    await answer(0, { ...ON, reply_reminder: false, reply_reminder_minutes: 60 });
    expect(named("看过没回时提醒")!.getAttribute("aria-checked")).toBe("false");
    expect(host.textContent).toContain("你看过 60 分钟还没回");
    await act(async () => named("到点叫醒")!.click());
    expect(pending[1].json).toEqual({ ...ON, wake: false, reply_reminder: false, reply_reminder_minutes: 60 });
  });
});
