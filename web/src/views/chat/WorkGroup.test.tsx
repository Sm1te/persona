import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import type { Item } from "../../lib/types";
import type { Step } from "../../lib/turns";
import type { ToolStep, WorkGroupNode } from "../../lib/work-groups";
import { ActionRow, argumentLines, DIFF_FOLD_LINES, diffStats, editDiffs, foldDiffs, inlineInput, lineDiff, presentAction, truncateParam } from "./ActionRow";
import { PlanCard } from "./PlanCard";
import { Answer, bubbleLines, SegmentView, UserBubble } from "./SegmentView";
import { SubagentCard } from "./SubagentCard";
import { LiveThinking } from "./ThinkingCard";
import { ThinkingRow } from "./ThinkingRow";
import { WorkGroup, type WorkProps } from "./WorkGroup";

const T0 = Date.parse("2026-11-04T10:00:00Z");
const at = (s: number) => new Date(T0 + s * 1000).toISOString();
const use = (id: string, name: string, input: Record<string, unknown>, ts = 0): Item => ({ id: `${id}#0`, seq: Number(id.replace(/\D/g, "")) || 1, role: "assistant", kind: "tool_use", tool_name: name, tool_use_id: `c-${id}`, tool_input: input, ts: at(ts) });
const res = (id: string, text = "ok", ts = 1): Item => ({ id: `${id}-r#0`, seq: 1, role: "user", kind: "tool_result", tool_use_id: `c-${id}`, text, ts: at(ts) });
const tool = (id: string, name: string, input: Record<string, unknown>, done = true): ToolStep => ({ kind: "tool", id: `${id}#0`, use: use(id, name, input), result: done ? res(id) : undefined, images: [] });
const props: WorkProps = { mid: "m", sid: "s", scope: "t:", expandFold: async () => {}, agentStatus: () => "completed" };
const group = (children: Step[], live: boolean, durationMs?: number): WorkGroupNode => ({ kind: "group", id: `work-${live}`, children: children.map((step) => ({ kind: "step", step })), live, durationMs, startedAt: T0 });

describe("WorkGroup", () => {
  it("its header, a subagent card's and a plan's are a finger tall on a touch screen (#1996)", () => {
    const coarse = "[@media(pointer:coarse)]:min-h-10";
    const head = renderToStaticMarkup(<WorkGroup g={group([tool("t1", "Grep", { pattern: "x" })], false, 3000)} p={props} />);
    // the margin gives back what the height adds, so the group does not move
    expect(head).toContain(`[@media(pointer:coarse)]:-my-[7.5px] ${coarse}`);
    expect(renderToStaticMarkup(<SubagentCard step={tool("a1", "Task", { description: "查", prompt: "p" })} status="completed" mid="m" />)).toContain(coarse);
    expect(renderToStaticMarkup(<PlanCard id="p" todos={[{ content: "读", status: "pending" }]} />)).toContain(coarse);
  });

  it("while live: only a collapsed summary, spinner and clock, with no preview", () => {
    const html = renderToStaticMarkup(<WorkGroup g={group([tool("t1", "Bash", { command: "ls", description: "看目录" }, false)], true)} p={props} />);
    expect(html).toContain('data-work-group="live"');
    expect(html).toContain(">正在工作…<");
    expect(html).toMatch(/font-mono text-\[12px\][^>]*>(\d+[smh]? ?)+<|font-mono text-\[12px\][^>]*>[\d天小时分 ]+</);
    expect(html).not.toContain("data-live-preview");
    expect(html).not.toContain(">看目录<");
    expect(html).toContain('aria-expanded="false"');
    expect(html).not.toContain("data-work-detail"); // a lazy Collapse: nothing until it opens
  });

  it("done: layers and 已工作 Xs, folded", () => {
    const html = renderToStaticMarkup(<WorkGroup g={group([tool("t1", "Bash", { command: "ls" })], false, 65_000)} p={props} />);
    expect(html).toContain(">已工作 1m 5s<");
    expect(html).toContain('aria-expanded="false"');
    expect(html).not.toContain("data-live-preview");
  });

  it("every nonempty live group can be manually opened and never previews tools", () => {
    const five = [1, 2, 3, 4, 5].map((n) => tool(`t${n}`, "Bash", { command: `echo ${n}` }));
    expect(renderToStaticMarkup(<WorkGroup g={group(five, true)} p={props} />)).not.toMatch(/<button[^>]*disabled=""/);
    const six = [...five, tool("t6", "Bash", { command: "echo 6" })];
    const html = renderToStaticMarkup(<WorkGroup g={group(six, true)} p={props} />);
    expect(html).not.toMatch(/^<div[^>]*><button[^>]*disabled/);
    expect(html).not.toContain("echo 1");
    expect(html).not.toContain("echo 6");
  });
});

