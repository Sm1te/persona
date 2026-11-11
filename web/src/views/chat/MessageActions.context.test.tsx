// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { initI18n } from "../../i18n";
import { addContext, getContexts, registerComposer, removeContext } from "../../lib/links";
import { ChatActionsContext, liveValue, type ChatActions } from "./chat-actions";
import { MessageActions, messageContextActions } from "./MessageActions";

const media = vi.hoisted(() => ({ noHover: false }));
vi.hoisted(() => {
  window.matchMedia = ((query: string) => ({
    get matches() { return query === "(hover: none)" && media.noHover; },
    media: query,
    onchange: null,
    addEventListener() {},
    removeEventListener() {},
    addListener() {},
    removeListener() {},
    dispatchEvent: () => false,
  })) as typeof window.matchMedia;
});
vi.mock("../../lib/links", async (original) => {
  const actual = await original<typeof import("../../lib/links")>();
  return { ...actual, addContext: vi.fn(actual.addContext) };
});
// counted: every MessageActions render asks for the chat's actions, every icon draws
const counts = vi.hoisted(() => ({ actions: 0, icons: 0 }));
vi.mock("./chat-actions", async (original) => {
  const actual = await original<typeof import("./chat-actions")>();
  return { ...actual, useChatActions: () => (counts.actions++, actual.useChatActions()) };
});
vi.mock("../../ui/icons", async (original) => {
  const actual = await original<typeof import("../../ui/icons")>();
  const { createElement } = await import("react");
  return { ...actual, Icon: (p: Parameters<typeof actual.Icon>[0]) => (counts.icons++, createElement(actual.Icon, p)) };
});

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const forward = vi.fn();
const chat: ChatActions = {
  draftKey: "machine/chat",
  canCompose: true,
  speaker: "Agent",
  title: "Fix login",
  forward,
  images: () => [],
};
let host: HTMLDivElement;
let root: Root;

