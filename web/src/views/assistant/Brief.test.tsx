// @vitest-environment jsdom
// A routine's `brief` item (Brief.tsx): the fixed part of a morning or evening run, drawn in the conversation, with an
// Accept button per task to verify that posts the round it was made for.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../../lib/api";
import type { BriefRow, FeedItem } from "../../lib/feed";

const api = vi.hoisted(() => vi.fn());
vi.mock("../../lib/api", async (orig) => ({ ...(await orig<typeof import("../../lib/api")>()), api }));
const toast = vi.hoisted(() => vi.fn());
vi.mock("../../ui/atoms", async (orig) => ({ ...(await orig<typeof import("../../ui/atoms")>()), toast }));
import { FeedLineView } from "./Tasks";
import { AcceptRow } from "./Brief";
import { getState } from "../../lib/store";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let root: Root;
let host: HTMLDivElement;
beforeEach(() => {
  api.mockReset();
  toast.mockReset();
  api.mockResolvedValue({});
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

const ASKED = "2026-11-06T01:02:03.123456789Z";
const body = (over: Record<string, unknown> = {}): Record<string, unknown> => ({
  title: "早报",
  at: "2026-11-07T01:00:00Z",
  hours: 12,
  waiting: [{ id: "ix1", title: "先发预发还是直接上线？", from: "来自 Agent「登录修复」", link: "https://hub.example/s/box/ag1", waited_s: 4800, nudged: true }],
  failed: [{ kind: "task", id: "todo_f", title: "修登录崩溃", at: "2026-11-07T00:10:00Z", why: "machine offline" }, { kind: "routine", id: "patrol", title: "巡检", at: "2026-11-07T00:20:00Z", skipped: true }],
  failed_total: 2,
  done: [{ id: "todo_d", title: "整理周报", at: "2026-11-06T22:00:00Z" }],
  done_total: 4,
  accept: [{ id: "todo_v1", title: "改设置页", detail: "设置页多了一个开关", from: "来自某个 Agent", waited_s: 7200, asked_at: ASKED }],
  ...over,
});
const askedLater = (n = 1): Record<string, unknown>[] => [
  { id: `lt_${n}`, title: "新接口用 snake_case 还是 camelCase？", from: "来自 Agent「网络层」", link: "https://hub.example/s/box/ag7", waited_s: 93600 },
  { id: `lt_${n + 1}`, title: "图标要不要换？", waited_s: 3600 },
];
const item = (b: Record<string, unknown> = body()): FeedItem => ({ id: "f1", conversation: "c1", at: "2026-11-07T01:00:05Z", kind: "brief", ref: "morning", body: b });

function draw(it: FeedItem) {
  act(() => root.render(<FeedLineView item={it} />));
}
function drawDecision(row: BriefRow) {
  act(() => root.render(<AcceptRow row={row} stacked />));
}
const rows = (kind: string) => [...host.querySelectorAll(`[data-brief-row=${kind}]`)];

describe("a brief item", () => {
  it("draws the sections: what waits, the last hours (failed first), what is to be verified", () => {
    draw(item());
    const text = host.textContent ?? "";
    for (const want of ["早报", "等你处理（1）", "先发预发还是直接上线？", "已等 1 小时 20 分", "已催过", "过去 12 小时：做完 4 · 没成 2", "修登录崩溃", "没开起来", "machine offline", "巡检", "被跳过", "整理周报", "待验收（1）", "改设置页", "设置页多了一个开关"]) {
      expect(text, want).toContain(want);
    }
    expect(text.indexOf("修登录崩溃")).toBeLessThan(text.indexOf("整理周报"));
    expect(host.querySelector<HTMLAnchorElement>("a[href='https://hub.example/s/box/ag1']")).not.toBeNull();
    expect(rows("accept")).toHaveLength(1);
    expect(text).toContain("还有 3 项"); // 4 done + 2 failed in all, 3 listed
  });

  it("draws a link only when it is a web address", () => {
    draw(item(body({ waiting: [{ id: "ix1", title: "一个问题", waited_s: 60, link: "javascript:alert(1)" }, { id: "ix2", title: "另一个", waited_s: 60, link: "https://hub.example/s/box/ag2" }] })));
    expect([...host.querySelectorAll("a")].map((a) => a.getAttribute("href"))).toEqual(["https://hub.example/s/box/ag2"]);
  });

  it("draws what is in progress last, with its stage bar and how many more there are", () => {
    draw(item(body({ progress: [{ id: "todo_w1", title: "重构设置页", stage: "develop" }, { id: "todo_w2", title: "整理文档" }], progress_total: 3 })));
    const text = host.textContent ?? "";
    expect(text).toContain("进行中（3）");
    expect(text.indexOf("待验收（1）")).toBeLessThan(text.indexOf("进行中（3）"));
    expect(rows("progress")).toHaveLength(2);
    expect(rows("progress")[0].querySelector("ol[aria-label]")).not.toBeNull(); // the stage bar
    expect(rows("progress")[1].querySelector("ol[aria-label]")).toBeNull(); // no stage known: no bar
    expect(text).toContain("还有 1 项");
    draw(item()); // nothing running: no section
    expect(host.textContent).not.toContain("进行中");
  });

  it("says so when nothing is there, and does not say that nothing waits when the cards were not read", () => {
    draw(item(body({ waiting: [], failed: [], failed_total: 0, done: [], done_total: 0, accept: [] })));
    expect(host.textContent).toContain("等你处理（0）");
    expect(host.querySelectorAll("[data-brief-section]")).toHaveLength(3);
    expect(host.textContent).not.toContain("还有");
    draw(item(body({ waiting: [], waiting_unknown: true })));
    expect(host.textContent).toContain("还没读到等你处理的卡片");
    expect(host.textContent).not.toContain("等你处理（0）");
  });

  it("draws the questions that can wait after what is to be verified and before what is in progress, with how many more", () => {
    draw(item(body({ later: askedLater(), later_total: 3, progress: [{ id: "todo_w1", title: "重构设置页" }], progress_total: 1 })));
    const text = host.textContent ?? "";
    for (const want of ["可以晚点答（3）", "新接口用 snake_case 还是 camelCase？", "已等 26 小时", "来自 Agent「网络层」", "图标要不要换？", "还有 1 项"]) {
      expect(text, want).toContain(want);
    }
    expect(text.indexOf("待验收（1）")).toBeLessThan(text.indexOf("可以晚点答（3）"));
    expect(text.indexOf("可以晚点答（3）")).toBeLessThan(text.indexOf("进行中（1）"));
    expect(text).not.toContain("还有 1 项，到任务页看"); // the queue's rest is not on the Tasks page
    expect(rows("later")).toHaveLength(2);
    expect(host.querySelector<HTMLAnchorElement>("a[href='https://hub.example/s/box/ag7']")).not.toBeNull(); // the Agent's chat
  });

  it("has no such section without questions, as for a hub that does not know them", () => {
    draw(item());
    expect(host.textContent).not.toContain("晚点答");
    expect(rows("later")).toHaveLength(0);
    draw(item(body({ later: "x", later_total: 5 }))); // not a list: nothing to draw
    expect(rows("later")).toHaveLength(0);
    expect(host.textContent).not.toContain("晚点答");
  });

  it("Handled posts the question's id, then says it is marked", async () => {
    draw(item(body({ later: askedLater(), later_total: 2 })));
    const btn = [...host.querySelectorAll("button")].filter((b) => b.textContent === "已处理")[0];
    await act(async () => btn.click());
    expect(api).toHaveBeenCalledWith("/later/lt_1/handled", { method: "POST" });
    expect(host.querySelectorAll("[data-brief-marked=marked]")).toHaveLength(1);
    expect(host.querySelector("[data-brief-marked=marked]")?.textContent).toBe("已标记为处理过");
    expect([...host.querySelectorAll("button")].filter((b) => b.textContent === "已处理")).toHaveLength(1); // the other one is still there
  });

  it("a question that is gone (409) says it was handled; another failure keeps the button for a retry", async () => {
    draw(item(body({ later: askedLater(), later_total: 2 })));
    api.mockRejectedValueOnce(new ApiError(409, "已经处理过了", {}));
    await act(async () => [...host.querySelectorAll("button")].filter((b) => b.textContent === "已处理")[0].click());
    expect(host.querySelector("[data-brief-marked=handled]")?.textContent).toBe("已经处理过了");

    draw(item(body({ later: askedLater(3), later_total: 2 }))); // other ids: a new row, not the one that was marked
    api.mockRejectedValueOnce(new ApiError(500, "boom", {}));
    await act(async () => [...host.querySelectorAll("button")].filter((b) => b.textContent === "已处理")[0].click());
    expect(host.querySelector("[data-brief-marked]")).toBeNull();
    expect([...host.querySelectorAll("button")].filter((b) => b.textContent === "已处理" && !b.disabled)).toHaveLength(2);
  });

  it("draws nothing for a body that is not one", () => {
    draw({ ...item(), body: undefined });
    expect(host.querySelector("[data-testid=brief-card]")).toBeNull();
    draw(item(body({ waiting: null, failed: "x", done: undefined, accept: 7 })));
    expect(host.querySelector("[data-testid=brief-card]")).not.toBeNull(); // lists it does not understand are empty ones
  });

  it("Accept posts the round it was made for, then shows the task as accepted", async () => {
    draw(item());
    const btn = [...host.querySelectorAll("button")].find((b) => b.textContent === "通过")!;
    await act(async () => btn.click());
    expect(api).toHaveBeenCalledWith("/todos/todo_v1/accept", { method: "POST", json: { asked_at: ASKED } });
    expect(host.querySelector("[data-brief-accepted=accepted]")?.textContent).toBe("已验收");
    expect([...host.querySelectorAll("button")].some((b) => b.textContent === "通过")).toBe(false);
  });

  it("a round that is over (409) says it was handled; another failure keeps the button for a retry", async () => {
    draw(item());
    api.mockRejectedValueOnce(new ApiError(409, "已经处理过了", {}));
    await act(async () => [...host.querySelectorAll("button")].find((b) => b.textContent === "通过")!.click());
    expect(host.querySelector("[data-brief-accepted=handled]")?.textContent).toBe("已经处理过了");

    draw(item(body({ accept: [{ id: "todo_v2", title: "另一件", waited_s: 60, asked_at: ASKED }] })));
    api.mockRejectedValueOnce(new ApiError(500, "boom", {}));
    await act(async () => [...host.querySelectorAll("button")].find((b) => b.textContent === "通过")!.click());
    expect(host.querySelector("[data-brief-accepted]")).toBeNull();
    expect([...host.querySelectorAll("button")].some((b) => b.textContent === "通过" && !b.disabled)).toBe(true);
  });

  it("draws each row's verdict as the hub wrote it; a conclusion's button says Got it, a decision's takes the recommendation", async () => {
    const accept = [
      { id: "todo_v1", title: "改设置页", detail: "设置页多了一个开关", verdict: "✅ 做完了：已部署，按下面验收", waited_s: 60, asked_at: ASKED },
      { id: "todo_c1", title: "调研直连", verdict: "⛔ 做不了：对方接口不对外开放", outcome: "cannot", waited_s: 60, asked_at: ASKED },
      { id: "todo_d1", title: "上线方式", verdict: "🤔 要你拍板：先灰度还是直接全量", outcome: "decide", recommend: "先灰度 10%", waited_s: 60, asked_at: ASKED },
      { id: "todo_d2", title: "没推荐的", verdict: "🤔 要你拍板：用哪个库", outcome: "decide", waited_s: 60, asked_at: ASKED },
    ];
    draw(item(body({ accept })));
    const [deployed, cannot, decide, none] = rows("accept");
    expect(deployed.querySelector("[data-brief-verdict]")?.textContent).toBe("✅ 做完了：已部署，按下面验收");
    expect(deployed.textContent).toContain("设置页多了一个开关");
    expect([...deployed.querySelectorAll("button")].map((b) => b.textContent)).toContain("通过");
    expect(cannot.querySelector("[data-brief-verdict=cannot]")?.textContent).toBe("⛔ 做不了：对方接口不对外开放");
    expect([...cannot.querySelectorAll("button")].map((b) => b.textContent)).toContain("知道了");
    expect(decide.textContent).toContain("推荐：先灰度 10%");
    const take = [...decide.querySelectorAll("button")].find((b) => b.textContent === "按推荐来")!;
    await act(async () => take.click());
    expect(api).toHaveBeenCalledWith("/todos/todo_d1/accept", { method: "POST", json: { asked_at: ASKED } });
    expect(decide.querySelector("[data-brief-accepted=accepted]")?.textContent).toBe("已按推荐来");
    expect(none.textContent).toContain("用哪个库");
    expect([...none.querySelectorAll("button")].map((b) => b.textContent)).toEqual(["打开"]); // a historical snapshot cannot finish a newer run
    expect(host.textContent).not.toContain("直接完成");
  });

  it("offers direct completion in live Pending even without a recommendation", () => {
    drawDecision({ id: "todo_no_recommendation", title: "Synthetic decision", outcome: "decide", waited_s: 60, asked_at: ASKED });
    expect([...rows("accept")[0].querySelectorAll("button")].map((b) => b.textContent)).toEqual(["打开", "直接完成"]);
  });

  it("finishes a decision using PATCH only, then clears its ask and shows the completion receipt", async () => {
    const ask = { outcome: "decide" as const, verdict: "Choose the rollout", recommend: "Use a staged rollout", asked_at: ASKED };
    const todo = { id: "todo_finish", title: "Demo rollout", created_at: ASKED, updated_at: ASKED, ask };
    const before = getState().todos;
    getState().todos = [todo];
    const finished = { ...todo, done_at: ASKED, ask: undefined };
    // JSON omits cleared optional fields, as the hub does.
    api.mockImplementation(async (path, opts) => opts?.method === "PATCH" ? { todo: JSON.parse(JSON.stringify(finished)) } : path === "/todos" ? { todos: [finished] } : {});
    try {
      drawDecision({ ...todo, ...ask, waited_s: 60 });
      const buttons = [...rows("accept")[0].querySelectorAll("button")];
      expect(buttons.map((b) => b.textContent)).toEqual(["打开", "直接完成", "按推荐来"]);
      expect(buttons[1].classList).toContain("shadow-btn");
      expect(buttons[2].classList).toContain("bg-ink");
      await act(async () => buttons[1].click());
      expect(api.mock.calls.filter(([, opts]) => opts?.method && opts.method !== "GET")).toEqual([["/todos/todo_finish", { method: "PATCH", json: { done: true } }]]);
      expect(getState().todos[0].done_at).toBe(ASKED);
      expect(getState().todos[0].ask).toBeUndefined();
      expect(host.querySelector("[data-brief-accepted=finished]")?.textContent).toBe("已完成，不再交给 Agent");
      expect(toast).toHaveBeenCalledWith("已完成，不再交给 Agent", "ok");
      expect([...rows("accept")[0].querySelectorAll("button")].map((b) => b.textContent)).toEqual(["打开"]);
    } finally {
      getState().todos = before;
    }
  });

  it("keeps both decision actions disabled during completion, rejects a mixed double-click, and lets a failure retry", async () => {
    drawDecision({ id: "todo_finish", title: "Demo rollout", outcome: "decide", recommend: "Stage it", waited_s: 60, asked_at: ASKED });
    let reject!: (e: unknown) => void;
    api.mockImplementationOnce(() => new Promise((_, fail) => { reject = fail; }));
    const buttons = [...rows("accept")[0].querySelectorAll("button")];
    act(() => { buttons[1].click(); buttons[2].click(); buttons[1].click(); });
    expect(api).toHaveBeenCalledTimes(1);
    expect(buttons[1].disabled).toBe(true);
    expect(buttons[2].disabled).toBe(true);
    await act(async () => reject(new ApiError(500, "Synthetic save failure", {})));
    expect(host.querySelector("[data-brief-accepted]")).toBeNull();
    expect(buttons[1].disabled).toBe(false);
    expect(buttons[2].disabled).toBe(false);
    expect(toast).toHaveBeenCalledWith(expect.stringContaining("Synthetic save failure"), "error");
    api.mockRejectedValueOnce(new ApiError(500, "Synthetic retry failure", {}));
    await act(async () => buttons[1].click());
    expect(api).toHaveBeenCalledTimes(2);
  });

  it("does not finish while recommendation delivery is in flight", async () => {
    drawDecision({ id: "todo_finish", title: "Demo rollout", outcome: "decide", recommend: "Stage it", waited_s: 60, asked_at: ASKED });
    api.mockImplementationOnce(() => new Promise(() => {}));
    const buttons = [...rows("accept")[0].querySelectorAll("button")];
    act(() => { buttons[2].click(); buttons[1].click(); });
    expect(api.mock.calls).toEqual([["/todos/todo_finish/accept", { method: "POST", json: { asked_at: ASKED } }]]);
    expect(buttons[1].disabled).toBe(true);
  });

  it("has no Accept button for a task that has no round to name", () => {
    draw(item(body({ accept: [{ id: "todo_v3", title: "没有轮次", waited_s: 60 }] })));
    expect([...host.querySelectorAll("button")].find((b) => b.textContent === "通过")?.disabled).toBe(true);
  });

  it("draws the week's recommended setup as the hub worded it, and only opens settings", () => {
    draw(item(body({ setup: [
      { id: "triage_wake", setting: "agents.triage.wake", count: 3, text: "Session triage, \"Wake on schedule\": 3 times …" },
      { id: "leaders", setting: "assistant.leaders", count: 12, minutes: 31, text: "Leader conversations: 12 of the main chat's rounds (31 min) …" },
    ] })));
    const setup = rows("setup");
    expect(setup.map((r) => r.querySelector("div")?.textContent)).toEqual([
      "Session triage, \"Wake on schedule\": 3 times …",
      "Leader conversations: 12 of the main chat's rounds (31 min) …",
    ]);
    // Only a mechanism with a settings page in this app gets a button, and the button calls nothing on the hub.
    expect(setup.map((r) => r.querySelectorAll("button").length)).toEqual([1, 0]);
    act(() => setup[0].querySelector("button")!.click());
    expect(api).not.toHaveBeenCalled();
    expect(location.search).toBe("?tab=persona");
  });

  it("has no setup section on a briefing without the block", () => {
    draw(item(body()));
    expect(rows("setup")).toEqual([]);
  });
});
