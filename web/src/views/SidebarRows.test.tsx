// The open conversation's row in the sidebar (#2283): marked for assistive tech, its own fill, its actions without hover.
import { afterEach, describe, expect, it, vi } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import type { AgentSession } from "../lib/agents";
import type { Session } from "../lib/types";
import { setAgentDetails, setContextAsUsed } from "../lib/context-usage";
import { AgentRow, PersonaRow, SessionRow } from "./Sidebar";
import { conversationMenuItems } from "./ConversationMenu";

// the sidebar's modules read the colour scheme and this browser's settings when they load
vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
  const kept = new Map<string, string>();
  g.localStorage = { getItem: (k: string) => kept.get(k) ?? null, setItem: (k: string, v: string) => void kept.set(k, v), removeItem: (k: string) => void kept.delete(k) };
});

// the avatar reads the persona store, which has no server snapshot
vi.mock("./assistant/Persona", async (importOriginal) => ({ ...(await importOriginal<typeof import("./assistant/Persona")>()), PersonaAvatar: () => null }));

const session: Session = { machine_id: "mac", session_id: "s1", title: "整理文档", cwd: "/code", status: "idle", state: "idle" };
const rowProps = { s: session, k: "s:mac/s1", reg: () => () => {}, hover: () => {}, to: { name: "session", mid: "mac", sid: "s1" } as const, go: () => () => {}, onMenu: () => {} };
const link = (html: string) => html.match(/<a [^>]*>/)![0];
const more = (html: string) => html.match(/<button [^>]*aria-label="Agent 的操作"[^>]*>/)![0];

describe("an agent's row", () => {
  it("is aria-current with the open row's fill when it is open", () => {
    const html = renderToStaticMarkup(<SessionRow {...rowProps} active />);
    expect(link(html)).toContain('aria-current="page"');
    expect(link(html)).toMatch(/class="[^"]*\bbg-hover-2\b/);
  });
  it("is neither when it is not", () => {
    const html = renderToStaticMarkup(<SessionRow {...rowProps} active={false} />);
    expect(link(html)).not.toContain("aria-current");
    expect(link(html)).not.toContain("bg-hover-2");
  });
  it("keeps its ⋯ shown when open, on hover only otherwise", () => {
    expect(more(renderToStaticMarkup(<SessionRow {...rowProps} active />))).toMatch(/class="[^"]*\bflex\b/);
    expect(more(renderToStaticMarkup(<SessionRow {...rowProps} active={false} />))).toMatch(/class="[^"]*\bhidden\b/);
  });
});