beforeEach(async () => {
  await initI18n("zh-Hans");
  vi.clearAllMocks();
  media.noHover = false;
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(async () => {
  act(() => root.unmount());
  host.remove();
  vi.useRealTimers();
  for (const key of [chat.draftKey, "machine/other"]) {
    getContexts(key).forEach((ref) => removeContext(key, ref.id));
    localStorage.removeItem(`draft:${key}`);
  }
  await initI18n("zh-Hans");
});

const pick = { itemId: "r1#0", text: "Original message", isUser: false };
const action = (key: string, text = "Original message", source = chat) => {
  const found = messageContextActions(text, source, pick).find((item) => item.key === key);
  expect(found, key).toBeDefined();
  return found!;
};

describe("message context actions", () => {
  it.each([
    [true, true, true, ["forward", "context"]],
    [true, false, true, ["forward"]],
    [false, true, true, ["context"]],
    [true, true, false, ["context"]],
    [false, false, true, []],
  ])("offers Forward where the chat forwards (%s) and Use as context where it composes (%s), for a message in the transcript (%s)", (forwards, canCompose, inTranscript, keys) => {
    const source = { ...chat, canCompose, forward: forwards ? forward : undefined };
    expect(messageContextActions("Message", source, inTranscript ? pick : undefined).map((item) => item.key)).toEqual(keys);
  });

  it("has no actions without a chat or message text", () => {
    expect(messageContextActions("Message", null, pick)).toEqual([]);
    expect(messageContextActions(" \n\t", chat, pick)).toEqual([]);
  });

  it.each([
    ["en", "Forward", "Use as context"],
    ["zh-Hans", "转发", "作为上下文"],
    ["ja", "転送", "コンテキストにする"],
  ])("names Forward and Use as context in %s, and Forward hands the message to the chat's picker", async (lang, forwardLabel, contextLabel) => {
    await initI18n(lang);
    const f = action("forward");
    expect(f.label).toBe(forwardLabel);
    expect(f.icon).toBe("forward");
    expect(action("context").label).toBe(contextLabel);
    await f.onSelect();
    expect(forward).toHaveBeenCalledExactlyOnceWith(pick);
  });

  it("adds to this message's composer even when another composer is active, preserving the draft", async () => {
    localStorage.setItem(`draft:${chat.draftKey}`, "My next question");
    const unregister = registerComposer({ key: "machine/other", focus: () => {}, send: async () => true });
    try {
      await action("context", "  First line\nSecond line  ").onSelect();
      expect(addContext).toHaveBeenCalledExactlyOnceWith(chat.draftKey, {
        id: expect.any(String), kind: "context", title: "Fix login · 消息", url: "", text: "First line\nSecond line",
      });
      expect(getContexts(chat.draftKey)).toHaveLength(1);
      expect(getContexts(chat.draftKey)[0].id).not.toBe("");
      expect(getContexts("machine/other")).toEqual([]);
      expect(localStorage.getItem(`draft:${chat.draftKey}`)).toBe("My next question");
      expect(forward).not.toHaveBeenCalled();
    } finally {
      unregister();
    }
  });
});

describe("message context interaction paths", () => {
  it.each(["hover", "right-click", "long-press"] as const)("offers both actions through %s for your messages and replies", async (entry) => {
    if (entry === "long-press") {
      media.noHover = true;
      vi.useFakeTimers();
    }
    const photos = ["/Users/me/.persona/inbox/a.jpg"];
    for (const isUser of [false, true]) {
      act(() => root.render(
        <ChatActionsContext.Provider value={chat}>
          <MessageActions text="Original message" isUser={isUser} side={isUser} {...(isUser ? { messageId: "u1#0", photos } : { itemId: "r1#3" })}>
            <p data-message>Original message</p>
          </MessageActions>
        </ChatActionsContext.Provider>,
      ));
      const open = () => act(() => {
        const message = host.querySelector("[data-message]")!;
        if (entry === "hover" && isUser) host.querySelector<HTMLButtonElement>('button[aria-label="更多"]')!.click();
        if (entry === "right-click") message.dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 20, clientY: 20 }));
        if (entry === "long-press") {
          message.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true, pointerType: "touch", clientX: 20, clientY: 20 }));
          vi.advanceTimersByTime(450);
          message.dispatchEvent(new PointerEvent("pointerup", { bubbles: true, pointerType: "touch" }));
        }
      });
      const menuItems = (label: string) => [...document.querySelectorAll<HTMLElement>('[role="menuitem"]')].filter((item) => item.textContent?.trim() === label);
      const button = (label: string) => entry === "hover" && !isUser
        ? host.querySelector<HTMLElement>(`button[aria-label="${label}"]`)
        : menuItems(label)[0];
      open();
      expect(button("转发")).toBeTruthy();
      expect(button("作为上下文")).toBeTruthy();
      expect(button("交给 Persona")).toBeFalsy();
      if (entry !== "hover" || isUser) {
        expect(menuItems("转发")).toHaveLength(1);
        expect(menuItems("作为上下文")).toHaveLength(1);
      }
      await act(async () => button("作为上下文")!.click());
      expect(addContext).toHaveBeenLastCalledWith(chat.draftKey, expect.objectContaining({ text: "Original message" }));
      open();
      await act(async () => button("转发")!.click());
      expect(forward).toHaveBeenLastCalledWith(isUser
        ? expect.objectContaining({ itemId: "u1#0", text: "Original message", isUser: true, photos })
        : expect.objectContaining({ itemId: "r1#3", text: "Original message", isUser: false }));
    }
    expect(addContext).toHaveBeenCalledTimes(2);
    expect(forward).toHaveBeenCalledTimes(2);
  });

  it("gives a reply a copy button and a ⋯ on touch, which opens the same menu", () => {
    const render = (isUser: boolean) => act(() => root.render(
      <ChatActionsContext.Provider value={chat}>
        <MessageActions text="Original message" isUser={isUser} side={isUser} itemId="r1#3"><p>Original message</p></MessageActions>
      </ChatActionsContext.Provider>,
    ));
    // the hover bar's copy button is always in the page, hidden by CSS: only a second one is the touch row's
    const copies = () => host.querySelectorAll('button[aria-label="拷贝回复"]').length;
    render(false);
    expect(copies()).toBe(1);
    expect(host.querySelector('button[aria-label="更多"]')).toBeNull();
    media.noHover = true;
    render(true);
    expect(copies()).toBe(0);
    render(false);
    expect(copies()).toBe(2);
    act(() => host.querySelector<HTMLButtonElement>('button[aria-label="更多"]')!.click());
    expect([...document.querySelectorAll('[role="menuitem"]')].map((item) => item.textContent?.trim())).toEqual(expect.arrayContaining(["转发", "作为上下文"]));
  });

  it("offers no Forward for words not in the transcript yet (no item to point back to)", () => {
    act(() => root.render(
      <ChatActionsContext.Provider value={chat}>
        <MessageActions text="Streaming reply"><p>Streaming reply</p></MessageActions>
      </ChatActionsContext.Provider>,
    ));
    expect(host.querySelector('button[aria-label="转发"]')).toBeNull();
    expect(host.querySelector('button[aria-label="作为上下文"]')).toBeTruthy();
  });

  it("keeps the side bar compact and separates context actions from rewind in More", () => {
    act(() => root.render(
      <ChatActionsContext.Provider value={{ ...chat, rewind: { block: liveValue(""), run: async () => {} } }}>
        <MessageActions text="Original message" isUser side messageId="message#0"><p>Original message</p></MessageActions>
      </ChatActionsContext.Provider>,
    ));
    expect(host.querySelector('button[aria-label="转发"]')).toBeNull();
    expect(host.querySelector('button[aria-label="作为上下文"]')).toBeNull();
    expect(host.querySelectorAll("button")).toHaveLength(4); // Copy, Quote, Edit, More.
    act(() => host.querySelector<HTMLButtonElement>('button[aria-label="更多"]')!.click());
    const menu = document.querySelector('[role="menu"]')!;
    expect([...menu.querySelectorAll('[role="menuitem"]')].map((item) => item.textContent?.trim())).toEqual([
      "转发", "作为上下文", "回退到这里",
    ]);
    expect(menu.querySelectorAll('[role="separator"]')).toHaveLength(1);
  });
});

