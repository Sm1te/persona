// @vitest-environment jsdom
// Answering a waiting card from the keyboard (#1720): the shortcut's focus lands on the card, digits pick its options,
// Enter runs its main action (never a deny — a focused Deny button's Enter stays the button's own, which jsdom does not
// fire, so the card staying silent is exactly the yield), Esc hands focus back to the composer without answering, and
// the key hints (option digits, the Enter badge) show only while the card holds keyboard focus. Typing in the card's
// own fields and an IME composition keep their keys. The hub is faked (apiOp), as the free-text test's.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
const calls = vi.hoisted(() => [] as { path: string; body: Record<string, unknown> }[]);
vi.mock("../../lib/ops", () => ({
  apiOp: vi.fn(async (path: string, init: { json: Record<string, unknown> }) => {
    calls.push({ path, body: init.json });
    return {};
  }),
}));
import { InteractionCard } from "./Interactions";
import { getState } from "../../lib/store";
import type { Interaction, Session } from "../../lib/types";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

beforeAll(() => {
  // jsdom gives elements no boxes; without this the composer would count as hidden and Esc would not find it
  Element.prototype.getClientRects = function () {
    return [{}] as unknown as DOMRectList;
  };
});

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 5; i++) await act(async () => { await Promise.resolve(); });
};
const cardRoot = () => host.querySelector<HTMLElement>("[data-card-keys]")!;
/** A keydown on the card's root, as a press with the card focused is; `at` picks another element inside it. */
const press = (k: string, at?: string, mods: Partial<{ ctrlKey: boolean; altKey: boolean; shiftKey: boolean; isComposing: boolean }> = {}) =>
  act(() => {
    (at ? host.querySelector(at)! : cardRoot()).dispatchEvent(new KeyboardEvent("keydown", { key: k, bubbles: true, cancelable: true, ...mods }));
  });

const question = JSON.parse(
  '{"id":"q_1","machine_id":"m","session_id":"s","kind":"question","tool_name":"AskUserQuestion",' +
    '"questions":[{"question":"哪个框架？","header":"测试","multi_select":false,"options":[' +
    '{"label":"Swift Testing","description":"新代码统一用"},{"label":"XCTest","description":"和现有的一致"}]}]}',
) as Interaction;
const threeQuestions = JSON.parse(
  '{"id":"q_3","machine_id":"m","session_id":"s","kind":"question","tool_name":"AskUserQuestion",' +
    '"questions":[{"question":"第一题","multi_select":false,"options":[{"label":"A"},{"label":"B"}]},' +
    '{"question":"第二题","multi_select":false,"options":[{"label":"C"}]},' +
    '{"question":"第三题","multi_select":false,"options":[{"label":"D"}]}]}',
) as Interaction;
const permission = JSON.parse(
  '{"id":"p_1","machine_id":"m","session_id":"s","kind":"permission","tool_name":"Bash","tool_input":{"command":"npm test"},"summary":"跑测试"}',
) as Interaction;
const plan = JSON.parse(
  '{"id":"pl_1","machine_id":"m","session_id":"s","kind":"plan","tool_name":"ExitPlanMode","plan":"## 计划\\n1. 第一步"}',
) as Interaction;

