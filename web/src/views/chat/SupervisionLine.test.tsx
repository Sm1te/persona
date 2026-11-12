// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { currentLanguage, initI18n } from "../../i18n";
import { chatLines, mergeItems, type ChatLine } from "../../lib/turns";
import { emptyTaskIndex } from "../../lib/tasks";
import type { Item } from "../../lib/types";
import { ChatContext } from "./chat-context";
import { ChatLineView } from "./Lines";
import { ReportLine } from "../assistant/Supervision";
import { reportLine } from "../../lib/supervision";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
window.matchMedia ??= ((media: string) => ({ matches: false, media, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;

// i18n-ignore: these exact markers are the supervision wire contract, not UI copy.
const input = '📋 督办：你派出去的任务有 1 个要你处理\n<persona-supervision level="triage" />\n这是 Persona 的督办消息\nOriginal fictional input';
const u: Item = { id: "input#0", seq: 1, role: "user", kind: "text", text: input };
const thought: Item = { id: "thought#0", seq: 2, role: "assistant", kind: "thinking", text: "Inspecting fictional evidence" };
const use: Item = { id: "read#0", seq: 3, role: "assistant", kind: "tool_use", tool_use_id: "read-call", tool_name: "Read", tool_input: { file_path: "/tmp/fictional-example.txt" } };
const result: Item = { id: "read-result#0", seq: 4, role: "user", kind: "tool_result", tool_use_id: "read-call", text: "Fictional source text" };
const finish: Item = { id: "finish#0", seq: 5, role: "assistant", kind: "tool_use", tool_use_id: "finish-call", tool_name: "supervision_finish", tool_input: { visibility: "silent", summary: "Fictional result" } };
const receipt: Item = { id: "finish-result#0", seq: 6, role: "user", kind: "tool_result", tool_use_id: "finish-call", text: '{"visibility":"silent","summary":"Fictional result","level":"triage"}' };
const answer: Item = { id: "answer#0", seq: 7, role: "assistant", kind: "text", text: "Fictional final reply" };
const next: Item = { id: "next#0", seq: 8, role: "user", kind: "text", text: "A later real request", queued: true, client_msg_id: "fictional-next" };
const initial = [u, thought, use, result, finish, receipt, answer];
const language = currentLanguage();
let host: HTMLDivElement;
let root: Root;
let scope: string;
let n = 0;

beforeEach(async () => {
  await initI18n("zh-Hans");
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  scope = `fictional-supervision-${++n}:`;
});
afterEach(async () => {
  act(() => root.unmount());
  host.remove();
  await initI18n(language);
});
function draw(items: Item[], live = false, streaming?: string, expandFold: (f: Item) => Promise<void> = async () => {}) {
  const lines = chatLines(items, { live });
  act(() => root.render(<ChatContext.Provider value={{ tasks: emptyTaskIndex(), subagents: [], scope }}>
    {lines.map((line) => <ChatLineView key={line.id} line={line} mid="fictional-machine" sid="fictional-chat" assistant streaming={streaming} expandFold={expandFold} />)}
  </ChatContext.Provider>));
}
const toggle = () => host.querySelector<HTMLButtonElement>("[data-supervision] > button")!;

describe("quiet supervision rendering", () => {
  it("defaults to one gray line, keeping a later queued user input in the open", () => {
    draw([...initial, next]);
    expect(toggle().textContent).toBe("@督办巡检 · 督办已处理");
    expect(toggle().getAttribute("aria-expanded")).toBe("false");
    expect(toggle().className).toContain("text-ink-3");
    expect(toggle().className).toContain("whitespace-nowrap");
    expect(host.textContent).not.toContain("Fictional final reply");
    expect(host.textContent).not.toContain("Original fictional input");
    expect(host.querySelector("[data-user-bubble]")?.textContent).toContain(next.text);
  });

  it("expands the original input, thoughts, tool input/result and reply through the real renderer", async () => {
    draw(initial);
    act(() => toggle().click());
    expect(host.querySelector("[data-supervision-input] pre")?.textContent).toBe(input);
    expect(host.querySelector("[data-answer]")?.textContent).toBe(answer.text);
    expect(host.querySelector('[data-action-row="supervision_finish"]')).toBeNull();
    await act(async () => host.querySelector<HTMLButtonElement>("[data-work-group] > button")!.click());
    expect(host.querySelector('[data-action-row="supervision_finish"]')).not.toBeNull();
    expect(host.querySelector('[data-action-row="Read"]')).not.toBeNull();
    // a thought that fits on its row says it there; rows come in their own chunk (#5155)
    await act(async () => { await vi.dynamicImportSettled(); });
    expect(host.querySelector('[data-thinking="words"]')?.textContent).toContain(thought.text);
    act(() => host.querySelector<HTMLButtonElement>('[data-action-row="Read"] > button')!.click());
    expect(host.querySelector('[data-action-row="Read"] [data-inline-detail]')?.textContent).toContain(result.text);
    expect(host.querySelector('[data-action-row="Read"] [data-inline-detail]')?.textContent).toContain('"file_path": "/tmp/fictional-example.txt"');
    const finishButton = host.querySelector<HTMLButtonElement>('[data-action-row="supervision_finish"] > button')!;
    act(() => finishButton.click());
    const detail = host.querySelector('[data-action-row="supervision_finish"] [data-inline-detail]');
    expect(detail?.textContent).toContain('"visibility": "silent"');
    expect(detail?.textContent).toContain(receipt.text);
    expect(host.textContent).not.toContain("A later real request");
    act(() => toggle().click());
    expect(host.querySelector("[data-supervision-trace]")).toBeNull();
  });

  it("keeps the running line quiet while live prose changes, and shows that prose when opened", () => {
    draw([u, thought], true, "Fictional streamed reply");
    expect(toggle().textContent).toBe("@督办巡检 · 督办处理中…");
    expect(host.textContent).not.toContain("Fictional streamed reply");
    act(() => toggle().click());
    expect(host.querySelector('[data-answer="streaming"]')?.textContent).toBe("Fictional streamed reply");
    draw(initial);
    expect(toggle().textContent).toBe("@督办巡检 · 督办已处理");
    expect(toggle().getAttribute("aria-expanded")).toBe("true");
  });

  it("expands a server fold without losing the opened trace or raw tool history", async () => {
    const f: Item = { id: "fold:thought#0", seq: 2, role: "assistant", kind: "fold", fold: { items: 3, tools: 1, thoughts: 1, through_seq: 4 } };
    let items = [u, f, finish, receipt, answer];
    const expandFold = vi.fn(async () => {
      items = mergeItems(items, [thought, use, result]);
      draw(items, false, undefined, expandFold);
    });
    draw(items, false, undefined, expandFold);
    act(() => toggle().click());
    await act(async () => host.querySelector<HTMLButtonElement>("[data-work-group] > button")!.click());
    expect(expandFold).toHaveBeenCalledWith(f);
    expect(toggle().getAttribute("aria-expanded")).toBe("true");
    expect(host.querySelector('[data-action-row="Read"]')).not.toBeNull();
    expect(host.querySelector("[data-supervision-input] pre")?.textContent).toBe(input);
  });

  it("promotes a user-visible round while its tool trace remains manually expandable", () => {
    draw(initial);
    const changed = { ...receipt, text: '{"visibility":"user","summary":"A decision is needed","level":"user"}' };
    draw(initial.map((i) => i.id === receipt.id ? changed : i));
    expect(host.querySelector("[data-supervision]")).toBeNull();
    expect(host.querySelector("[data-answer]")?.textContent).toBe(answer.text);
    expect(host.querySelector('[data-action-row="supervision_finish"]')).toBeNull();
    act(() => host.querySelector<HTMLButtonElement>("[data-work-group] > button")!.click());
    const tool = host.querySelector('[data-action-row="supervision_finish"]');
    expect(tool).not.toBeNull();
    expect(tool?.closest("[data-work-group]")).not.toBeNull();
  });

  it.each([
    ["en", "@Supervision · Supervision handled"],
    ["zh-Hans", "@督办巡检 · 督办已处理"],
    ["ja", "@進捗チェック · タスクの確認済み"],
  ] as const)("localizes the %s compact line", async (lang, expected) => {
    await initI18n(lang);
    draw(initial);
    expect(toggle().textContent).toBe(expected);
  });

  it("renders all child segments of a folded round without absorbing an unrelated sibling", () => {
    const one = chatLines([u, answer])[0];
    if (one.kind !== "supervision") throw new Error("Expected supervision");
    const extra = chatLines([{ ...u, id: "input-part#0", seq: 2 }, { ...answer, id: "reply-part#0", seq: 3 }])[0];
    if (extra.kind !== "supervision") throw new Error("Expected supervision");
    const line: ChatLine = { ...one, lines: [...one.lines, ...extra.lines] };
    act(() => root.render(<ChatLineView line={line} mid="fictional-machine" sid="fictional-chat" expandFold={async () => {}} />));
    act(() => toggle().click());
    expect(host.querySelectorAll("[data-supervision-input]")).toHaveLength(2);
    expect(host.querySelectorAll("[data-answer]")).toHaveLength(2);
  });
});

describe("progress reports", () => {
  it("defaults to a quiet line and preserves the full progress body without the separate evidence list on expansion", () => {
    const item = { id: "fictional-report", conversation: "fictional-chat", at: "2026-11-01T10:00:00Z", kind: "report" as const, body: { kind: "progress", text: "Fictional progress details", step: 1, steps: 3, evidence: ["https://example.com/evidence"] } };
    act(() => root.render(<ReportLine item={item} title="Example task" />));
    expect(host.textContent).toBe("进度更新 · Example task");
    expect(host.querySelector('[data-testid="report-line"]')).toBeNull();
    act(() => host.querySelector<HTMLButtonElement>('button[aria-expanded="false"]')!.click());
    expect(host.textContent).toContain("Fictional progress details");
    expect(host.textContent).not.toContain("https://example.com/evidence");
    expect(host.querySelector("ul")).toBeNull();
  });
});

describe("report bodies", () => {
  it.each(["done", "blocked", "question", "progress"])("keeps the %s body verbatim, including authored links, while hiding all evidence rows", (kind) => {
    const text = "Checks passed.\nPR: https://example.com/body-pr\nKeep this final line.";
    const evidence = [
      "https://example.com/evidence-pr",
      "https://example.com/evidence-commit",
      "https://example.com/evidence-comment",
      "https://example.com/evidence-run",
      "/tmp/example-results.xcresult",
    ];
    const item = { id: "fictional-report-body", conversation: "fictional-chat", at: "2026-11-01T10:00:00Z", kind: "report" as const, body: { kind, text, evidence } };
    act(() => root.render(<ReportLine item={item} title="Example task" />));
    if (kind === "progress") act(() => host.querySelector<HTMLButtonElement>('button[aria-expanded="false"]')!.click());
    expect(host.querySelector('[data-testid="report-line"] .whitespace-pre-wrap')?.textContent).toBe(reportLine(kind, text));
    for (const entry of evidence) expect(host.textContent).not.toContain(entry);
    expect(host.querySelector("ul")).toBeNull();
    expect(item.body.evidence).toEqual(evidence);
  });

  it("retains can-wait status and the task link without showing evidence", () => {
    const onOpenTask = vi.fn();
    const item = { id: "fictional-later-report", conversation: "fictional-chat", at: "2026-11-01T10:00:00Z", kind: "report" as const, body: { kind: "question", text: "A question that can wait", later: true, evidence: ["https://example.com/evidence"] } };
    act(() => root.render(<ReportLine item={item} title="Example task" onOpenTask={onOpenTask} />));
    expect(host.querySelector('[data-testid="report-later"]')).not.toBeNull();
    act(() => host.querySelector<HTMLButtonElement>("button")!.click());
    expect(onOpenTask).toHaveBeenCalledOnce();
    expect(host.textContent).not.toContain("https://example.com/evidence");
  });
});
