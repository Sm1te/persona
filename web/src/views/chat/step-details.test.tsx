// @vitest-environment jsdom
// Opened steps and user bubbles the web drew short of the apps (#2990): a Codex patch's diff (G001), "Show full text" for a text
// the hub cut (G002), plans and questions instead of raw JSON (G017), and the photos sent with a message (G026).
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Item } from "../../lib/types";
import type { ToolStep } from "../../lib/work-groups";

const full = vi.hoisted(() => ({ calls: [] as string[], fail: false }));
vi.mock("../../lib/api", async (original) => ({
  ...(await original<typeof import("../../lib/api")>()),
  fullItem: (_mid: string, _sid: string, id: string) => {
    full.calls.push(id);
    return full.fail ? Promise.reject(new Error("offline")) : Promise.resolve({ id, text: "全部的输出\n第二行" });
  },
}));
const panels = vi.hoisted(() => ({ opened: [] as unknown[] }));
vi.mock("../../lib/panel", async (original) => ({
  ...(await original<typeof import("../../lib/panel")>()),
  openPanel: (p: unknown) => panels.opened.push(p),
}));
import { ActionRow } from "./ActionRow";
import { forgetExpanded } from "./expanded";
import { Answer, UserBubble } from "./SegmentView";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.ResizeObserver ??= class {
  observe() {}
  unobserve() {}
  disconnect() {}
} as unknown as typeof ResizeObserver;

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 4; i++) await act(async () => { await Promise.resolve(); });
};
const button = (pred: (b: HTMLButtonElement) => boolean) => [...document.querySelectorAll<HTMLButtonElement>("button")].find(pred)!;

const use = (name: string, input: Record<string, unknown>): Item => ({ id: "t1#0", seq: 1, role: "assistant", kind: "tool_use", tool_name: name, tool_use_id: "c1", tool_input: input });
const result = (text: string, extra: Partial<Item> = {}): Item => ({ id: "t1-r#0", seq: 2, role: "user", kind: "tool_result", tool_use_id: "c1", text, ...extra });
const step = (u: Item, r?: Item): ToolStep => ({ kind: "tool", id: u.id, use: u, result: r, images: [] });
const render = (s: ToolStep) => act(() => root.render(<ActionRow step={s} mid="m" sid="s" />));
const openRow = () => act(() => host.querySelector<HTMLButtonElement>("[data-action-row] > button")!.click());

beforeEach(() => {
  forgetExpanded(); // every row here has the same id
  full.calls = [];
  full.fail = false;
  panels.opened = [];
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  document.body.innerHTML = "";
});

describe("a Codex file change (G001)", () => {
  it("counts its patch's lines and opens it as a diff", () => {
    render(step(use("Edit", { file_path: "/w/src/a.ts", change: "update", diff: "@@ -1,2 +1,3 @@\n keep\n-old\n+new\n+more" }), result("update /w/src/a.ts")));
    expect(host.textContent).toContain("+2");
    expect(host.textContent).toContain("-1");
    openRow();
    const lines = [...document.querySelectorAll("[data-diff-line]")].map((l) => l.getAttribute("data-diff-line"));
    expect(lines).toEqual(["same", "del", "add", "add"]);
  });

  it("opens the file when the patch is empty (an empty new file, a pure move)", () => {
    render(step(use("Edit", { file_path: "/w/src/empty.ts", change: "add", diff: "" }), result("add /w/src/empty.ts")));
    expect(host.textContent).not.toContain("+0");
    openRow();
    expect(document.querySelector("[data-diff-line]")).toBeNull();
    expect(panels.opened).toEqual([expect.objectContaining({ kind: "file", mid: "m", path: "/w/src/empty.ts" })]);
  });
});

describe("a cut output (G002)", () => {
  const cut = result("开头的输出\n…（已截断，点开看全文，共 90000 字节）", { full_size: 90_000 });

  it("offers 看全文 without the hub's note, and shows the whole once loaded", async () => {
    render(step(use("Bash", { command: "cat big.log" }), cut));
    openRow();
    expect(host.textContent).toContain("开头的输出");
    expect(host.textContent).not.toContain("已截断，点开看全文");
    const more = host.querySelector<HTMLButtonElement>("[data-full-text]")!;
    expect(more.textContent).toMatch(/^已截断，看全文（\d/);
    act(() => more.click());
    await flush();
    expect(full.calls).toEqual(["t1-r#0"]);
    expect(host.textContent).toContain("全部的输出\n第二行");
    expect(host.querySelector("[data-full-text]")).toBeNull();
  });

  it("says when loading failed, and tries again", async () => {
    full.fail = true;
    render(step(use("Bash", { command: "cat big.log" }), cut));
    openRow();
    act(() => host.querySelector<HTMLButtonElement>("[data-full-text]")!.click());
    await flush();
    const more = host.querySelector<HTMLButtonElement>("[data-full-text]")!;
    expect(more.getAttribute("data-full-text")).toBe("failed");
    expect(more.textContent).toBe("加载失败，点一下重试");
    full.fail = false;
    act(() => more.click());
    await flush();
    expect(full.calls).toHaveLength(2);
    expect(host.textContent).toContain("全部的输出");
  });
});

