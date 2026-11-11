import { renderToStaticMarkup } from "react-dom/server";
import { beforeAll, beforeEach, describe, expect, it } from "vitest";
import { CardConversation, CardItem, LiveCard, VIEWS_FEATURE } from "./AssistantCard";
import { applyAction, applyBlock, applyView, resetCards, updateDraft } from "../lib/assistant-views";
import { getState } from "../lib/store";
import { Markdown } from "./Markdown";
import { UserBubble } from "../views/chat/SegmentView";
import type { AssistantView } from "../lib/extension-wire";
import { loadPages } from "../lib/lazy-page";

// The cards draw the renderer through a lazy chunk (#3614); these tests draw without waiting for it.
beforeAll(() => loadPages(), 60_000);

const EXAMPLES = import.meta.glob("../../../schema/extension/examples/assistant/*.json", { query: "?raw", import: "default", eager: true }) as Record<string, string>;
const example = (name: string) => JSON.parse(Object.entries(EXAMPLES).find(([p]) => p.endsWith(`/${name}.json`))![1]);

const decision = example("decision");
const form = example("form");
const fence = (v: unknown) => "```persona-view\n" + JSON.stringify(v) + "\n```\n";

function draw(text: string, o: { conversation?: string; item?: string } = {}) {
  const item = o.item === undefined ? "item-1" : o.item;
  return renderToStaticMarkup(
    <CardConversation.Provider value={o.conversation ?? "c1"}>
      <CardItem.Provider value={item ? { id: item, text } : null}>
        <Markdown text={text} />
      </CardItem.Provider>
    </CardConversation.Provider>,
  );
}

const enabledButtons = (html: string) => [...html.matchAll(/<button(?![^>]*disabled)[^>]*>/g)].length;

beforeEach(() => {
  resetCards("hub-a");
  Object.assign(getState(), { features: [VIEWS_FEATURE] });
});

describe("正文卡片", () => {
  it("draws the card with its actions, but only answers once the hub registered the block", () => {
    const text = "结论\n" + fence(decision);
    const before = draw(text);
    expect(before).toContain('data-card-key="item-1/0"');
    expect(before).toContain("回复完成并经 hub 确认后可以回答");
    applyBlock("c1", "item-1/0");
    const after = draw(text);
    expect(after).not.toContain("回复完成并经 hub 确认后可以回答");
    expect(enabledButtons(after)).toBeGreaterThan(enabledButtons(before));
  });

  it("a hub without assistant_views takes no answer", () => {
    Object.assign(getState(), { features: ["lark"] });
    applyBlock("c1", "item-1/0");
    expect(draw(fence(decision))).toContain("这个 hub 还不支持在卡片上回答");
  });

  it("an agent's chat (no assistant conversation) draws the card, nothing answers", () => {
    const html = draw(fence(decision), { conversation: "" });
    expect(html).toContain("data-assistant-card=\"persona-view\"");
    expect(html).not.toContain("data-card-key");
  });

  it("keys follow the shared numbering: the bad block before it counts", () => {
    const text = "```persona-view\ninvalid\n```\n" + fence(decision);
    expect(draw(text)).toContain('data-card-key="item-1/1"');
  });

  it("past the third block, and a block never closed, show the fallback only", () => {
    const many = [0, 1, 2, 3].map((i) => fence({ ...decision, fallback: `第${i}张` })).join("");
    const html = draw(many);
    expect(html).toContain('data-card-key="item-1/2"');
    expect(html).not.toContain('data-card-key="item-1/3"');
    expect(html).toContain("第3张");
  });

  it("an answered card marks the choice and shows the hub's values, locked", () => {
    const text = fence(form);
    applyBlock("c1", "item-1/0");
    applyAction("c1", { key: "item-1/0", action: "submit", values: { task: "整理笔记", priority: "high", notify: "false" }, client_msg_id: "x", status: "sent", revision: 2, at: "2026-11-05T00:00:00Z" });
    const html = draw(text);
    expect(html).toContain('value="整理笔记"');
    expect(html).toContain("已发送");
    expect(html).toMatch(/<input[^>]*disabled/);
  });

  it("a card with html shows its fallback in the assistant's frame; nothing runs", () => {
    const html = draw(fence(example("html")));
    expect(html).toContain('data-assistant-card="html-fallback"');
    expect(html).toContain("助手画的页面");
    expect(html).toContain(example("html").fallback);
    expect(html).not.toMatch(/<iframe|<script|onclick=/i);
  });

  it("a card with html runs it where the hub has the bridge and the card is kept: no fallback, the card's body instead", () => {
    applyBlock("c1", "m-html/0");
    Object.assign(getState(), { features: [VIEWS_FEATURE, "assistant_html_bridge"] });
    const text = fence(example("html"));
    const html = draw(text, { item: "m-html" });
    expect(html).not.toContain('data-assistant-card="html-fallback"');
    expect(html).toContain('data-assistant-card="persona-view"');
    // without the bridge, or outside an assistant conversation (an agent's chat: no card to keep), its words as before
    Object.assign(getState(), { features: [VIEWS_FEATURE] });
    expect(draw(text, { item: "m-html" })).toContain('data-assistant-card="html-fallback"');
    Object.assign(getState(), { features: [VIEWS_FEATURE, "assistant_html_bridge"] });
    expect(draw(text, { conversation: "", item: "m-html" })).toContain('data-assistant-card="html-fallback"');
  });

  it("a card with a component the web does not draw shows its fallback, not an empty or partial card", () => {
    const future = { title: "T", fallback: "未来组件的文字", sections: [{ items: [{ type: "metric", label: "a", value: "1" }, { type: "timeline" }] }] };
    const html = draw(fence(future));
    expect(html).toContain('data-assistant-card="fallback"');
    expect(html).toContain("未来组件的文字");
    expect(html).not.toContain('data-assistant-card="persona-view"');
    // the same in a named page and in a one-component card; an html component keeps its own frame
    const paged = { ...decision, pages: { more: { sections: [{ items: [{ type: "timeline" }] }] } } };
    expect(draw(fence(paged))).toContain('data-assistant-card="fallback"');
    expect(draw(fence({ type: "timeline", fallback: "单个未来组件" }))).toContain("单个未来组件");
    expect(draw(fence(example("html")))).toContain('data-assistant-card="html-fallback"');
  });

  it("a card without the exact fallback stays code", () => {
    const html = draw(fence({ Fallback: "x", sections: [] }));
    expect(html).not.toContain("data-assistant-card");
  });
});

