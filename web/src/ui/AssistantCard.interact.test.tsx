// @vitest-environment jsdom
// Pressing a card's actions (docs/assistant/assistant-views.md "Persistent submission"): a form is submitted once with the values typed, a
// confirm asks natively first, a card that changed under the submit (409 view_changed) keeps the draft and asks again at
// the new version, a live update redraws in place without taking the field being typed in, and push / pop move within
// the card. The hub is a fake one (fetch), the data made up.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
import { AssistantCard, CardConversation, CardItem, LiveCard, VIEWS_FEATURE } from "./AssistantCard";
import { applyBlock, applyView, conversationCards, getDraft, resetCards } from "../lib/assistant-views";
import { getState } from "../lib/store";
import type { AssistantView } from "../lib/extension-wire";
import { loadPages } from "../lib/lazy-page";

// The cards draw the renderer through a lazy chunk (#3614); these tests draw without waiting for it.
beforeAll(() => loadPages(), 60_000);

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const EXAMPLES = import.meta.glob("../../../schema/extension/examples/assistant/*.json", { query: "?raw", import: "default", eager: true }) as Record<string, string>;
const example = (name: string) => JSON.parse(Object.entries(EXAMPLES).find(([p]) => p.endsWith(`/${name}.json`))![1]);
const form = example("form");

type Reply = { status: number; body: unknown };
let replies: Reply[] = [];
let calls: { path: string; body: Record<string, unknown> }[] = [];
const json = (v: unknown, status = 200) => new Response(JSON.stringify(v), { status, headers: { "Content-Type": "application/json" } });

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 5; i++) await act(async () => { await Promise.resolve(); });
};
const button = (label: string, within: ParentNode = document) =>
  [...within.querySelectorAll("button")].find((b) => b.textContent?.trim() === label) as HTMLButtonElement | undefined;
const click = async (el: Element | undefined) => {
  expect(el, "element").toBeTruthy();
  await act(async () => (el as HTMLElement).click());
  await flush();
};
function type(input: HTMLInputElement, value: string) {
  const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!;
  act(() => {
    set.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

beforeEach(() => {
  resetCards("hub-a");
  Object.assign(getState(), { features: [VIEWS_FEATURE] });
  replies = [];
  calls = [];
  vi.stubGlobal("localStorage", { getItem: () => "t", setItem: () => {}, removeItem: () => {} });
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    calls.push({ path: String(input), body: JSON.parse(String(init?.body ?? "{}")) });
    const r = replies.shift() ?? { status: 500, body: { error: "unexpected" } };
    return json(r.body, r.status);
  }));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
});

const inline = (view: unknown, item = "item-9") => {
  const code = JSON.stringify(view);
  act(() =>
    root.render(
      <CardConversation.Provider value="c1">
        <CardItem.Provider value={{ id: item, text: "```persona-view\n" + code + "\n```\n" }}>
          <AssistantCard lang="persona-view" code={code} fallback={<pre />} />
        </CardItem.Provider>
      </CardConversation.Provider>,
    ),
  );
};
const record = (key: string, cid: string, values: Record<string, string> = {}) => ({ action: { key, action: "submit", values, client_msg_id: cid, status: "pending", revision: 1, at: "2026-11-05T00:00:00Z" } });

describe("表单", () => {
  it("submits once, with what was typed, at version 1; then it is locked", async () => {
    applyBlock("c1", "item-9/0");
    inline(form);
    type(host.querySelector<HTMLInputElement>("input#extension-ui-task")!, "整理读书笔记");
    replies.push({ status: 200, body: record("item-9/0", "x", { task: "整理读书笔记", priority: "normal", notify: "true" }) });
    await click(button("提交", host));
    expect(calls).toHaveLength(1);
    expect(calls[0].path).toBe("/api/v1/assistant/conversations/c1/views/item-9%2F0/actions");
    expect(calls[0].body).toMatchObject({ action: "submit", values: { task: "整理读书笔记", priority: "normal", notify: "true" }, expected_version: 1 });
    expect(calls[0].body).not.toHaveProperty("confirmed");
    expect(button("提交", host)?.disabled).toBe(true);
    await click(button("提交", host));
    expect(calls).toHaveLength(1);
  });

  it("a confirm asks natively with the full reply first; cancel sends nothing", async () => {
    const confirmCard = { ...form, sections: [{ items: [...form.sections[0].items.slice(0, -1), { type: "button", title: "提交", action: { id: "submit", reply: "按这些信息安排任务", submit: true, confirm: "确定安排？" } }] }] };
    applyBlock("c1", "item-9/0");
    inline(confirmCard);
    await click(button("提交", host));
    expect(document.body.textContent).toContain("确定安排？");
    expect(document.body.textContent).toContain("按这些信息安排任务");
    await click(button("取消"));
    expect(calls).toHaveLength(0);
    await click(button("提交", host));
    replies.push({ status: 200, body: record("item-9/0", "x") });
    await click(button("发送"));
    expect(calls).toHaveLength(1);
    expect(calls[0].body).toMatchObject({ confirmed: true, expected_version: 1 });
  });
});