describe("where an agent was opened, as a mark at its row's end (#2526)", () => {
  const render = (patch: Partial<Session>, extra = {}) => renderToStaticMarkup(<SessionRow {...rowProps} s={{ ...session, ...patch }} active={false} {...extra} />);
  const mark = (html: string) => html.match(/<span data-source="[^"]*"[^>]*>.*?<\/span>/)?.[0];
  it("draws a terminal pane's as the terminal icon, named but without words", () => {
    const html = render({ pane_id: "w1:p1" });
    expect(mark(html)).toMatch(/aria-label="终端"/);
    expect(mark(html)).toMatch(/title="终端"/);
    expect(mark(html)).toContain("text-source-terminal");
    expect(mark(html)).toContain("<svg");
    expect(mark(html)).not.toMatch(/>终端</); // no visible word any more
    expect(link(html)).toMatch(/title="整理文档\n终端/); // the row's tooltip names it too: the mark hides while hovered
  });
  it("draws a desktop app's own chat as a window, in its own color", () => {
    for (const patch of [{ space_kind: "codex_app", source_kind: "codex_history" }, { host: "paseo", source_kind: "runtime", source_instance_id: "paseo" }]) {
      const html = render(patch);
      expect(mark(html)).toMatch(/aria-label="桌面端"/);
      expect(mark(html)).toContain("text-source-desktop");
      expect(mark(html)).not.toContain("text-source-terminal");
    }
  });
  it("draws Lark's mark for a Lark group, a link to its topic when it has one", () => {
    expect(mark(render({ pane_id: "botmux:oc_1/t1" }))).toMatch(/^<span data-source="feishu" role="img"[^>]*aria-label="飞书群"/);
    const linked = mark(render({ pane_id: "botmux:oc_1/t1", external_url: "https://example.com/topic" }));
    expect(linked).toMatch(/^<span data-source="feishu" role="link" tabindex="0"[^>]*aria-label="飞书群 · 在飞书里打开这个话题"/);
    expect(linked).toContain("<img");
  });
  it("draws none for other sources, Persona's conversations and a tab with several panes", () => {
    expect(mark(render({}))).toBeUndefined();
    expect(mark(render({ source_kind: "runtime", source_instance_id: "dsh" }))).toBeUndefined();
    expect(mark(render({ pane_id: "w1:p1", assistant: true }))).toBeUndefined();
    expect(mark(render({ pane_id: "w1:p1" }, { endRoom: true }))).toBeUndefined();
    expect(link(render({ pane_id: "w1:p1" }, { endRoom: true }))).not.toMatch(/\n终端/);
  });
  it("steps aside for the hover ⋯ like the rest of the row's end, and stays on the open row", () => {
    expect(mark(render({ pane_id: "w1:p1" }))).toContain("group-hover/row:invisible");
    expect(mark(renderToStaticMarkup(<SessionRow {...rowProps} s={{ ...session, pane_id: "w1:p1" }} active />))).not.toContain("invisible");
  });
  it("tells terminal and desktop apart by shape too: a bare prompt, and a framed window", () => {
    const svg = (html: string) => mark(html)!.match(/<svg[^>]*>(.*?)<\/svg>/)![1];
    const terminal = svg(render({ pane_id: "w1:p1" }));
    const desktop = svg(render({ space_kind: "codex_app", source_kind: "codex_history" }));
    expect(terminal).not.toContain("<rect"); // no frame: a prompt chevron and an underscore
    expect(desktop).toContain("<rect");
    expect(terminal).not.toBe(desktop);
  });
  it("drops a writable desktop runtime's name from the row's end, where its marks already say it", () => {
    const paseo = { host: "paseo", source_kind: "runtime", source_instance_id: "paseo", agent_label: "Paseo" };
    const writable = render({ ...paseo, capabilities: { read: true, send: true } });
    expect(writable).not.toMatch(/>Paseo</);
    expect(link(writable)).toContain("Paseo"); // still in the row's tooltip
    // read-only: the end still says why
    expect(render({ ...paseo, capabilities: { read: true, send: false } })).toMatch(/>[^<]*Paseo[^<]*</);
  });
  it("keeps the title's width first when words sit at the row's end", () => {
    const html = render({ space_kind: "codex_app", source_kind: "codex_history", agent_name: "Codex App" });
    expect(html).toMatch(/<span class="[^"]*flex-1 truncate[^"]*basis-auto[^"]*">整理文档<\/span>/);
    expect(html).toMatch(/<span class="[^"]*min-w-6 shrink-\[20\][^"]*">[^<]*Codex/);
    expect(render({ pane_id: "w1:p1" })).not.toContain("basis-auto"); // nothing at the end: as before
  });
});

describe("an agent's row with 「Agent 行显示详情」 (#2430)", () => {
  const detailed: Session = { ...session, model: "claude-opus-5-5", context_tokens: 78000, context_window: 200000 };
  const render = (s: Session, extra = {}) => renderToStaticMarkup(<SessionRow {...rowProps} s={s} active={false} {...extra} />);
  afterEach(() => {
    setAgentDetails(false);
    setContextAsUsed(false);
  });
  it("is byte for byte the same as before while the setting is off", () => {
    expect(render(detailed)).toBe(render(session)); // the model and context change nothing
    expect(render(detailed)).not.toContain("opus 5.5");
  });
  it("adds a second line with folder, model and what is left when on", () => {
    setAgentDetails(true);
    const html = render(detailed);
    expect(html).toContain("opus 5.5");
    expect(html).toContain("61%");
    expect(html).toContain("code");
  });
  it("shows what is used with 「上下文显示为已用」 and warns at 80%", () => {
    setAgentDetails(true);
    setContextAsUsed(true);
    expect(render(detailed)).toContain("39%");
    expect(render({ ...detailed, context_tokens: 170000 })).toContain("text-orange");
    const danger = render({ ...detailed, context_tokens: 185000 });
    expect(danger).toContain("text-orange");
    expect(danger).not.toContain("text-red");
  });
  it("draws no second line for a session that says nothing, or for a conversation of the assistant", () => {
    setAgentDetails(true);
    const plain = { ...session, cwd: "" };
    expect(render(plain)).not.toContain("flex-col");
    expect(render(detailed, { showState: false })).not.toContain("opus 5.5");
  });
});

describe("a personal agent's row", () => {
  const s = { id: "a1", title: "周报助手", provider: "lark", unreadCount: 0, rankTimeMs: 0 } as AgentSession;
  const props = { s, reg: () => () => {}, hover: () => {}, go: () => () => {}, pinned: false, onMenu: () => {} };
  const pin = (html: string) => html.match(/<button [^>]*aria-label="置顶"[^>]*>/)![0];
  it("is aria-current with the open row's fill and its pin shown when open", () => {
    const html = renderToStaticMarkup(<AgentRow {...props} active />);
    expect(link(html)).toContain('aria-current="page"');
    expect(link(html)).toMatch(/class="[^"]*\bbg-hover-2\b/);
    expect(pin(html)).toMatch(/class="[^"]*\bopacity-100\b/);
  });
  it("shows its pin on hover only otherwise", () => {
    const html = renderToStaticMarkup(<AgentRow {...props} active={false} />);
    expect(link(html)).not.toContain("aria-current");
    expect(pin(html)).toMatch(/class="[^"]*\bopacity-0\b/);
  });
});

describe("Persona's main row", () => {
  const main: Session = { machine_id: "hub", session_id: "main", title: "Persona", cwd: "/", status: "idle", state: "idle", assistant: true };
  const props = { reg: () => () => {}, hover: () => {}, go: () => () => {}, main };
  it("has a ⋯ for the conversation's menu when the main conversation is there", () => {
    const html = renderToStaticMarkup(<PersonaRow {...props} active={false} onMenu={() => {}} />);
    expect(html).toMatch(/<button [^>]*aria-label="对话的操作"[^>]*aria-haspopup="menu"/);
  });
  it("draws no ⋯ without it", () => {
    expect(renderToStaticMarkup(<PersonaRow {...props} main={undefined} active={false} />)).not.toContain("对话的操作");
  });
  it("whose menu has only 拷贝会话 ID and 结束…", () => {
    expect(conversationMenuItems(main, { main: true }).map((i) => i.key)).toEqual(["copy", "end"]);
  });
});
