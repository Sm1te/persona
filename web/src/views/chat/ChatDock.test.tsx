// The chat's header and what sits under the chat on a phone (#1996), as markup: the browser checks are
// scripts/test/web_mobile_input_e2e.py.
import { afterEach, describe, expect, it, vi } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import type { Interaction, Session } from "../../lib/types";
import { getState, type AnsweredCard } from "../../lib/store";
import { Header, spaceAgentsItem } from "./ChatView";
import { HEADER_SLOT, HEADER_SLOT_END, HEADER_SLOT_START } from "./header";
import { Composer } from "./Composer";
import { PendingCards, permissionFrom } from "./Interactions";
import { QueueStrip, restoreQueued } from "./QueueStrip";
import { onDraftEvent, type DraftEvent } from "../../lib/drafts";
import { getContexts, removeContext, withContexts } from "../../lib/links";
import { parseQueued } from "../../lib/queue";
import { AttachmentTray } from "../../ui/AttachmentTray";

// stores without a server snapshot: renderToStaticMarkup needs one
vi.mock("../../lib/links", async (orig) => ({ ...(await orig<typeof import("../../lib/links")>()), useContexts: () => [] }));
vi.mock("../../lib/assistant-chat", async (orig) => ({ ...(await orig<typeof import("../../lib/assistant-chat")>()), usePickedLauncher: () => undefined }));

const session = {
  machine_id: "m",
  session_id: "s",
  title: "补齐网络层的单元测试",
  state: "needs_you",
  space_id: "sp",
  cwd: "/tmp/x",
} as unknown as Session;
const sibling = { ...session, session_id: "s2" } as Session;

const header = (extra: Partial<Parameters<typeof Header>[0]> = {}) =>
  renderToStaticMarkup(<Header session={session} title="补齐网络层的单元测试" machineName="MacBook" siblings={[session, sibling]} onInterrupt={() => {}} onStopLoop={() => {}} onToggleSidebar={() => {}} {...extra} />);

