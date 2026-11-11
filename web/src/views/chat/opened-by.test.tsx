import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import type { ExternalOrigin } from "../../lib/conversation-origins";
import { emptyTaskIndex } from "../../lib/tasks";
import { segments } from "../../lib/turns";
import type { Item } from "../../lib/types";
import { NO_WORKFLOW_CALLS } from "../../lib/workflows";
import { OriginMark } from "../../ui/ChannelMark";
import { ChatContext } from "./chat-context";
import { SegmentView } from "./SegmentView";

// WeChat is a channel here, so a message whose client_msg_id starts with `wechat-` came in from it.
vi.mock("../../lib/channels", async (original) => ({ ...(await original<typeof import("../../lib/channels")>()), useChannels: () => [{ id: "wechat", name: "微信", kind: "im" }] }));

// An external conversation's first message says where it came from (#3758), as a channel's message does (tests run in zh-Hans).
const u = (id: string, text: string, extra: Partial<Item> = {}): Item => ({ id, seq: 1, role: "user", kind: "text", text, ...extra });

const render = (item: Item, opening?: { id: string; origin: ExternalOrigin }) =>
  renderToStaticMarkup(
    <ChatContext.Provider value={{ tasks: emptyTaskIndex(), subagents: [], scope: "", workflowCalls: NO_WORKFLOW_CALLS, opening }}>
      <SegmentView seg={segments([item])[0]} live={false} mid="m" sid="s" expandFold={async () => {}} />
    </ChatContext.Provider>,
  );

describe("the message an extension opened a conversation with", () => {
  it("says Lark /agent or an extension under the opening message only", () => {
    const lark = render(u("u1#0", "把群里这周提的需求汇总一下"), { id: "u1#0", origin: "lark_agent" });
    expect(lark).toContain('data-opened-by="lark_agent"');
    expect(lark).toContain("来自 飞书 /agent");
    const ext = render(u("u1#0", "整理成周报草稿"), { id: "u1#0", origin: "extension" });
    expect(ext).toContain('data-opened-by="extension"');
    expect(ext).toContain("来自扩展");
    // a later message, and a conversation nobody's extension opened
    expect(render(u("u2#0", "再补一条"), { id: "u1#0", origin: "lark_agent" })).not.toContain("data-opened-by");
    expect(render(u("u1#0", "你好"))).not.toContain("data-opened-by");
  });

  it("leaves the line to the message's own channel when it has one", () => {
    const html = render(u("u1#0", "你好", { client_msg_id: "wechat-1" }), { id: "u1#0", origin: "extension" });
    expect(html).toContain("来自 微信");
    expect(html).not.toContain("data-opened-by");
  });

  it("names its mark on a row and hides it beside words that say it", () => {
    const row = renderToStaticMarkup(<OriginMark origin="lark_agent" size={16} />);
    expect(row).toContain('role="img"');
    expect(row).toContain('aria-label="来自 飞书 /agent"');
    const line = renderToStaticMarkup(<OriginMark origin="extension" size={14} decorative />);
    expect(line).toContain('aria-hidden="true"');
    expect(line).not.toContain("aria-label");
  });
});
