// The phone shell's views (#1996), rendered statically: "Assistant | Agents", the drawer's page grid, the pane tabs, the ⋯ of
// a sidebar row and the search's key hints.
import { describe, expect, it, vi } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import type { Session } from "../lib/types";
import { agentRowMenuItems } from "./AgentMenus";
import { AgentsPage } from "./AgentsPage";
import { paneTabsFit, PaneTabs } from "./chat/PaneTabs";
import { Palette } from "./Palette";
import { ShellSwitchView } from "./ShellSwitch";
import { NavGrid, navGridColumns, type NavPage } from "./Sidebar";

// the sidebar's modules read the colour scheme and this browser's settings when they load
vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
  const kept = new Map<string, string>();
  g.localStorage = { getItem: (k: string) => kept.get(k) ?? null, setItem: (k: string, v: string) => void kept.set(k, v), removeItem: (k: string) => void kept.delete(k) };
});

vi.mock("react-dom", async (importOriginal) => ({
  ...(await importOriginal<typeof import("react-dom")>()),
  createPortal: (children: React.ReactNode) => children,
}));

const session = (o: Partial<Session> = {}): Session => ({ machine_id: "mac", session_id: "s1", title: "整理文档", cwd: "/code", status: "idle", state: "idle", ...o });

describe("「助手 | Agents」", () => {
  it("marks the page it is on and links to the other", () => {
    const html = renderToStaticMarkup(<ShellSwitchView page="assistant" waiting={0} />);
    expect(html).toMatch(/<a href="\/" aria-current="page" aria-label="助手"/);
    expect(html).toMatch(/<a href="\/terminal" aria-label="会话与终端"/);
    expect(renderToStaticMarkup(<ShellSwitchView page="agents" waiting={0} />)).toMatch(/href="\/terminal" aria-current="page"/);
  });
  it("carries the agents waiting for you on its Agents side, none at 0", () => {
    const html = renderToStaticMarkup(<ShellSwitchView page="assistant" waiting={3} />);
    expect(html).toContain('aria-label="会话与终端，3 个需要你"');
    expect(html).toMatch(/text-attention[^"]*">3<\/span>/);
    expect(renderToStaticMarkup(<ShellSwitchView page="assistant" waiting={0} />)).not.toContain("bg-red");
  });
  it("has a 44 hit area and single-line labels", () => {
    const html = renderToStaticMarkup(<ShellSwitchView page="assistant" waiting={0} />);
    expect(html.match(/touch-hit/g)).toHaveLength(2);
    expect(html.match(/whitespace-nowrap/g)).toHaveLength(2);
  });
  it("takes no more room than the bar gives it (#5036): on a phone the short Sessions label, truncating last", () => {
    const html = renderToStaticMarkup(<ShellSwitchView page="assistant" waiting={2} />);
    // the switch may shrink, so the side slots' buttons never go under it
    expect(html).toMatch(/<nav aria-label="切换页面" class="flex h-8 max-w-full min-w-0 /);
    expect(html.match(/min-w-0 [^"]*px-2 [^"]*sm:shrink-0 sm:px-3\.5/g)).toHaveLength(2);
    expect(html.match(/<span class="min-w-0 truncate" data-switch-label="true">/g)).toHaveLength(2);
    // Sessions: the short name on a phone, the full one wider, the full one always its name and tooltip
    expect(html).toContain('<span class="sm:hidden">会话</span><span class="max-sm:hidden">会话与终端</span>');
    expect(html).toMatch(/<a href="\/terminal" aria-label="会话与终端，2 个需要你" title="会话与终端"/);
    // the assistant side has no short name and no tooltip
    expect(html).toMatch(/<a href="\/" aria-current="page" aria-label="助手" class=/);
    // the count: inline when wide, on the segment's corner on a phone
    expect(html).toMatch(/max-sm:absolute max-sm:-top-1 max-sm:-right-1">2<\/span>/);
  });
});

describe("the Agents page", () => {
  it("puts 侧边栏 and its button in ChatView's side slots, the switch narrow only, a title wide", () => {
    const html = renderToStaticMarkup(<AgentsPage route={{ name: "agent-list" }} onToggleSidebar={() => {}} onNewAgent={() => {}} />);
    const header = html.slice(0, html.indexOf("</header>"));
    // left slot, centre, right slot in that order, both slots at least one width on a narrow screen, growing to fit (#5036)
    expect(header.match(/max-lg:min-w-16/g)).toHaveLength(2);
    expect(header.indexOf('aria-label="侧边栏"')).toBeLessThan(header.indexOf('aria-label="切换页面"'));
    expect(header.indexOf('aria-label="切换页面"')).toBeLessThan(header.indexOf('aria-label="新建 Agent"'));
    expect(header).toMatch(/flex min-w-0 flex-1 justify-center lg:hidden"><nav aria-label="切换页面"/);
    expect(header).toMatch(/hidden[^"]*lg:block">Agents<\/h1>/);
  });
});

describe("the drawer's page grid", () => {
  const pages: NavPage[] = [
    { k: "home", to: { name: "home" }, icon: "home", label: "总览", badge: 2, badgeTone: "red" },
    { k: "workspace-board", to: { name: "workspace-board" }, icon: "layers", label: "工作流看板", short: "看板" },
    { k: "settings", to: { name: "settings" }, icon: "gear", label: "设置" },
  ];
  it("lays the pages in two even rows, at most five across", () => {
    expect(navGridColumns(9)).toBe(5);
    expect(navGridColumns(8)).toBe(4);
    expect(navGridColumns(10)).toBe(5);
    expect(navGridColumns(3)).toBe(2);
    expect(navGridColumns(1)).toBe(1);
  });
  it("is one link per page with its short name, badge and the current one marked", () => {
    const html = renderToStaticMarkup(<NavGrid pages={pages} activeKey="settings" go={() => () => {}} />);
    expect(html.match(/<a /g)).toHaveLength(3);
    expect(html).toContain(">看板</span>");
    expect(html).toContain('title="工作流看板"');
    expect(html).toContain('aria-label="总览 2"');
    expect(html).toMatch(/href="\/settings"[^>]*aria-current="page"/);
    expect(html).toMatch(/min-h-12/);
  });
});

describe("pane tabs on a phone", () => {
  const two = [session({ session_id: "a" }), session({ session_id: "b", title: "改测试" })];
  const five = ["a", "b", "c", "d", "e"].map((id) => session({ session_id: id }));
  it("up to three share the width; more scroll with faded ends", () => {
    expect(paneTabsFit(2)).toBe("fill");
    expect(paneTabsFit(3)).toBe("fill");
    expect(paneTabsFit(4)).toBe("scroll");
    const fill = renderToStaticMarkup(<PaneTabs sessions={two} current="a" onSelect={() => {}} />);
    expect(fill.match(/max-lg:flex-1 max-lg:basis-0/g)).toHaveLength(2);
    expect(fill).not.toContain("mask-image");
    const scroll = renderToStaticMarkup(<PaneTabs sessions={five} current="c" onSelect={() => {}} />);
    expect(scroll).not.toContain("max-lg:flex-1");
    expect(scroll).toContain("max-lg:[mask-image:linear-gradient(");
  });
});

describe("a sidebar row's ⋯", () => {
  const items = (s: Session) => agentRowMenuItems(s, { pinned: false, muted: false, onRename: () => {}, onDelete: () => {}, onLinkTodo: () => {} }).map((i) => i.key);
  it("has what used to be only on right click: 关联到待办, 解除关联 and 删除对话", () => {
    const offline = session({ state: "offline", status: "offline" });
    expect(items(offline)).toEqual(expect.arrayContaining(["pin", "link-todo", "delete"]));
    expect(items(offline)).not.toContain("unlink-todo");
    expect(items(session({ todo: { id: "t1", title: "发版" } } as Partial<Session>))).toEqual(expect.arrayContaining(["link-todo", "unlink-todo"]));
  });
  it("says 改挂到其他待办… on an agent that already works for a todo (#2348)", () => {
    const label = (s: Session) => agentRowMenuItems(s, { pinned: false, muted: false, onRename: () => {}, onDelete: () => {}, onLinkTodo: () => {} }).find((i) => i.key === "link-todo")?.label;
    expect(label(session())).toBe("关联到待办…");
    expect(label(session({ todo: { id: "t1", title: "发版" } } as Partial<Session>))).toBe("改挂到其他待办…");
  });
  it("offers 关闭 Agent and 实时接入 when the chat's ⋯ does (#3405)", () => {
    const keys = (s: Session, close = true) => agentRowMenuItems(s, { pinned: false, muted: false, onRename: () => {}, onDelete: () => {}, ...(close ? { onCloseAgent: () => {} } : {}) }).map((i) => i.key);
    expect(keys(session())).toContain("close");
    expect(keys(session({ state: "offline", status: "offline" }))).not.toContain("close");
    expect(keys(session({ assistant: true } as Partial<Session>))).not.toContain("close");
    expect(keys(session(), false)).not.toContain("close");
    const live = { source_kind: "codex_app_server", capabilities: { attach: true } } as Partial<Session>;
    expect(keys(session(live))).toContain("attach");
    expect(keys(session(live))).not.toContain("close"); // no terminal capability
    expect(keys(session({ ...live, attached: true } as Partial<Session>))).not.toContain("attach");
    expect(keys(session())).not.toContain("attach");
  });
  it("offers 删除对话 on a finished or seen desktop thread, which has no terminal to close it from", () => {
    const thread = { source_kind: "codex_app_server", capabilities: { read: true, send: true } } as Partial<Session>;
    for (const status of ["done", "unknown", "interrupted"]) {
      expect(items(session({ ...thread, status, state: status === "done" ? "unread" : "unknown" } as Partial<Session>))).toContain("delete");
      expect(items(session({ status, state: "idle" } as Partial<Session>))).toContain("delete");
    }
    expect(items(session({ ...thread, status: "idle", state: "idle" } as Partial<Session>))).toContain("delete");
    expect(items(session({ status: "working", state: "working" }))).not.toContain("delete");
  });
  it("leaves the todo out where the list gives no way to link one", () => {
    expect(agentRowMenuItems(session(), { pinned: false, muted: false, onRename: () => {}, onDelete: () => {} }).map((i) => i.key)).not.toContain("link-todo");
  });
});

describe("the search on a touch screen", () => {
  it("hides the key hints (↑↓ ↵ Esc) under a coarse pointer", () => {
    vi.stubGlobal("document", { body: {} }); // the portal's target (createPortal is mocked)
    const html = renderToStaticMarkup(<Palette open onClose={() => {}} onNewAgent={() => {}} />);
    vi.unstubAllGlobals();
    const esc = html.indexOf(">Esc<");
    expect(esc).toBeGreaterThan(0);
    expect(html.lastIndexOf("[@media(pointer:coarse)]:hidden", esc)).toBeGreaterThan(html.lastIndexOf("<input", esc));
    expect(html).toMatch(/\[@media\(pointer:coarse\)\]:hidden">(?:(?!<\/div>).)*↵/);
  });
});