describe("a message only on this device (#4626)", () => {
  it.each(["hover", "right-click", "long-press"] as const)("ends in Delete, in red, through %s", (entry) => {
    if (entry === "long-press") {
      media.noHover = true;
      vi.useFakeTimers();
    }
    const onDelete = vi.fn();
    act(() => root.render(
      <ChatActionsContext.Provider value={{ ...chat, rewind: { block: liveValue(""), run: async () => {} } }}>
        <MessageActions text="Not sent" isUser side onDelete={onDelete}><p data-message>Not sent</p></MessageActions>
      </ChatActionsContext.Provider>,
    ));
    act(() => {
      const message = host.querySelector("[data-message]")!;
      if (entry === "hover") host.querySelector<HTMLButtonElement>('button[aria-label="更多"]')!.click();
      if (entry === "right-click") message.dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 20, clientY: 20 }));
      if (entry === "long-press") {
        message.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true, pointerType: "touch", clientX: 20, clientY: 20 }));
        vi.advanceTimersByTime(450);
        message.dispatchEvent(new PointerEvent("pointerup", { bubbles: true, pointerType: "touch" }));
      }
    });
    const items = [...document.querySelectorAll<HTMLElement>('[role="menuitem"]')];
    const last = items.at(-1)!;
    expect(last.textContent?.trim()).toBe("删除");
    expect(last.className).toContain("text-red");
    expect(items.map((item) => item.textContent?.trim())).not.toContain("回退到这里"); // not in the transcript to go back to
    act(() => last.click());
    expect(onDelete).toHaveBeenCalledOnce();
  });

  it("has no Delete on a message the hub has", () => {
    act(() => root.render(
      <ChatActionsContext.Provider value={chat}>
        <MessageActions text="Sent" isUser side messageId="m#0"><p data-message>Sent</p></MessageActions>
      </ChatActionsContext.Provider>,
    ));
    act(() => void host.querySelector("[data-message]")!.dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 20, clientY: 20 })));
    expect([...document.querySelectorAll('[role="menuitem"]')].map((item) => item.textContent?.trim())).not.toContain("删除");
  });
});

describe("why rewind is off, as the session starts and stops working", () => {
  it("renders no message's actions, and an open menu follows it", () => {
    vi.useFakeTimers({ toFake: ["Date"] }); // fixed time (lesson #1989): only the block's changes are under test
    const block = liveValue("");
    const value: ChatActions = { ...chat, rewind: { block, run: async () => {} } };
    const draw = (v: ChatActions) => act(() => root.render(
      <ChatActionsContext.Provider value={v}>
        {["u1#0", "u2#0", "u3#0"].map((id) => (
          <MessageActions key={id} text={`Message ${id}`} isUser side messageId={id}><p>{id}</p></MessageActions>
        ))}
      </ChatActionsContext.Provider>,
    ));
    draw(value);
    Object.assign(counts, { actions: 0, icons: 0 });
    for (const why of ["工作中", "", "工作中", ""]) act(() => block.set(why));
    expect(counts).toEqual({ actions: 0, icons: 0 });

    act(() => host.querySelector<HTMLButtonElement>('button[aria-label="更多"]')!.click());
    const rewind = () => [...document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')].find((b) => b.textContent?.includes("回退到这里"))!;
    expect(rewind().disabled).toBe(false);
    act(() => block.set("工作中"));
    expect(rewind().disabled).toBe(true);
    expect(rewind().textContent).toContain("工作中");
    act(() => block.set(""));
    expect(rewind().disabled).toBe(false);
    expect(rewind().textContent).not.toContain("工作中");

    // the counts are live: new chat actions still reach every message
    Object.assign(counts, { actions: 0, icons: 0 });
    draw({ ...value, canCompose: false });
    expect(counts.actions).toBeGreaterThanOrEqual(3);
    expect(counts.icons).toBeGreaterThan(0);
    expect(host.querySelector('button[aria-label="引用"]')).toBeNull();
  });
});