describe("活卡片", () => {
  const live = (version: number, title: string): AssistantView => ({
    id: "view_1", conversation: "c1", version, created_at: "", updated_at: "",
    view: { ...form, title } as unknown as AssistantView["view"],
  });

  it("draws the newest version in place and keeps what was typed", () => {
    updateDraft("c1", "view_1", (d) => ({ ...d, values: { task: "我在写" } }));
    applyView("c1", live(2, "第二版"));
    const html = renderToStaticMarkup(<LiveCard conversation="c1" id="view_1" first={live(1, "第一版")} />);
    expect(html).toContain("第二版");
    expect(html).toContain('data-card-version="2"');
    expect(html).toContain('value="我在写"');
    // an older event never brings the old one back
    applyView("c1", live(1, "第一版"));
    expect(renderToStaticMarkup(<LiveCard conversation="c1" id="view_1" first={live(1, "第一版")} />)).toContain("第二版");
  });

  it("a version with a component the web does not draw shows its fallback", () => {
    const future = { ...decision, fallback: "新版本的文字", sections: [{ items: [{ type: "timeline" }] }] };
    applyView("c1", { id: "view_3", conversation: "c1", version: 2, created_at: "", updated_at: "", view: future as unknown as AssistantView["view"] });
    const html = renderToStaticMarkup(<LiveCard conversation="c1" id="view_3" />);
    expect(html).toContain('data-assistant-card="fallback"');
    expect(html).toContain("新版本的文字");
  });

  it("a page taken away by an update leaves the stack at what still reads", () => {
    const paged = { ...decision, pages: { detail: { title: "详情", sections: [{ items: [{ type: "text", text: "细节" }] }] } } };
    applyView("c1", { id: "view_2", conversation: "c1", version: 1, created_at: "", updated_at: "", view: paged as unknown as AssistantView["view"] });
    updateDraft("c1", "view_2", (d) => ({ ...d, stack: ["detail"] }));
    expect(renderToStaticMarkup(<LiveCard conversation="c1" id="view_2" />)).toContain("细节");
    applyView("c1", { id: "view_2", conversation: "c1", version: 2, created_at: "", updated_at: "", view: decision as unknown as AssistantView["view"] });
    const html = renderToStaticMarkup(<LiveCard conversation="c1" id="view_2" />);
    expect(html).not.toContain("细节");
    expect(html).toContain(decision.title);
  });
});

describe("来自卡片的消息", () => {
  const msg = { text: '选 B：队列加去重\n<persona-action view="view_1" action="b">{"values":{}}</persona-action>', images: [], contexts: [], ts: "2026-11-05T00:00:00Z" };

  it("shows the reply without the tag, marked 来自卡片, in an assistant conversation", () => {
    const html = renderToStaticMarkup(
      <CardConversation.Provider value="c1">
        <UserBubble msg={msg as never} mid="m" sid="c1" pending />
      </CardConversation.Provider>,
    );
    expect(html).toContain("来自卡片");
    expect(html).toContain("选 B：队列加去重");
    expect(html).not.toContain("persona-action");
  });

  it("elsewhere it is the message as written", () => {
    const html = renderToStaticMarkup(<UserBubble msg={msg as never} mid="m" sid="s" pending />);
    expect(html).not.toContain("来自卡片");
    expect(html).toContain("persona-action");
  });
});

describe("persona-card 照旧", () => {
  it("still draws, nothing acts", () => {
    const html = draw("```persona-card\n" + JSON.stringify({ type: "stat", title: "步数", value: 8432 }) + "\n```\n");
    expect(html).toContain('data-assistant-card="persona-card"');
    expect(html).toContain("8,432");
  });
});

describe("HTML 的回答", () => {
  it("an html reply locks the card's replies without marking one of its actions", () => {
    applyBlock("c1", "item-1/0");
    applyAction("c1", { key: "item-1/0", kind: "html", action: "guide", text: "继续", values: {}, client_msg_id: "h", status: "sent", revision: 1, at: "2026-11-05T00:00:00Z" });
    const html = draw(fence(decision));
    expect(html).toContain("已发送");
    expect(html).not.toContain("bg-accent-tint");
    expect(enabledButtons(html)).toBe(0);
  });
});
