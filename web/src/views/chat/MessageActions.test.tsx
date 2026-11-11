import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { ChatActionsContext, liveValue, type ChatActions } from "./chat-actions";
import { MessageActions, moreActions, rewindActions } from "./MessageActions";

const base: ChatActions = { draftKey: "m/s", canCompose: true, speaker: "Persona", title: "Persona", images: () => [] };

function render(actions: ChatActions, itemId?: string) {
  return renderToStaticMarkup(
    <ChatActionsContext.Provider value={actions}>
      <MessageActions text="回复" itemId={itemId}>
        <p>回复</p>
      </MessageActions>
    </ChatActionsContext.Provider>,
  );
}

// "Branch to new chat" (#2137): a reply's bar ends in ⋯ only when the session can fork.
describe("MessageActions ⋯", () => {
  it("shows ⋯ on a reply of a session that can fork", () => {
    expect(render({ ...base, fork: () => {} }, "u1#0")).toContain('aria-label="更多"');
  });
  it("has no ⋯ when the session cannot fork, or the message is not a reply", () => {
    expect(render(base, "u1#0")).not.toContain('aria-label="更多"');
    expect(render({ ...base, fork: () => {} })).not.toContain('aria-label="更多"');
  });
});

describe("moreActions", () => {
  it("branches off at the reply it is on", () => {
    const got: string[] = [];
    const [fork] = moreActions("u1#0", (id) => got.push(id));
    expect(fork?.label).toBe("分支到新聊天");
    fork?.onSelect();
    expect(got).toEqual(["u1#0"]);
  });
  it("offers nothing for a line the node cannot find", () => {
    expect(moreActions("line12#0", () => {})).toEqual([]);
    expect(moreActions("u1", () => {})).toEqual([]);
    expect(moreActions(undefined, () => {})).toEqual([]);
  });
});

// "Rewind to here" (#2436): on your messages, "Conversation only" / "Code and conversation", the second asking again.
describe("rewindActions", () => {
  const run = async () => {};
  it("has the two choices, the second a danger that asks again", () => {
    const [rewind] = rewindActions("u1#0", "继续", { block: "", run });
    expect(rewind.label).toBe("回退到这里");
    expect(rewind.disabled).toBe(false);
    const [conv, code] = rewind.children as { label: string; danger?: boolean; confirm?: string; inline?: boolean }[];
    expect([conv.label, code.label]).toEqual(["只回退对话", "回退代码和对话"]);
    expect(conv.danger).toBeUndefined();
    expect(conv.confirm).toBeUndefined();
    expect(code.danger).toBe(true);
    expect(code.confirm).toBe("确认回退代码");
    expect(conv.inline && code.inline).toBe(true);
  });
  it("runs the choice it is given, with the message", async () => {
    const got: unknown[] = [];
    const [rewind] = rewindActions("u1#0", "继续", { block: "", run: async (...a) => void got.push(a) });
    const [conv, code] = rewind.children as { onSelect: () => Promise<unknown> }[];
    await conv.onSelect();
    await code.onSelect();
    expect(got).toEqual([["u1#0", "继续", "conversation"], ["u1#0", "继续", "code_and_conversation"]]);
  });
  it("is off with the reason while the session cannot", () => {
    const [rewind] = rewindActions("u1#0", "继续", { block: "工作中", run });
    expect(rewind.disabled).toBe(true);
    expect(rewind.hint).toBe("工作中");
  });
  it("is not there without a message, words or a session that rewinds", () => {
    expect(rewindActions(undefined, "继续", { block: "", run })).toEqual([]);
    expect(rewindActions("u1#0", "  ", { block: "", run })).toEqual([]);
    expect(rewindActions("u1#0", "继续", undefined)).toEqual([]);
  });
});

describe("MessageActions on your own message", () => {
  const mine = (actions: ChatActions, messageId?: string) =>
    renderToStaticMarkup(
      <ChatActionsContext.Provider value={actions}>
        <MessageActions text="继续" isUser messageId={messageId}>
          <p>继续</p>
        </MessageActions>
      </ChatActionsContext.Provider>,
    );
  it("has ⋯ for 回退到这里 when the chat can rewind, even while it is off", () => {
    expect(mine({ ...base, rewind: { block: liveValue(""), run: async () => {} } }, "u1#0")).toContain('aria-label="更多"');
    expect(mine({ ...base, rewind: { block: liveValue("工作中"), run: async () => {} } }, "u1#0")).toContain('aria-label="更多"');
  });
  it("has none for a chat that cannot, or a message without an id", () => {
    expect(mine(base, "u1#0")).not.toContain('aria-label="更多"');
    expect(mine({ ...base, rewind: { block: liveValue(""), run: async () => {} } })).not.toContain('aria-label="更多"');
  });
});
