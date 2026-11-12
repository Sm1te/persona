// @vitest-environment jsdom
// "Search in chat" (ChatSearch.tsx, search-reveal.ts): the panel's states and picking a hit; a hit inside a folded tool row or
// thought opens it, and "Back to where you were" closes it again.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Item } from "../../lib/types";
import { emptyTaskIndex } from "../../lib/tasks";
import { ActionRow } from "./ActionRow";
import { ChatContext } from "./chat-context";
import { ChatSearch } from "./ChatSearch";
import { revealItem, undoReveals } from "./search-reveal";
import { ThinkingRow } from "./ThinkingRow";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;

beforeEach(() => {
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  document.body.innerHTML = "";
  vi.useRealTimers();
});

const items: Item[] = [
  { id: "u#0", seq: 1, role: "user", kind: "text", text: "look at the parser" },
  { id: "tu#0", seq: 2, role: "assistant", kind: "tool_use", tool_name: "Bash", tool_use_id: "c", tool_input: { command: "go test ./parser" } },
  { id: "tr#0", seq: 3, role: "user", kind: "tool_result", tool_use_id: "c", text: "FAIL parser_test.go" },
];

function panel(over: Partial<React.ComponentProps<typeof ChatSearch>> = {}) {
  const props: React.ComponentProps<typeof ChatSearch> = {
    open: true,
    query: "",
    onQuery: () => {},
    items,
    rounds: 1,
    hasMore: false,
    onLoadEarlier: async () => null,
    onPick: () => {},
    onClose: () => {},
    ...over,
  };
  act(() => root.render(<ChatSearch {...props} />));
  return props;
}

describe("ChatSearch", () => {
  it("asks for a keyword, says what it searches and that the chat starts here", () => {
    panel();
    const text = document.body.textContent ?? "";
    expect(text).toContain("输入关键词");
    expect(text).toContain("可搜索正文、思考、工具名、工具输入和输出、系统事件。");
    expect(text).toContain("0 条命中，已加载 1 轮");
    expect(text).toContain("已到最早的记录");
  });

  it("lists the hits by category with the match marked, and picks one", () => {
    vi.useFakeTimers();
    const onPick = vi.fn();
    panel({ query: "parser", onPick });
    act(() => vi.advanceTimersByTime(250));
    const heads = [...document.querySelectorAll("[data-chat-search] h3")].map((h) => h.textContent);
    expect(heads).toEqual(["工具输出", "工具输入", "正文"]);
    expect([...document.querySelectorAll("mark")].map((m) => m.textContent)).toEqual(["parser", "parser", "parser"]);
    act(() => (document.querySelector("[data-search-result]") as HTMLButtonElement).click());
    expect(onPick).toHaveBeenCalledWith(expect.objectContaining({ itemId: "tr#0", category: "tool_output" }));
  });

  it("says when nothing matched", () => {
    vi.useFakeTimers();
    panel({ query: "nothing-like-this" });
    act(() => vi.advanceTimersByTime(250));
    expect(document.body.textContent).toContain("没有找到匹配记录");
  });

  it("loads earlier pages, and says why it stopped", async () => {
    let resolve: (v: string | null) => void = () => {};
    const onLoadEarlier = vi.fn((onPage: (n: number) => void) => {
      onPage(2);
      return new Promise<string | null>((r) => (resolve = r));
    });
    panel({ hasMore: true, onLoadEarlier });
    const load = [...document.querySelectorAll("button")].find((b) => b.textContent === "加载更早的记录并继续搜")!;
    act(() => load.click());
    expect(document.body.textContent).toContain("正在加载更早的记录，已加载 2 页");
    expect([...document.querySelectorAll("button")].some((b) => b.textContent === "停止加载")).toBe(true);
    await act(async () => resolve("更早的记录未能载入，请重试"));
    expect(document.body.textContent).toContain("更早的记录未能载入，请重试");
  });
});

describe("going to a hit", () => {
  const ctx = { tasks: emptyTaskIndex(), subagents: [], scope: "m/s:" };

  it("opens the tool row that holds it, and closes it again on the way back", () => {
    const step = { kind: "tool" as const, id: "tu#0", use: items[1], result: items[2], images: [] };
    act(() => root.render(<ChatContext.Provider value={ctx}><ActionRow step={step} mid="m" sid="s" /></ChatContext.Provider>));
    expect(host.querySelector("[data-inline-detail]")).toBeNull();
    expect(host.querySelector("[data-items]")?.getAttribute("data-items")).toBe("tu#0 tr#0");
    act(() => revealItem("m/s:", "tr#0"));
    expect(host.querySelector("[data-inline-detail]")?.textContent).toContain("FAIL parser_test.go");
    act(() => undoReveals());
    expect(host.querySelector("[data-inline-detail]")).toBeNull();
  });

  it("leaves another chat's rows alone", () => {
    const step = { kind: "tool" as const, id: "tu#0", use: items[1], result: items[2], images: [] };
    act(() => root.render(<ChatContext.Provider value={ctx}><ActionRow step={step} mid="m" sid="s" /></ChatContext.Provider>));
    act(() => revealItem("other:", "tr#0"));
    expect(host.querySelector("[data-inline-detail]")).toBeNull();
  });

  it("opens a thought", () => {
    const thought: Item = { id: "th#0", seq: 2, role: "assistant", kind: "thinking", text: "the parser drops a token\nat the end" };
    act(() => root.render(<ThinkingRow item={thought} scope="m/s:" />));
    expect(host.querySelector('[data-thinking="words"] > div')?.getAttribute("aria-expanded")).toBe("false");
    act(() => revealItem("m/s:", "th#0"));
    expect(host.querySelector('[data-thinking="words"] > div')?.getAttribute("aria-expanded")).toBe("true");
    act(() => undoReveals());
    expect(host.querySelector('[data-thinking="words"] > div')?.getAttribute("aria-expanded")).toBe("false");
  });

  it("opens a thought that fits on its row too (#5155: its row says it already, opened it shows it whole)", () => {
    const thought: Item = { id: "th#1", seq: 2, role: "assistant", kind: "thinking", text: "the parser drops a token" };
    act(() => root.render(<ThinkingRow item={thought} scope="m/s:" />));
    expect(host.querySelector('[data-thinking="words"] > div')?.hasAttribute("aria-expanded")).toBe(false);
    act(() => revealItem("m/s:", "th#1"));
    expect(host.querySelector('[data-thinking="words"] > div')?.getAttribute("aria-expanded")).toBe("true");
    act(() => undoReveals());
    expect(host.querySelector('[data-thinking="words"] > div')?.hasAttribute("aria-expanded")).toBe(false);
  });
});