describe("ActionRow", () => {
  it("a command with a description is that sentence, no verb; the command is its title", () => {
    const p = presentAction(use("t1", "Bash", { command: "npm test", description: "跑单测" }));
    expect(p).toEqual({ param: { text: "跑单测", title: "npm test" } });
    const html = renderToStaticMarkup(<ActionRow step={tool("t1", "Bash", { command: "npm test", description: "跑单测" })} mid="m" sid="s" />);
    expect(html).toContain("py-[3px]");
    expect(html).toContain("rounded-[8px]");
    expect(html).not.toContain("运行命令");
  });

  it("one without: 运行命令 and the command on a quieter line under it", () => {
    const html = renderToStaticMarkup(<ActionRow step={tool("t1", "Bash", { command: "git status --short" })} mid="m" sid="s" />);
    expect(html).toContain(">运行命令<");
    expect(html).toMatch(/pl-\[30px\][^>]*>git status --short</);
  });

  it("a file is a chip with its name only; long parameters stop at 60 characters", () => {
    const html = renderToStaticMarkup(<ActionRow step={tool("t1", "Read", { file_path: "/Users/me/code/app/src/views/Home.tsx" })} mid="m" sid="s" />);
    expect(html).toContain(">读取<");
    expect(html).toContain("data-file-chip");
    expect(html).toContain(">Home.tsx<");
    expect(html).not.toContain(">/Users/me/code/app/src/views/Home.tsx<");
    expect(truncateParam("x".repeat(80))).toHaveLength(60);
    expect(truncateParam("x".repeat(80)).endsWith("...")).toBe(true);
    expect(presentAction(use("g", "Grep", { pattern: "y".repeat(100) })).param!.text).toHaveLength(60);
  });

  it("verbs follow cindy's table", () => {
    const verb = (name: string, input: Record<string, unknown> = {}) => presentAction(use("v", name, input)).verb;
    expect([verb("Edit", { file_path: "/a" }), verb("Write", { file_path: "/a" }), verb("Grep", { pattern: "x" }), verb("WebFetch", { url: "u" }), verb("mcp__github__create_issue")]).toEqual(["编辑", "创建", "搜索", "访问", "调用"]);
    expect(presentAction(use("v", "mcp__github__create_issue", {})).param!.text).toBe("GitHub · Create issue");
    expect(presentAction(use("v", "mcp__github__create_issue", { title: "Crash" })).param!.text).toBe("GitHub · Create issue · Crash");
    expect(presentAction(use("v", "mcp__persona__unknown_tool", {})).param).toEqual({ text: "Persona · unknown tool", title: "mcp__persona__unknown_tool" });
  });

  it("Claude Code's newer tools say what they did, then what about (#2986)", () => {
    expect(presentAction(use("n", "TaskCreate", { subject: "Write tests", description: "…" }))).toEqual({ verb: "加待办", param: { text: "Write tests", title: "Write tests" } });
    expect(presentAction(use("n", "ListAgents", {}))).toEqual({ verb: "看 Agent 列表" });
    expect(presentAction(use("n", "Monitor", { command: "tail -f x", description: "errors in x" })).verb).toBe("监视");
    expect(presentAction(use("n", "PowerShell", { command: "Get-ChildItem" }))).toEqual({ verb: "运行命令", raw: "Get-ChildItem" });
    expect(inlineInput(use("n", "Monitor", { command: "tail -f x", description: "d" }))).toBe("tail -f x");
  });

  it("a tool named like one of Object's methods reads as any other unknown tool", () => {
    expect(presentAction(use("n", "constructor", {}))).toEqual({ verb: "调用", param: { text: "constructor", title: undefined } });
    expect(presentAction(use("n", "hasOwnProperty", { title: "T" })).param!.text).toBe("hasOwnProperty · T");
    expect(presentAction(use("n", "mcp__foundry__constructor_args", {})).param!.text).toBe("Foundry · Constructor args");
  });

  it("any other call opens to its arguments as key: value lines, not JSON", () => {
    expect(argumentLines({ repo: "persona", title: "Crash", labels: ["bug", "ios"], meta: { a: 1 }, blank: " ", none: [] })).toBe(
      "title: Crash\nlabels: bug, ios\nmeta: {\"a\":1}\nrepo: persona",
    );
    expect(inlineInput(use("n", "mcp__github__create_issue", { title: "Crash", body: "Steps" }))).toBe("title: Crash\nbody: Steps");
  });

  it("Persona's own tools read as their verb, then what they were given", () => {
    expect(presentAction(use("p", "mcp__persona__agent_read", {}))).toEqual({ verb: "读 Agent 对话" });
    expect(presentAction(use("p", "mcp__persona__kb_search", { query: "周报" }))).toEqual({ verb: "查知识库", param: { text: "周报", title: "mcp__persona__kb_search" } });
    expect(presentAction(use("p", "mcp__persona__phone_contacts_search", { query: "Alice" }))).toEqual({ verb: "iPhone", param: { text: "contacts search · Alice", title: "mcp__persona__phone_contacts_search" } });
    expect(presentAction(use("p", "mcp__github__create_issue", {})).verb).toBe("调用");
  });

  it("an edit shows its +N -N from a line diff (cindy's ASCII minus); a running call a spinner in its 16 px slot", () => {
    const edit = tool("e1", "Edit", { file_path: "/a.ts", old_string: "a\nb\nc", new_string: "a\nB\nc\nd" });
    expect(diffStats(editDiffs(edit.use)!)).toEqual({ add: 2, del: 1 });
    const html = renderToStaticMarkup(<ActionRow step={edit} mid="m" sid="s" />);
    expect(html).toContain(">+2<");
    expect(html).toContain(">-1<");
    expect(renderToStaticMarkup(<ActionRow step={tool("r", "Bash", { command: "sleep 9" }, false)} running mid="m" sid="s" />)).toMatch(/h-\[18px\] w-4[^>]*><span[^>]*animation:spin/);
  });

  it("lineDiff keeps what did not change and pairs what did", () => {
    const sign = (l: { kind: string; text: string }) => ({ same: " ", del: "-", add: "+" })[l.kind] + l.text;
    expect(lineDiff("a\nb\nc", "a\nx\nc").map(sign)).toEqual([" a", "-b", "+x", " c"]);
    expect(lineDiff("", "n1\nn2").map((l) => l.kind)).toEqual(["add", "add"]);
    expect(lineDiff("o", "").map((l) => l.kind)).toEqual(["del"]);
  });

  it("a long diff shows its first 40 lines on a phone, cut inside the change they end in (#1996)", () => {
    const lines = (n: number) => lineDiff("", Array.from({ length: n }, (_, i) => `l${i}`).join("\n"));
    const short = [lines(10), lines(30)];
    expect(foldDiffs(short)).toEqual({ shown: short, hidden: 0 });
    const { shown, hidden } = foldDiffs([lines(25), lines(25), lines(5)]);
    expect(shown.map((d) => d.length)).toEqual([25, 15]);
    expect(hidden).toBe(15);
    expect(DIFF_FOLD_LINES).toBe(40);
  });

  it("is a finger tall on a touch screen, in the chat's secondary size (#1996)", () => {
    const html = renderToStaticMarkup(<ActionRow step={tool("t1", "Grep", { pattern: "x" })} mid="m" sid="s" />);
    expect(html).toContain("[@media(pointer:coarse)]:min-h-10");
    expect(html).toContain("text-[length:var(--chat-secondary)]");
  });
});

