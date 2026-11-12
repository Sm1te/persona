// @vitest-environment jsdom
// Review is a separate reason for a task to need attention, even when its agent has stopped (#4889).
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { initI18n, t } from "../../i18n";
import type { FeedItem, TodayBody } from "../../lib/feed";
import { FeedLineView } from "./Tasks";

// where a click went: a page of its own (#5096), never a side panel
const opened = () => location.pathname + location.search;

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let root: Root;
let host: HTMLDivElement;
beforeEach(() => {
  history.replaceState(null, "", "/");
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(async () => {
  act(() => root.unmount());
  host.remove();
  history.replaceState(null, "", "/");
  await initI18n("zh-Hans");
});

function draw(body: Partial<TodayBody>) {
  const item: FeedItem = {
    id: "today-review",
    conversation: "main",
    at: "2026-11-07T00:00:00Z",
    kind: "today",
    body: { date: "2026-11-07", ...body },
  };
  act(() => root.render(<FeedLineView item={item} />));
}

const first = { id: "review-first", title: "Update settings" };
const second = { id: "review-second", title: "Write documentation" };

describe("Today tasks awaiting review", () => {
  it.each([
    { lang: "en", single: "Waiting for your review: Update settings", multiple: "2 tasks waiting for your review", action: "View" },
    { lang: "zh-Hans", single: "等你验收：Update settings", multiple: "2 件等你验收", action: "去看" },
    { lang: "ja", single: "検収待ち：Update settings", multiple: "検収待ち2件", action: "確認" },
  ])("names one task, counts several and opens the first in $lang", async ({ lang, single, multiple, action }) => {
    await initI18n(lang);
    draw({ pending_review: [first] });
    expect(host.textContent).toContain(single);
    expect(host.textContent).not.toContain(t("assistant-tasks.today_empty"));
    let button = host.querySelector("button")!;
    expect(button.textContent).toBe(action);
    act(() => button.click());
    expect(opened()).toBe(`/todos?id=${first.id}`);

    history.replaceState(null, "", "/");
    draw({ pending_review: [second, first] });
    expect(host.textContent).toContain(multiple);
    expect(host.textContent).not.toContain(first.title);
    expect(host.textContent).not.toContain(second.title);
    expect(host.textContent).not.toContain(t("assistant-tasks.today_empty"));
    button = host.querySelector("button")!;
    expect(button.textContent).toBe(action);
    act(() => button.click());
    expect(opened()).toBe(`/todos?id=${second.id}`);
  });

  it.each([{ pending_review: undefined }, { pending_review: [] }])("keeps the empty card when pending_review is $pending_review", ({ pending_review }) => {
    draw(pending_review === undefined ? {} : { pending_review });
    expect(host.textContent).toContain(t("assistant-tasks.today_empty"));
    expect(host.querySelector("button")).toBeNull();
  });

  it("keeps the waiting and review actions separate alongside done and working tasks", () => {
    draw({
      done: [{ id: "done", title: "Finished work" }],
      waiting: [{ id: "waiting", title: "Choose a format" }],
      pending_review: [first],
      working: 1,
    });
    expect(host.textContent).toContain("已完成：Finished work");
    expect(host.textContent).toContain("需要你：Choose a format");
    expect(host.textContent).toContain("等你验收：Update settings");
    expect(host.textContent).toContain("1 个任务工作中");
    const buttons = [...host.querySelectorAll("button")].filter((b) => b.textContent === "去看");
    expect(buttons).toHaveLength(2);
    act(() => buttons[0].click());
    expect(opened()).toBe(`/todos?id=${"waiting"}`);
    act(() => buttons[1].click());
    expect(opened()).toBe(`/todos?id=${first.id}`);
  });
});