describe("Header", () => {
  it("puts the switch it is given in the middle on a narrow screen, in place of the title", () => {
    const html = header({ topCenter: <span data-switch>助手 | Agents</span> });
    expect(html).toContain("data-switch");
    expect(html).toMatch(/class="flex min-w-0 flex-1 justify-center lg:hidden"><span data-switch/);
    expect(html).toMatch(/class="min-w-0 flex-1 max-lg:hidden"/);
    // the buttons either side sit in slots of one least width, which grow to fit them (#5036)
    expect(html).toContain(`class="${HEADER_SLOT} ${HEADER_SLOT_START} lg:contents"`);
    expect(html).toContain(`class="${HEADER_SLOT} ${HEADER_SLOT_END} lg:contents"`);
  });

  it("is as before without one", () => {
    const html = header();
    expect(html).not.toContain("justify-center lg:hidden");
    expect(html).not.toContain("max-lg:hidden");
    expect(html).not.toContain("max-lg:min-w-16");
  });

  it("leaves the title room on a phone: no state pill, 同空间 only from ⋯ (#3576)", () => {
    const html = header();
    expect(html).toMatch(/<span class="hidden @min-\[800px\]:contents"><span[^>]*>.*?需要你/);
    // the "Same space" button is there from sm up, gone below
    expect(html).toMatch(/<button[^>]*aria-label="同空间 1"[^>]*class="[^"]*\bmax-sm:hidden\b/);
    expect(html).toContain("<span>同空间</span>");
  });

  it("marks ⋯ on a phone when a sibling needs you, as the 同空间 button does wider", () => {
    expect(header()).toMatch(/aria-label="更多"[^]*?<span aria-hidden="true" class="[^"]*bg-attention[^"]*sm:hidden"/);
    const calm = { ...sibling, state: "idle" } as Session;
    const quiet = { ...session, state: "idle" } as Session;
    expect(header({ session: quiet, siblings: [quiet, calm] })).not.toMatch(/bg-attention[^"]*sm:hidden/);
  });
});

describe("spaceAgentsItem, ⋯'s 同空间 row on a phone (#3576)", () => {
  it("counts the others, opens the space's menu and marks attention when one needs you", () => {
    let opened = 0;
    const item = spaceAgentsItem([sibling], () => opened++);
    expect(item.label).toBe("同空间 1");
    expect(item.icon).toBe("layers");
    expect(item.state).toBe("needs_you");
    void item.onSelect();
    expect(opened).toBe(1);
    expect(spaceAgentsItem([{ ...sibling, state: "working" } as Session], () => {}).state).toBeUndefined();
  });
});

describe("Header, the 终端 | 对话 switch (#2419)", () => {
  it("has the two views in that order, the one showing checked", () => {
    const html = header({ view: "chat", onView: () => {} });
    expect(html).toMatch(/role="radiogroup" aria-label="终端或对话"/);
    expect(html.indexOf("终端</button>")).toBeLessThan(html.indexOf("对话</button>"));
    expect(html).toMatch(/aria-checked="true"[^>]*>对话</);
    expect(html).toMatch(/aria-checked="false"[^>]*>终端</);
    expect(header({ view: "term", onView: () => {} })).toMatch(/aria-checked="true"[^>]*>终端</);
  });
  it("is not there without a terminal to switch to, and the old terminal button is gone", () => {
    expect(header()).not.toContain("终端或对话");
    // the only "Terminal" button is the phone's one-button switch below
    expect(header({ view: "chat", onView: () => {} }).match(/aria-label="终端"/g)).toHaveLength(1);
    expect(header()).not.toContain('aria-label="终端"');
  });

  it("is one button to the other view on a phone, the two-part switch from sm up (#3576)", () => {
    const chat = header({ view: "chat", onView: () => {} });
    expect(chat).toMatch(/<span class="hidden sm:contents"><div[^>]*role="radiogroup" aria-label="终端或对话"/);
    expect(chat).toMatch(/<span class="relative flex shrink-0 sm:hidden"><button[^>]*aria-label="终端"/);
    const term = header({ view: "term", onView: () => {} });
    expect(term).toMatch(/<span class="relative flex shrink-0 sm:hidden"><button[^>]*aria-label="对话"/);
    expect(header({ view: "term", onView: () => {}, viewDot: true })).toMatch(/aria-label="对话"[^]*?<\/button><span aria-hidden="true" class="pointer-events-none absolute[^"]*bg-attention"/);
  });
  it("marks 对话 when the terminal shows and something wants an answer", () => {
    expect(header({ view: "term", onView: () => {}, viewDot: true })).toMatch(/对话<span aria-hidden="true" class="absolute/);
    expect(header({ view: "term", onView: () => {} })).not.toMatch(/对话<span/);
  });
});

const ix = (id: string, kind: Interaction["kind"], extra: Partial<Interaction> = {}) =>
  ({ id, machine_id: "m", session_id: "s", kind, tool_name: "Bash", ...extra }) as Interaction;
const three = [ix("a", "plan", { plan: "## 计划" }), ix("b", "question", { questions: [{ question: "用哪个框架？", options: [{ label: "A" }] }] as Interaction["questions"] }), ix("c", "permission")];
const cards = (o: { narrow: boolean; keyboard?: boolean; interactions?: Interaction[]; answered?: AnsweredCard[]; offer?: boolean }) =>
  renderToStaticMarkup(<PendingCards interactions={o.interactions ?? three} answered={o.answered ?? []} offerNext={o.offer ? { mid: "m", sid: "s" } : undefined} narrow={o.narrow} keyboard={Boolean(o.keyboard)} onDismissKeyboard={() => {}} />);

describe("PendingCards", () => {
  it("uses attention marks and neutral surfaces for questions, plans and permissions", () => {
    const html = cards({ narrow: false });
    expect(html).toContain("text-attention");
    expect(html).toContain("bg-surface");
    expect(html).not.toMatch(/\b(?:bg-red|bg-red-tint|text-red)\b/);
    expect(html).not.toContain("bg-danger-fill");
  });
  it("keeps the attention mark when the pending cards fold", () => {
    const html = cards({ narrow: true, keyboard: true });
    expect(html).toContain("bg-attention");
    expect(html).not.toContain("bg-red");
  });
  it("says what an answered card was about when its answer is one word (允许 / 批准)", () => {
    const perm = ix("p", "permission", { tool_input: { command: "swift test" } });
    const html = cards({ narrow: false, interactions: [], answered: [{ ix: perm, resolution: { allow: true } }] });
    expect(html).toContain("data-answered-about");
    expect(html).toContain("swift test");
    expect(html).toContain("允许");
    // a question's lines already carry what was asked
    const q = ix("q", "question", { questions: [{ question: "用哪个框架？", options: [{ label: "A" }] }] as Interaction["questions"] });
    expect(cards({ narrow: false, interactions: [], answered: [{ ix: q, resolution: { answers: { "用哪个框架？": "A" } } }] })).not.toContain("data-answered-about");
    // a plan says its first line, not "Plan awaiting approval"; a bare one says nothing extra
    const plan = ix("l", "plan", { plan: "\n## 改用 **vitest**\n1. 换配置" });
    const planHtml = cards({ narrow: false, interactions: [], answered: [{ ix: plan, resolution: { approve: true } }] });
    expect(planHtml).toContain("改用 vitest");
    expect(planHtml).not.toContain("计划等你审批");
    expect(cards({ narrow: false, interactions: [], answered: [{ ix: ix("l2", "plan"), resolution: { approve: true } }] })).not.toContain("data-answered-about");
    // a terminal dialog with nothing to say: no "Terminal needs you" above its Yes
    const dialog = ix("d", "screen", { dialog: { options: [{ key: "1", label: "Yes" }] } as Interaction["dialog"] });
    const dialogHtml = cards({ narrow: false, interactions: [], answered: [{ ix: dialog, resolution: { option: "1" } }] });
    expect(dialogHtml).not.toContain("data-answered-about");
    expect(dialogHtml).not.toContain("终端等你处理");
    // handed back (#2450): nothing was decided, so no answer line repeats the head; the line says which request went back
    const handed = cards({ narrow: false, interactions: [], answered: [{ ix: perm, resolution: { dismiss: true } }] });
    expect(handed).toContain("data-answered-about");
    expect(handed).not.toContain("交回电脑处理");
  });

  it("says which subagent asks for a permission", () => {
    const html = cards({ narrow: false, interactions: [ix("p", "permission", { agent_id: "a1", agent_type: "code-reviewer", tool_input: { command: "npm test" } })] });
    expect(html).toContain("data-permission-from");
    expect(html).toContain("来自子 Agent · code-reviewer");
    expect(cards({ narrow: false, interactions: [ix("p", "permission")] })).not.toContain("data-permission-from");
    // the main session's own card: `claude --agent reviewer` sets agent_type with no agent_id
    expect(cards({ narrow: false, interactions: [ix("p", "permission", { agent_type: "reviewer" })] })).not.toContain("data-permission-from");
    expect(cards({ narrow: false, interactions: [ix("p", "permission", { agent_id: "a1", agent_type: "main" })] })).not.toContain("data-permission-from");
  });

  it("keeps the subagent's type to one short line", () => {
    expect(permissionFrom({ agent_id: "a1", agent_type: "  code\n  reviewer  " })).toBe("来自子 Agent · code reviewer");
    const long = permissionFrom({ agent_id: "a1", agent_type: "x".repeat(80) });
    expect(long).toBe(`来自子 Agent · ${"x".repeat(47)}…`);
    const html = cards({ narrow: false, interactions: [ix("p", "permission", { agent_id: "a1", agent_type: "code-reviewer" })] });
    expect(html).toMatch(/data-permission-from="true" title="来自子 Agent · code-reviewer" class="truncate/);
  });

  it("shows every card on a wide screen, in no box of their own", () => {
    const html = cards({ narrow: false });
    expect(html).not.toContain("data-chat-cards");
    expect(html).not.toContain("还有");
    expect(html).toContain("计划等你审批");
    expect(html).toContain("用哪个框架？");
    expect(html).toContain("允许使用 Bash？");
  });

  it("scrolls them in a box of their own on a narrow one, the first open and 还有 N 件 for the rest", () => {
    const html = cards({ narrow: true });
    expect(html).toMatch(/data-chat-cards[^>]*overflow-y-auto/);
    expect(html).toContain("还有 2 件");
    // the first card's fold is open, the others' closed (inert)
    const folds = [...html.matchAll(/<div class="grid [^"]*"([^>]*)>/g)].map((m) => m[1]);
    expect(folds).toHaveLength(2);
    expect(folds[0]).not.toContain("inert");
    expect(folds[1]).toContain("inert");
  });

  it("folds them into one line while the keyboard is up", () => {
    const html = cards({ narrow: true, keyboard: true });
    expect(html).toContain("3 件待处理");
    expect(html).not.toContain("还有");
    const folds = [...html.matchAll(/<div class="grid [^"]*"([^>]*)>/g)].map((m) => m[1]);
    expect(folds.every((f) => f.includes("inert"))).toBe(true);
  });

  it("is one line on a phone on its side", () => {
    const html = renderToStaticMarkup(<PendingCards interactions={three} answered={[]} narrow short keyboard={false} onDismissKeyboard={() => {}} />);
    expect(html).toContain("3 件待处理");
    expect(html).not.toContain("还有");
  });

  it("is nothing without cards", () => {
    expect(cards({ narrow: true, interactions: [] })).toBe("");
  });

  // #3112: on a phone and in a side panel the open cards sit in folds; a fold that still clipped once open was the box
  // a card's sticky buttons stuck in, so a card taller than the cards' box (a long command) had its Allow / Deny below
  // the fold, out of sight. The layout itself is checked in a browser (scripts/test/web_card_scroll_e2e.py).
  it("doesn't clip the open cards' folds on a narrow screen, so a tall card's buttons stick in view (#3112)", () => {
    const long = ix("p", "permission", { tool_input: { command: "python3 -c \"" + "print(1)\n".repeat(80) + "\"" } });
    const html = renderToStaticMarkup(<PendingCards interactions={[long, three[1]]} answered={[]} narrow keyboard={false} onDismissKeyboard={() => {}} />);
    const box = html.slice(html.indexOf("data-chat-cards"));
    // the first card's fold is open: its clip box is not a scroller (no overflow-hidden), the card's footer sticky
    const first = box.match(/<div class="grid [^"]*"[^>]*><div class="([^"]*)"/);
    expect(first?.[1]).toBeDefined();
    expect(first![1].split(/\s+/)).not.toContain("overflow-hidden");
    expect(box).toMatch(/data-card-actions="[^"]*" class="sticky -bottom-1 /);
    // the folded one (2 more) still clips: it is closed
    const folds = [...box.matchAll(/<div class="grid [^"]*"[^>]*><div class="([^"]*)"/g)].map((m) => m[1]);
    expect(folds).toHaveLength(2);
    expect(folds[1]).toContain("overflow-hidden");
  });
});

describe("PendingCards, the next card waiting elsewhere (#4091)", () => {
  const answered: AnsweredCard[] = [{ ix: ix("done", "question"), resolution: { answers: { q: "A" } } }];
  const other = ix("other", "question", { machine_id: "n", session_id: "t", session_title: "照片同步脚本支持 HEIC", questions: [{ question: "保存为哪种格式？", options: [] }] as Interaction["questions"] });
  // the store's cards: two of other chats, one of this chat (left out of the count)
  const seed = (list: Interaction[]) => Object.assign(getState(), { interactions: list, sessions: {} });
  afterEach(() => seed([]));

  it("says how many, which is next and what it asks under 已回答, linking to that chat", () => {
    seed([ix("mine", "permission"), other, ix("more", "permission", { machine_id: "n", session_id: "u" })]);
    for (const narrow of [false, true]) {
      const html = cards({ narrow, interactions: [], answered, offer: true });
      expect(html).toContain("已回答");
      expect(html).toContain("还有 2 件等你");
      expect(html).toContain("下一件：照片同步脚本支持 HEIC · 保存为哪种格式？");
      expect(html).toMatch(/<a href="[^"]*\/s\/n\/t"[^>]*data-chat-next-waiting/);
    }
  });
  it("is not there while this chat still has a card, before anything is answered here, when not offered, or with nothing elsewhere", () => {
    seed([other]);
    expect(cards({ narrow: true, interactions: [three[2]], answered, offer: true })).not.toContain("data-chat-next-waiting");
    expect(cards({ narrow: true, interactions: [], answered: [], offer: true })).toBe("");
    expect(cards({ narrow: false, interactions: [], answered })).not.toContain("data-chat-next-waiting");
    seed([ix("mine", "permission")]);
    expect(cards({ narrow: false, interactions: [], answered, offer: true })).not.toContain("data-chat-next-waiting");
  });
});

describe("Composer", () => {
  afterEach(() => vi.unstubAllGlobals());
  it("caps the text box by the visible height", () => {
    vi.stubGlobal("localStorage", { getItem: () => null });
    const html = renderToStaticMarkup(<Composer draftKey="t" placeholder="给 Agent 发消息…" onSend={async () => true} />);
    expect(html).toMatch(/<textarea[^>]*style="max-height:min\(240px, calc\(var\(--kb-h\) \* 0.35\)\)"/);
  });
});

describe("tap areas", () => {
  afterEach(() => vi.unstubAllGlobals());
  const hit = (html: string, label: string) => new RegExp(`<button[^>]*aria-label="${label}"[^>]*class="touch-hit `).test(html) || new RegExp(`<button[^>]*class="touch-hit [^"]*"[^>]*aria-label="${label}"`).test(html);

  it("covers the composer's row: + @ / and send", () => {
    vi.stubGlobal("localStorage", { getItem: () => null });
    const html = renderToStaticMarkup(<Composer draftKey="t" placeholder="…" machineId="m" mention={{ self: "t", files: () => [] }} onSend={async () => true} />);
    for (const label of ["添加图片或文件", "引用文件或会话", "命令", "发送"]) expect(hit(html, label), label).toBe(true);
  });

  it("covers a card's hand back, a question's options and its arrows", () => {
    const html = cards({ narrow: false });
    expect(hit(html, "交给电脑处理")).toBe(true);
    expect(html).toMatch(/<button[^>]*aria-pressed="false"[^>]*class="touch-hit /);
    expect(hit(html, "下一题") || hit(html, "发送回答")).toBe(true);
  });

  it("covers an attachment's remove button", () => {
    const html = renderToStaticMarkup(<AttachmentTray attachments={[{ id: "a", name: "a.png", url: "blob:a" }]} onRemove={() => {}} />);
    expect(hit(html, "移除 a.png")).toBe(true);
    // its tile does not clip the area
    expect(html).not.toMatch(/group\/att relative overflow-hidden/);
  });
});

describe("QueueStrip", () => {
  const queued = { ...session, queued: ["顺便把截图换掉", "跑一遍 UI 测试"] } as Session;
  it("is one line that opens the messages on a phone on its side", () => {
    const html = renderToStaticMarkup(<QueueStrip session={queued} draftKey="t" canEdit compact />);
    expect(html).toContain('aria-expanded="false"');
    expect(html).toContain("排队中 2 条");
    expect(html).toMatch(/<div class="grid [^"]*"[^>]*inert/);
  });

  it("starts folded everywhere, the opened list capped and scrolling", () => {
    const html = renderToStaticMarkup(<QueueStrip session={queued} draftKey="t" canEdit />);
    expect(html).toContain('aria-expanded="false"');
    expect(html).toMatch(/<div class="grid [^"]*"[^>]*inert/);
    expect(html).toMatch(/max-h-\[120px\][^"]*overflow-y-auto/);
    expect(html).toContain("跑一遍 UI 测试");
  });

  it("gives a Claude's queue Send now and its next-step wording; other agents neither (#6088)", () => {
    const claude = renderToStaticMarkup(<QueueStrip session={{ ...queued, agent: "claude" }} draftKey="t" canEdit />);
    expect(claude).toContain("data-send-now");
    expect(claude).toContain("下一步就会依次接收");
    const codex = renderToStaticMarkup(<QueueStrip session={{ ...queued, agent: "codex" }} draftKey="t" canEdit />);
    expect(codex).not.toContain("data-send-now");
    expect(codex).toContain("忙完会依次处理");
    expect(renderToStaticMarkup(<QueueStrip session={{ ...queued, agent: "claude" }} draftKey="t" canEdit={false} />)).not.toContain("data-send-now");
  });

  it("in a Persona chat offers Send now for the user's own messages, in the persona's name; relayed ones alone get none (#4342)", () => {
    const persona = { ...queued, agent: undefined, assistant: true, queued: ["顺便把截图换掉", "来自「日报」：今天的汇总"], queued_relayed: 1 } as Session;
    const html = renderToStaticMarkup(<QueueStrip session={persona} draftKey="t" canEdit persona="Nova" />);
    expect(html).toContain("data-send-now");
    expect(html).toContain("Nova 下一步就会依次接收");
    const relayedOnly = renderToStaticMarkup(<QueueStrip session={{ ...persona, queued_relayed: 2 }} draftKey="t" canEdit persona="Nova" />);
    expect(relayedOnly).not.toContain("data-send-now");
    expect(relayedOnly).toContain("Nova 忙完会依次处理");
    // the relayed one says it arrives when the turn ends; the user's own don't
    expect(html.match(/data-queue-note/g)).toHaveLength(1);
    expect(html).toContain('aria-label="排队的第 2 条：来自「日报」：今天的汇总 · 对话空闲后送达"');
  });

  it("marks the photos a Persona chat's queued message went with when this browser sent it (#4342)", () => {
    const persona = { ...queued, assistant: true, queued: ["请看图片", "顺便把截图换掉"] } as Session;
    const html = renderToStaticMarkup(<QueueStrip session={persona} draftKey="t" canEdit persona="Nova" photosOf={(q) => (q === "请看图片" ? 2 : 0)} />);
    expect(html.match(/data-queue-mark="photo"/g)).toHaveLength(1);
    expect(html).toContain('aria-label="排队的第 1 条：2 张图片"');
    expect(html).not.toContain(">请看图片<");
  });

  // made-up words and paths, in the shape Claude Code queues a message sent with photos (#3032)
  const photo = (name: string) => `/Users/me/.persona/uploads/2026-11-10/${name}.jpg`;
  const withPhotos = `[Image #1] [Image #2]\n\n\n把这两张截图对一下\n<attached_images>\npath: ${photo("a1")}\npath: ${photo("b2")}\n</attached_images>`;

  it("shows a queued message's words and a mark per kind of attachment, never Claude's queue text (#3032)", () => {
    const photoOnly = `[Image #3] 请看图片\n\n<attached_images>\npath: ${photo("c3")}\n</attached_images>`;
    const html = renderToStaticMarkup(<QueueStrip session={{ ...session, queued: [withPhotos, photoOnly] } as Session} draftKey="t" canEdit />);
    expect(html).toContain("把这两张截图对一下");
    expect(html).not.toContain("[Image #");
    expect(html).not.toContain("attached_images");
    expect(html).not.toContain("path: ");
    expect(html).not.toContain("请看图片");
    expect(html.match(/data-queue-mark="photo"/g)).toHaveLength(2);
    // the label says what came with it
    expect(html).toContain('aria-label="排队的第 1 条：把这两张截图对一下 · 2 张图片"');
    expect(html).toContain('aria-label="排队的第 2 条：1 张图片"');
  });
});

describe("restoreQueued (#3032)", () => {
  const photo = "/Users/me/.persona/uploads/2026-11-10/a1.jpg";
  const gone = "/Users/me/.persona/uploads/2026-11-10/gone.jpg";
  afterEach(() => {
    vi.unstubAllGlobals();
    for (const c of getContexts("rq")) removeContext("rq", c.id);
  });

  it("puts the words in the box, the photos in the tray, the contexts on their chips and a lost photo in as its path", async () => {
    const urls: string[] = [];
    vi.stubGlobal("localStorage", { getItem: () => "tok", setItem: () => {}, removeItem: () => {} });
    vi.stubGlobal("fetch", async (url: string) => {
      urls.push(url);
      return url.includes(encodeURIComponent(photo)) ? new Response(new Blob([new Uint8Array([1, 2, 3])], { type: "image/jpeg" })) : new Response("", { status: 404 });
    });
    const events: DraftEvent[] = [];
    const off = onDraftEvent("rq", (e) => void events.push(e));
    try {
      const workspace = { kind: "workspace" as const, title: "web", url: "", text: "~/code/web", attrs: { machine: "laptop", cwd: "/home/me/code/web" } };
      const raw = `${withContexts([workspace], "Fix the header", true)} <persona-file kind="file" path="/tmp/spec.pdf" name="spec.pdf"/>\n\n<attached_images>\npath: ${photo}\npath: ${gone}\n</attached_images>`;
      await restoreQueued("rq", session, parseQueued(raw));
      expect(urls).toHaveLength(2);
      expect(urls[0]).toContain("/machines/m/file/download?path=");
      expect(events).toEqual([
        { type: "append", text: "Fix the header /tmp/spec.pdf" },
        { type: "image", image: { name: "a1.jpg", mime: "image/jpeg", data: "AQID" } },
        { type: "append", text: gone },
      ]);
      expect(getContexts("rq").map(({ kind, title, text, attrs }) => ({ kind, title, text, attrs }))).toEqual([{ kind: "workspace", title: "web", text: "~/code/web", attrs: workspace.attrs }]);
    } finally {
      off();
    }
  });
});

describe("restoreQueued in a Persona chat (#4342)", () => {
  it("puts the photos that came back with the message in the tray, without the stand-in words", async () => {
    const events: DraftEvent[] = [];
    const off = onDraftEvent("rqp", (e) => void events.push(e));
    try {
      const images = [{ mime: "image/png", data: "AQID" }];
      await restoreQueued("rqp", session, parseQueued("请看图片", images.length), images);
      expect(events).toEqual([{ type: "image", image: { name: "photo-1.png", mime: "image/png", data: "AQID" } }]);
    } finally {
      off();
    }
  });
});