describe("活卡片的版本", () => {
  const live = (version: number, title: string): AssistantView => ({ id: "view_7", conversation: "c1", version, created_at: "", updated_at: "", view: { ...form, title } as unknown as AssistantView["view"] });
  const draw = () => act(() => root.render(<CardConversation.Provider value="c1"><LiveCard conversation="c1" id="view_7" /></CardConversation.Provider>));

  it("409 view_changed: the draft stays, the new version shows, the next submit asks again at it", async () => {
    applyView("c1", live(3, "第三版"));
    draw();
    type(host.querySelector<HTMLInputElement>("input#extension-ui-task")!, "草稿内容");
    replies.push({ status: 409, body: { error: "card changed", code: "view_changed", version: 4, view: live(4, "第四版") } });
    await click(button("提交", host));
    expect(calls).toHaveLength(1);
    expect(calls[0].body.expected_version).toBe(3);
    expect(host.textContent).toContain("第四版");
    expect(host.querySelector<HTMLInputElement>("input#extension-ui-task")!.value).toBe("草稿内容");
    expect(host.textContent).toContain("卡片已更新，请检查后重新确认");
    // not sent again by itself
    await flush();
    expect(calls).toHaveLength(1);
    await click(button("提交", host));
    expect(document.body.textContent).toContain("卡片刚更新过");
    replies.push({ status: 200, body: record("view_7", "y") });
    await click(button("发送"));
    expect(calls).toHaveLength(2);
    expect(calls[1].body).toMatchObject({ expected_version: 4, confirmed: true });
    expect(calls[1].body.client_msg_id).not.toBe(calls[0].body.client_msg_id);
  });

  it("a live update redraws in place: the field being typed in keeps its text and focus", async () => {
    applyView("c1", live(1, "第一版"));
    draw();
    const input = host.querySelector<HTMLInputElement>("input#extension-ui-task")!;
    input.focus();
    type(input, "正在输入");
    act(() => applyView("c1", live(2, "第二版")));
    expect(host.textContent).toContain("第二版");
    const now = host.querySelector<HTMLInputElement>("input#extension-ui-task")!;
    expect(now).toBe(input);
    expect(document.activeElement).toBe(input);
    expect(now.value).toBe("正在输入");
  });

  it("a network failure keeps the request for 重试 with the same client_msg_id", async () => {
    applyView("c1", live(1, "第一版"));
    draw();
    replies.push({ status: 503, body: { error: "hub restarting" } });
    await click(button("提交", host));
    expect(host.textContent).toContain("hub restarting");
    replies.push({ status: 200, body: record("view_7", String(calls[0].body.client_msg_id)) });
    await click(button("重试", host));
    expect(calls).toHaveLength(2);
    expect(calls[1].body.client_msg_id).toBe(calls[0].body.client_msg_id);
    expect(conversationCards("c1").actions.view_7).toBeTruthy();
    expect(getDraft("c1", "view_7")?.sending).toBeUndefined();
  });
});

describe("多页", () => {
  it("push opens a named page, back returns; nothing is sent", async () => {
    const paged = {
      title: "方案", fallback: "fb",
      sections: [{ items: [{ type: "row", title: "看细节", action: { id: "more", push: { page: "detail" } } }] }],
      pages: { detail: { title: "细节页", sections: [{ items: [{ type: "text", text: "这里是细节" }, { type: "button", title: "回去", action: { id: "up", pop: true } }] }] } },
    };
    inline(paged);
    await click([...host.querySelectorAll("button")].find((b) => b.textContent?.includes("看细节")));
    expect(host.textContent).toContain("这里是细节");
    await click(button("回去", host));
    expect(host.textContent).toContain("看细节");
    expect(host.textContent).not.toContain("这里是细节");
    expect(calls).toHaveLength(0);
  });
});

describe("html 卡片", () => {
  it("shows the fallback and nothing of the page: no frame, no script, no request", async () => {
    const html = example("html");
    applyBlock("c1", "item-9/0");
    inline(html);
    await flush();
    expect(host.textContent).toContain(html.fallback);
    expect(host.querySelector("iframe, script, object, embed")).toBeNull();
    expect(host.innerHTML).not.toContain("persona.reply");
    expect(calls).toHaveLength(0);
  });
});
