// @vitest-environment jsdom
// A review the background supervisor made (#5082) is one line in the main chat: who, the task, the verdict. The main
// chat's own reviews stay out of the simplified chat as before, and so does a reviewer or a kind this page doesn't know.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const hub = vi.hoisted(() => ({ todos: [] as { id: string; title: string }[], opened: [] as string[] }));
vi.mock("../../lib/store", async (original) => {
  const real = await original<typeof import("../../lib/store")>();
  return { ...real, useStore: <T,>(select: (s: ReturnType<typeof real.getState>) => T): T => select({ ...real.getState(), todos: hub.todos } as ReturnType<typeof real.getState>) };
});
vi.mock("../../lib/handoff", async (original) => ({ ...(await original<typeof import("../../lib/handoff")>()), openTask: (id: string) => void hub.opened.push(id) }));
import type { FeedItem } from "../../lib/feed";
import { FeedLineView } from "./Tasks";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;

let host: HTMLDivElement;
let root: Root;
beforeEach(() => {
  hub.todos = [{ id: "todo_1", title: "整理发布说明" }];
  hub.opened = [];
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

const review = (body: Record<string, unknown>, kind = "review"): FeedItem =>
  ({ id: "f1", conversation: "main", at: "2026-11-12T08:00:00Z", kind, ref: "todo_1", body }) as FeedItem;
const line = () => host.querySelector('[data-testid="review-line"]');

describe("the main chat's line for a background supervisor's review (#5082)", () => {
  it("says who reviewed, the task and the verdict, and opens the task", () => {
    act(() => root.render(<FeedLineView simplified item={review({ title: "整理发布说明", verdict: "accepted", note: "7 个 PR 都在", by: "background" })} />));
    expect(line()?.querySelector('[data-testid="review-by"]')?.textContent).toBe("后台督办");
    expect(line()?.textContent).toContain("整理发布说明");
    expect(line()?.textContent).toContain("已验收");
    expect(line()?.textContent).toContain("7 个 PR 都在");
    act(() => [...line()!.querySelectorAll("button")].find((b) => b.textContent === "整理发布说明")!.click());
    expect(hub.opened).toEqual(["todo_1"]);
  });

  it("keeps the title the entry was written with for a task the page no longer lists, with nothing to open", () => {
    hub.todos = [];
    act(() => root.render(<FeedLineView simplified item={review({ title: "修小组件横屏崩溃", verdict: "rework", round: 2, by: "background" })} />));
    expect(line()?.textContent).toContain("修小组件横屏崩溃");
    expect(line()?.textContent).toContain("返工第 2 轮");
    expect([...line()!.querySelectorAll("button")].some((b) => b.textContent === "修小组件横屏崩溃")).toBe(false);
  });

  it("draws it in the full feed too, and the main chat's own review there without a reviewer", () => {
    act(() => root.render(<FeedLineView item={review({ verdict: "accepted", by: "background" })} />));
    expect(line()?.querySelector('[data-testid="review-by"]')).not.toBeNull();
    act(() => root.render(<FeedLineView item={review({ verdict: "accepted" })} />));
    expect(line()).not.toBeNull();
    expect(line()?.querySelector('[data-testid="review-by"]')).toBeNull();
  });

  it("still leaves the main chat's own reviews, an unknown reviewer and an unknown kind out of the simplified chat", () => {
    act(() => root.render(<FeedLineView simplified item={review({ verdict: "accepted" })} />));
    expect(host.innerHTML).toBe("");
    act(() => root.render(<FeedLineView simplified item={review({ verdict: "accepted", by: "someone_newer" })} />));
    expect(host.innerHTML).toBe("");
    act(() => root.render(<FeedLineView simplified item={review({ verdict: "accepted", by: "background" }, "something_newer")} />));
    expect(host.innerHTML).toBe("");
  });
});