describe("thought rows (cindy's ThinkingActivityRow, #5155)", () => {
  const thought = (text: string): Item => ({ id: "th#1", seq: 1, role: "assistant", kind: "thinking", text });

  it("a thought without words is no row at all", () => {
    expect(renderToStaticMarkup(<ThinkingRow item={thought("")} />)).toBe("");
    expect(renderToStaticMarkup(<ThinkingRow item={thought("  \n ")} />)).toBe("");
  });

  it("one with words is one row: the sparkle in the 16 px slot, its words italic on one line, the whole as its title", () => {
    const html = renderToStaticMarkup(<ThinkingRow item={thought("先看测试，  再看实现")} />);
    expect(html).toContain('data-thinking="words"');
    expect(html).toMatch(/h-\[18px\] w-4[^>]*><svg/);
    expect(html).toMatch(/class="[^"]*truncate[^"]*"[^>]*>先看测试， 再看实现</);
    expect(html).toContain('title="先看测试， 再看实现"');
    expect(html).toContain("text-thinking-ink italic");
    expect(html).toContain("text-[length:var(--chat-secondary)]");
    // the tool rows' chrome: the same radius, padding and chevron slot
    expect(html).toContain("rounded-[8px] px-2 py-[3px]");
    expect(html).toContain("size-[18px]");
    // it fits: nothing to open (measured once drawn), so no chevron and no toggle
    expect(html).not.toContain('role="button"');
    expect(html).not.toContain("aria-expanded");
    expect(html).toContain("cursor-default");
  });

  it("a thought with a line break opens in place", () => {
    const html = renderToStaticMarkup(<ThinkingRow item={thought("先看测试\n再看实现")} />);
    expect(html).toContain('aria-expanded="false"');
    // a div with the button role, not a <button>: opened, its words can be selected
    expect(html).toMatch(/<div role="button" tabindex="0" aria-expanded="false"/);
    expect(html).not.toContain("<button");
    expect(html).toContain(">先看测试 再看实现<");
  });

  it("reads **strong** and backtick code as cindy does, on the row and opened, and nothing else", () => {
    const html = renderToStaticMarkup(<ThinkingRow item={thought("**Inspecting files** with `git status` and [docs](https://example.com)")} />);
    expect(html).toMatch(/<span class="font-medium" data-thinking-markup="strong">Inspecting files<\/span>/);
    expect(html).toMatch(/<code class="font-mono not-italic" data-thinking-markup="code">git status<\/code>/);
    expect(html).not.toContain("**");
    expect(html).not.toContain("<a ");
    expect(html).toContain("[docs](https://example.com)");
    // the hover text is the words without the markers
    expect(html).toContain('title="Inspecting files with git status and [docs](https://example.com)"');
  });

  it("live: Thinking with three dots and a clock", () => {
    const html = renderToStaticMarkup(<LiveThinking />);
    expect(html).toContain(">正在思考<");
    expect(html.match(/size-1 rounded-full/g)).toHaveLength(3);
    expect(html).toMatch(/tabular-nums">1s</);
  });
});

describe("the user's bubble and the answer", () => {
  const msg = (text: string) => ({ id: "u#0", text, images: [], contexts: [] });

  it("on the right, at most 488 wide, 12 px corners", () => {
    const html = renderToStaticMarkup(<UserBubble msg={msg("你好")} mid="m" sid="s" />);
    expect(html).toContain("ml-auto");
    expect(html).toContain("max-w-[488px]");
    expect(html).toContain("rounded-[12px]");
    expect(html).not.toContain("展开");
    expect(html).not.toContain("data-fold-scroll");
  });

  it("past 14 lines it folds to 10 that scroll inside, faded at the bottom, with 展开", () => {
    const long = Array.from({ length: 20 }, (_, i) => `第 ${i} 行`).join("\n");
    expect(bubbleLines(long)).toBe(20);
    const html = renderToStaticMarkup(<UserBubble msg={msg(long)} mid="m" sid="s" />);
    expect(html).toContain("max-h-[10lh] overflow-y-auto");
    expect(html).toContain("data-fold-scroll");
    expect(html).toContain("mask-image:linear-gradient(180deg");
    expect(html).not.toContain("line-clamp");
    expect(html).not.toContain("overscroll");
    // the fade is on the words, not the bubble behind them
    expect(html).toMatch(/bg-user-bubble[^>]*>\s*<div[^>]*data-fold-scroll/);
    expect(html).toContain(">展开<");
    expect(bubbleLines("x".repeat(31))).toBe(2);
  });

  it("past 14 lines it folds while it is still being sent too, dimmed (#1996)", () => {
    const long = Array.from({ length: 20 }, (_, i) => `第 ${i} 行`).join("\n");
    const html = renderToStaticMarkup(<UserBubble msg={msg(long)} mid="m" sid="s" pending />);
    expect(html).toContain("max-h-[10lh] overflow-y-auto");
    expect(html).toContain(">展开<");
    expect(html).toContain("opacity-60");
    expect(html).toContain(">发送中…<");
  });

  it("leaves 12% on its left like a merged bubble, so it never runs the whole width (#1996)", () => {
    expect(renderToStaticMarkup(<UserBubble msg={msg("你好")} mid="m" sid="s" />)).toContain("pl-[12%]");
  });

  it("a streaming answer has the message menu too, at the chat's body size (#1996)", () => {
    const html = renderToStaticMarkup(<Answer streaming="写到一半" mid="m" sid="s" />);
    expect(html).toContain("group/msg");
    expect(html).toContain('data-answer="streaming"');
    expect(html).toContain("[&amp;_.prose-j]:text-[length:var(--chat-body)]!");
  });

  it("an exchange: the answer without a bubble at 15 / 1.6, the work before it folded", () => {
    const user: Item = { id: "u1#0", seq: 1, role: "user", kind: "text", text: "看一下", ts: at(0) };
    const seg = { id: "s", user: { id: "u1#0", text: "看一下", images: [], contexts: [], ts: at(0) }, steps: [tool("t2", "Read", { file_path: "/a.ts" })], answer: { id: "a3#0", seq: 3, role: "assistant", kind: "text", text: "看完了。", ts: at(9) } as Item, tools: 1, thoughts: 0, replies: 0, folds: [], start: user.ts };
    const html = renderToStaticMarkup(<SegmentView seg={seg} live={false} mid="m" sid="s" expandFold={async () => {}} />);
    expect(html).toContain("[&amp;_.prose-j]:text-[length:var(--chat-body)]!");
    expect(html).toContain(">已工作 9s · 读取 1 个文件<");
    expect(html.indexOf("已工作")).toBeLessThan(html.indexOf("看完了。"));
  });

  it("live words occupy the same slot after the folded work on every conversation (#2602)", () => {
    const user: Item = { id: "u1#0", seq: 1, role: "user", kind: "text", text: "跑一遍测试", ts: at(0) };
    const seg = { id: "s", user: { id: "u1#0", text: "跑一遍测试", images: [], contexts: [], ts: at(0) }, steps: [], tools: 0, thoughts: 0, replies: 0, folds: [], start: user.ts };
    const live = (batchedWords?: boolean) =>
      renderToStaticMarkup(<SegmentView seg={seg} live mid="m" sid="s" streaming="接下来跑一遍单元测试。" batchedWords={batchedWords} expandFold={async () => {}} />);
    expect(live()).not.toContain('data-work-group="live"');
    const agent = live(true);
    expect(agent).toContain('data-work-group="live"');
    expect(agent.indexOf('data-work-group="live"')).toBeLessThan(agent.indexOf('data-answer="streaming"'));
    // no live words yet: the row as before
    const waiting = renderToStaticMarkup(<SegmentView seg={seg} live mid="m" sid="s" batchedWords expandFold={async () => {}} />);
    expect(waiting).toContain('data-work-group="live"');
  });
});