describe("a cut reply (G002)", () => {
  it("offers 看全文 under a long answer and swaps the whole text in", async () => {
    const item: Item = { id: "a1#0", seq: 3, role: "assistant", kind: "text", text: "开头\n…（已截断，共 90000 字节）", full_size: 90_000 };
    act(() => root.render(<Answer item={item} mid="m" sid="s" />));
    expect(host.textContent).toContain("开头");
    expect(host.textContent).not.toContain("共 90000 字节）");
    act(() => host.querySelector<HTMLButtonElement>("[data-full-text]")!.click());
    await flush();
    expect(full.calls).toEqual(["a1#0"]);
    expect(host.textContent).toContain("全部的输出");
    expect(host.querySelector("[data-full-text]")).toBeNull();
  });
});

describe("plans and questions (G017)", () => {
  it("draws a plan as Markdown, not JSON", () => {
    render(step(use("ExitPlanMode", { plan: "## 计划\n\n- 第一步" }), result("User has approved your plan.")));
    openRow();
    const detail = host.querySelector("[data-step-detail=plan]")!;
    expect(detail.querySelector("h2")?.textContent).toBe("计划");
    expect(host.textContent).not.toContain('"plan"');
  });

  it("lists AskUserQuestion's questions and options, the answer under them", () => {
    const input = { questions: [{ question: "用哪个？", header: "方案", options: [{ label: "A", description: "快" }, { label: "B" }] }] };
    render(step(use("AskUserQuestion", input), result('User has answered your questions: "用哪个？"="A".')));
    openRow();
    const detail = host.querySelector("[data-step-detail=questions]")!;
    expect(detail.textContent).toContain("方案");
    expect(detail.textContent).toContain("用哪个？");
    expect(detail.textContent).toContain("A：快");
    expect(host.textContent).toContain('"用哪个？"="A"');
    expect(host.textContent).not.toContain('"questions"');
  });

  it("ticks what was picked in the assistant's ask_user, without its result text", () => {
    const input = { questions: [{ question: "要哪些？", multiSelect: true, options: [{ label: "x" }, { label: "y" }] }, { question: "叫什么？", options: [{ label: "甲" }] }] };
    render(step(use("mcp__persona__ask_user", input), result("The user answered:\n- 要哪些？ → x\n- 叫什么？ → 乙\n")));
    openRow();
    const detail = host.querySelector("[data-step-detail=ask-user]")!;
    const picked = [...detail.querySelectorAll("[data-picked]")].map((e) => e.textContent);
    expect(picked).toEqual(["已选：x", "已选：乙"]);
    expect(detail.textContent).toContain("可多选");
    expect(host.textContent).not.toContain("The user answered");
  });

  it("still ticks the answer when the question or option has edge whitespace", () => {
    const input = { questions: [{ question: "继续吗？ ", options: [{ label: " 是" }, { label: "否" }] }] };
    render(step(use("mcp__persona__ask_user", input), result("The user answered:\n- 继续吗？  →  是\n")));
    openRow();
    const picked = [...host.querySelectorAll("[data-step-detail=ask-user] [data-picked]")].map((e) => e.textContent);
    expect(picked).toEqual(["已选：是"]);
    expect(host.textContent).not.toContain("没有回答");
  });

  it("says so when nobody answered", () => {
    const input = { questions: [{ question: "继续吗？", options: [{ label: "是" }] }] };
    render(step(use("mcp__persona__ask_user", input), result("The user did not answer (dismissed or timed out).")));
    openRow();
    expect(host.querySelector("[data-step-detail=ask-user]")!.textContent).toContain("没有回答");
    expect(host.querySelector("[data-picked]")).toBeNull();
  });
});

describe("photos sent with a message (G026)", () => {
  it("draws them over the bubble and opens them in the viewer", () => {
    act(() => root.render(<UserBubble msg={{ id: "u1", text: "看图", images: [], contexts: [], photos: ["/tmp/persona/a.jpg", "/tmp/persona/b.png"] }} mid="m" sid="s" />));
    const thumbs = host.querySelectorAll<HTMLImageElement>("[data-attached-photos] img");
    expect(thumbs).toHaveLength(2);
    expect(thumbs[0].getAttribute("src")).toContain("/machines/m/file/thumb?path=%2Ftmp%2Fpersona%2Fa.jpg");
    act(() => button((b) => b.getAttribute("aria-label")?.startsWith("图片 2") ?? false).click());
    const viewer = document.querySelector<HTMLElement>('[aria-label="图片 2 / 2"]');
    expect(viewer).not.toBeNull();
    expect(viewer!.querySelector("img")?.getAttribute("src")).toContain("/machines/m/file/download?path=%2Ftmp%2Fpersona%2Fb.png");
  });

  it("falls back from the thumbnail to the file, then to a glyph", () => {
    act(() => root.render(<UserBubble msg={{ id: "u1", text: "", images: [], contexts: [], photos: ["/tmp/persona/a.jpg"] }} mid="m" sid="s" />));
    const img = () => host.querySelector<HTMLImageElement>("[data-attached-photos] img");
    expect(img()!.getAttribute("src")).toContain("/file/thumb?");
    act(() => { img()!.dispatchEvent(new Event("error")); });
    expect(img()!.getAttribute("src")).toContain("/file/download?");
    act(() => { img()!.dispatchEvent(new Event("error")); });
    expect(img()).toBeNull();
    expect(host.querySelector("[data-attached-photos] button svg")).not.toBeNull();
  });
});
