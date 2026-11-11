import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import type { FeedItem } from "../../lib/feed";
import { ChatActionsContext, type ChatActions } from "../chat/chat-actions";
import { FeedLineView } from "./Tasks";

const chat: ChatActions = { draftKey: "m/s", canCompose: true, speaker: "Persona", title: "Persona", images: () => [] };
const receipt: FeedItem = {
  id: "f1",
  conversation: "s",
  at: "2026-11-05T08:45:00Z",
  kind: "receipt",
  ref: "todo_7f3a",
  body: { title: "补齐网络层的单元测试", summary: "测试补好了，覆盖率 92%。", pr: "https://github.com/o/r/pull/53" },
};

function render(actions: ChatActions | null, item = receipt) {
  return renderToStaticMarkup(
    <ChatActionsContext.Provider value={actions}>
      <FeedLineView item={item} />
    </ChatActionsContext.Provider>,
  );
}

// a task's card offers "Copy" / "Quote" like a reply (#2462)
describe("a task's receipt card", () => {
  it("has 复制 and 引用 in its corner bar, the card as it was", () => {
    const html = render(chat);
    expect(html).toContain('aria-label="拷贝回复"');
    expect(html).toContain('aria-label="引用"');
    expect(html).toMatch(/absolute top-2 right-2/);
    expect(html).toContain("补齐网络层的单元测试 完成");
    expect(html).toContain("PR #53");
    expect(html).toContain("看看");
  });

  it("offers no 引用 where nothing can be sent", () => {
    const html = render({ ...chat, canCompose: false });
    expect(html).not.toContain('aria-label="引用"');
    expect(html).toContain("补齐网络层的单元测试 完成");
  });
});