beforeEach(() => {
  calls.length = 0;
  Object.assign(getState(), { sessions: { m: [{ machine_id: "m", session_id: "s" } as Session] }, interactions: [], answered: {} });
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

describe("a card answering from the keyboard", () => {
  it("selects and submits a Codex App async answer with only the question capability", async () => {
    Object.assign(getState(), { sessions: { m: [{ machine_id: "m", session_id: "s", source_kind: "codex_history", capabilities: { read: true, send: true, answer_questions: true, approve: false } } as Session] } });
    act(() => root.render(<InteractionCard ix={{ ...question, tool_name: "request_user_input_async" }} />));
    expect(host.querySelector("fieldset[disabled]")).toBeNull();
    const picked = [...host.querySelectorAll<HTMLButtonElement>("button[aria-pressed]")].find((b) => b.textContent?.includes("Swift Testing"))!;
    act(() => picked.click());
    expect(picked.getAttribute("aria-pressed")).toBe("true");
    act(() => cardRoot().focus());
    await press("Enter");
    await flush();
    expect(calls).toEqual([{ path: "/interactions/q_1/resolve", body: { answers: { "哪个框架？": "Swift Testing" } } }]);
  });

  it.each([
    ["sync question", { ...question, tool_name: "request_user_input" }],
    ["permission", permission],
    ["plan", plan],
  ])("the desktop async-question capability does not enable a %s", async (_name, ix) => {
    Object.assign(getState(), { sessions: { m: [{ machine_id: "m", session_id: "s", source_kind: "codex_history", capabilities: { read: true, send: true, answer_questions: true, approve: false } } as Session] } });
    act(() => root.render(<InteractionCard ix={ix} />));
    expect(host.querySelector("fieldset[disabled]")).toBeTruthy();
    act(() => cardRoot().focus());
    await press("1");
    await press("Enter");
    await flush();
    expect(calls).toEqual([]);
  });

  it("Enter allows a permission: the main action, never a deny", async () => {
    act(() => root.render(<InteractionCard ix={permission} />));
    act(() => cardRoot().focus());
    await press("Enter");
    await flush();
    expect(calls).toEqual([{ path: "/interactions/p_1/resolve", body: { allow: true } }]);
  });

  it("Enter on a focused Deny button is the button's, not the card's: the card does not answer for it", async () => {
    act(() => root.render(<InteractionCard ix={permission} />));
    const deny = [...host.querySelectorAll("button")].find((b) => b.textContent === "拒绝")!;
    act(() => deny.focus());
    await press("Enter", "button");
    await flush();
    expect(calls).toEqual([]);
  });

  it("digits pick a question's options and Enter sends the answer, as the send arrow does", async () => {
    act(() => root.render(<InteractionCard ix={question} />));
    act(() => cardRoot().focus());
    await press("1");
    const picked = [...host.querySelectorAll("button[aria-pressed]")].find((b) => b.textContent?.includes("Swift Testing"))!;
    expect(picked.getAttribute("aria-pressed")).toBe("true");
    await press("Enter");
    await flush();
    expect(calls).toEqual([{ path: "/interactions/q_1/resolve", body: { answers: { "哪个框架？": "Swift Testing" } } }]);
  });

  it("Enter approves a plan with edits", async () => {
    act(() => root.render(<InteractionCard ix={plan} />));
    act(() => cardRoot().focus());
    await press("Enter");
    await flush();
    expect(calls).toEqual([{ path: "/interactions/pl_1/resolve", body: { approve: true, mode: "acceptEdits" } }]);
  });

  it("Esc goes back to the composer and answers nothing", async () => {
    const composer = document.createElement("main");
    composer.innerHTML = '<div data-chat-dock><textarea></textarea></div>';
    document.body.append(composer);
    act(() => root.render(<InteractionCard ix={permission} />));
    act(() => cardRoot().focus());
    await press("Escape");
    await flush();
    expect(document.activeElement).toBe(composer.querySelector("textarea"));
    expect(calls).toEqual([]);
  });

  it("a digit then Enter right away advances one question, not two (the pick's own slide is cancelled)", async () => {
    vi.useFakeTimers();
    try {
      act(() => root.render(<InteractionCard ix={threeQuestions} />));
      act(() => cardRoot().focus());
      await press("1");
      await press("Enter");
      act(() => vi.advanceTimersByTime(400));
      expect(host.textContent).toContain("第二题");
      expect(host.textContent).not.toContain("第三题");
      // unanswered as it is, Enter sends nothing
      await press("Enter");
      await flush();
      expect(calls).toEqual([]);
    } finally {
      vi.useRealTimers();
    }
  });

  it("a card this page cannot answer (a disabled fieldset) takes no keys: nothing is picked, nothing sent", async () => {
    Object.assign(getState(), { sessions: { m: [{ machine_id: "m", session_id: "s", source_kind: "codex_history" } as Session] } });
    act(() => root.render(<InteractionCard ix={question} />));
    expect(host.querySelector("fieldset[disabled]")).toBeTruthy();
    act(() => cardRoot().focus());
    await press("1");
    await press("Enter");
    await flush();
    for (const b of host.querySelectorAll("button[aria-pressed]")) expect(b.getAttribute("aria-pressed")).toBe("false");
    expect(calls).toEqual([]);
  });

  it("keeps typing and IME composition to the fields: a digit in the card's input picks nothing", async () => {
    act(() => root.render(<InteractionCard ix={question} />));
    const field = host.querySelector("input")!;
    act(() => field.focus());
    await press("1", "input");
    await press("1", "input", { isComposing: true });
    await flush();
    for (const b of host.querySelectorAll("button[aria-pressed]")) expect(b.getAttribute("aria-pressed")).toBe("false");
    expect(calls).toEqual([]);
  });

  it("shows the key hints only while the card holds keyboard focus", async () => {
    act(() => root.render(<InteractionCard ix={question} />));
    const hints = () => [...host.querySelectorAll("button[aria-pressed]")].map((b) => b.querySelectorAll("kbd, [data-key-hint]").length);
    expect(hints().every((n) => n === 0)).toBe(true);
    act(() => cardRoot().focus());
    await press("2"); // a key first, so the hints' state has gone through a render
    expect(host.querySelectorAll("button[aria-pressed]")[0]!.querySelectorAll("[data-key-hint]").length).toBe(1); // the digit badge
    expect(host.querySelector("[data-card-actions] kbd")).toBeTruthy(); // the Enter badge by the send arrow
    act(() => cardRoot().blur());
    await act(async () => { await Promise.resolve(); });
    expect(hints().every((n) => n === 0)).toBe(true);
    expect(host.querySelector("[data-card-actions] kbd")).toBeFalsy();
  });
});
