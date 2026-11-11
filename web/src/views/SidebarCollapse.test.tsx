// @vitest-environment jsdom
// The sidebar folds (#2524): to a rail of icons with its button or ⌘⌥B / Ctrl+Alt+B (kept across reloads), and each of
// its blocks under its title: Pages, Agents, and the groups inside Agents, each fold kept in this browser.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
// jsdom has no matchMedia (the sidebar's modules read it when they load) and no ResizeObserver (its highlight)
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
import { COLLAPSED_KEY, getLayout, setSidebarCollapsed } from "../lib/layout";
import { setAllFolded } from "../lib/sidebar-api";
import { refresh, reset } from "../lib/store";
import type { AssistantInfo, Session } from "../lib/types";
import { checkBinding, eventChord, getBindings, matchShortcut } from "../lib/shortcuts";
import { useShortcuts } from "../lib/use-shortcuts";
import { initI18n } from "../i18n";
import { NavGrid, Sidebar, SidebarRail, useNavPages } from "./Sidebar";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 5; i++) await act(async () => { await Promise.resolve(); });
};
const button = (name: string) => [...host.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.getAttribute("aria-label") === name || b.textContent?.trim() === name);
const sectionBody = (title: string) => button(title)!.closest(".mt-1, .mt-3")!.querySelector("[inert], .flex.flex-col") as HTMLElement;

beforeEach(() => {
  localStorage.clear();
  setSidebarCollapsed(false);
  vi.stubGlobal("fetch", vi.fn(() => Promise.resolve(new Response("{}", { headers: { "Content-Type": "application/json" } }))));
  history.replaceState(null, "", "/");
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  reset();
  vi.unstubAllGlobals();
});

describe("folding the whole sidebar", () => {
  it("has a button at the top that folds it to the rail and keeps that", async () => {
    act(() => root.render(<Sidebar route={{ name: "home" }} onSearch={() => {}} onNewAgent={() => {}} />));
    await flush();
    const fold = button("收起侧边栏")!;
    expect(fold.getAttribute("aria-expanded")).toBe("true");
    expect(fold.title).toContain("Ctrl+Alt+B");
    act(() => fold.click());
    expect(getLayout().sidebarCollapsed).toBe(true);
    expect(localStorage.getItem(COLLAPSED_KEY)).toBe("1");
  });

  it("the rail keeps the pages as icons, their counts as dots, and its own way back", async () => {
    act(() => root.render(<SidebarRail route={{ name: "settings" }} onSearch={() => {}} onNewAgent={() => {}} />));
    await flush();
    const links = [...host.querySelectorAll("nav a")].map((a) => a.getAttribute("aria-label"));
    expect(links).toEqual(expect.arrayContaining(["Persona", "总览", "收件箱", "定时任务", "知识库", "机器", "设置"]));
    expect(host.querySelector('a[aria-label="设置"]')!.getAttribute("aria-current")).toBe("page");
    expect(host.textContent).toBe(""); // icons only: no labels, no numbers
    const open = button("展开侧边栏")!;
    expect(open.getAttribute("aria-expanded")).toBe("false");
    setSidebarCollapsed(true);
    act(() => open.click());
    expect(getLayout().sidebarCollapsed).toBe(false);
  });
});

describe("localized page names in the drawer and desktop sidebar (#3579)", { timeout: 30_000 }, () => {
  const locales = [
    { lang: "en", full: ["Overview", "Inbox", "Health", "Scheduled tasks", "Knowledge", "Machines"], short: ["Home", "Inbox", "Health", "Tasks", "KB", "Hosts"] },
    { lang: "ja", full: ["概要", "受信トレイ", "ヘルスケア", "定期タスク", "ナレッジ", "マシン"], short: ["概要", "受信", "健康", "定期", "ナレッジ", "マシン"] },
    { lang: "zh-Hans", full: ["总览", "收件箱", "健康", "定时任务", "知识库", "机器"], short: ["总览", "收件箱", "健康", "定时任务", "知识库", "机器"] },
  ] as const;
  const paths = ["/overview", "/inbox", "/health", "/routines", "/kb", "/machines"];
  const pageLink = (path: string) => host.querySelector<HTMLAnchorElement>(`a[href="${path}"]`)!;
  function DrawerPages() {
    const { pages } = useNavPages();
    return <NavGrid pages={pages} activeKey="home" go={() => () => {}} />;
  }

  afterEach(() => initI18n("zh-Hans"), 30_000);

  it.each(locales)("uses short visible names and full accessible names in $lang", async ({ lang, full, short }) => {
    await initI18n(lang);
    act(() => root.render(<DrawerPages />));
    await flush();
    paths.forEach((path, i) => {
      const link = pageLink(path);
      expect(link.textContent).toBe(short[i]);
      expect(link.title).toBe(full[i]);
      expect(link.getAttribute("aria-label")).toBe(full[i]);
      expect(link.getAttribute("aria-current")).toBe(i === 0 ? "page" : null);
    });
  });

  it.each(locales)("keeps full names in desktop rows and the icon rail in $lang", async ({ lang, full }) => {
    await initI18n(lang);
    act(() => root.render(<Sidebar route={{ name: "home" }} onSearch={() => {}} onNewAgent={() => {}} />));
    await flush();
    paths.forEach((path, i) => expect(pageLink(path).textContent).toBe(full[i]));

    act(() => root.render(<SidebarRail route={{ name: "home" }} onSearch={() => {}} onNewAgent={() => {}} />));
    await flush();
    paths.forEach((path, i) => {
      const link = pageLink(path);
      expect(link.textContent).toBe("");
      expect(link.title).toBe(full[i]);
      expect(link.getAttribute("aria-label")).toBe(full[i]);
      expect(link.getAttribute("aria-current")).toBe(i === 0 ? "page" : null);
    });
  });
});

describe("the shortcut", () => {
  function Shell({ narrow, toggleDrawer }: { narrow: boolean; toggleDrawer: () => void }) {
    useShortcuts({ route: { name: "home" }, narrow, toggleDrawer });
    return null;
  }
  const press = () => dispatchEvent(new KeyboardEvent("keydown", { key: "b", code: "KeyB", ctrlKey: true, altKey: true, bubbles: true, cancelable: true }));

  it("is Mod+Alt+B: Mod+B is already the right panel's, Mod+Shift+B the browser's", () => {
    const b = getBindings();
    expect(b["left-sidebar"]).toBe("Mod+Alt+B");
    expect(b["right-panel"]).toBe("Mod+B");
    expect(checkBinding("left-sidebar", { mod: true, alt: false, shift: false, key: "B" }, b, "other")).toMatchObject({ ok: false, reason: "conflict", with: "right-panel" });
    expect(checkBinding("left-sidebar", { mod: true, alt: false, shift: true, key: "B" }, b, "other")).toMatchObject({ ok: false, reason: "reserved" });
    const ev = { key: "b", code: "KeyB", ctrlKey: true, metaKey: false, altKey: true, shiftKey: false };
    expect(eventChord(ev, "other")?.mod).toBe(true);
    expect(matchShortcut(ev, b, "other")?.def.id).toBe("left-sidebar");
    expect(matchShortcut({ ...ev, ctrlKey: false, metaKey: true }, b, "mac")?.def.id).toBe("left-sidebar");
  });

  it("folds and opens the column on a wide screen, kept in this browser", () => {
    const drawer = vi.fn();
    act(() => root.render(<Shell narrow={false} toggleDrawer={drawer} />));
    press();
    expect(getLayout().sidebarCollapsed).toBe(true);
    expect(localStorage.getItem(COLLAPSED_KEY)).toBe("1");
    press();
    expect(getLayout().sidebarCollapsed).toBe(false);
    expect(drawer).not.toHaveBeenCalled();
  });

  it("still opens and closes the drawer on a narrow screen, folding nothing", () => {
    const drawer = vi.fn();
    act(() => root.render(<Shell narrow toggleDrawer={drawer} />));
    press();
    expect(drawer).toHaveBeenCalledTimes(1);
    expect(getLayout().sidebarCollapsed).toBe(false);
  });
});

describe("folding a block of the sidebar", () => {
  it("folds 页面 under its title and keeps it", async () => {
    act(() => root.render(<Sidebar route={{ name: "home" }} onSearch={() => {}} onNewAgent={() => {}} />));
    await flush();
    const title = button("页面")!;
    expect(title.getAttribute("aria-expanded")).toBe("true");
    act(() => title.click());
    expect(title.getAttribute("aria-expanded")).toBe("false");
    expect(localStorage.getItem("fold:pages")).toBe("1");
    expect(sectionBody("页面").hasAttribute("inert")).toBe(true);
    // the Persona row stays above it
    expect(host.querySelector('a[title="Persona 的主对话"]')).not.toBeNull();
  });

  it("gives a section and its chevron the same fold tokens in both directions", async () => {
    act(() => root.render(<Sidebar route={{ name: "home" }} onSearch={() => {}} onNewAgent={() => {}} />));
    await flush();
    const title = button("页面")!;
    const section = title.closest(".mt-1, .mt-3")!;
    const fold = [...section.children].find((el) => el.classList.contains("grid")) as HTMLElement;
    const chevron = title.querySelector("svg")!;
    const motion = (el: HTMLElement | SVGElement) => ({ duration: el.style.transitionDuration, curve: el.style.transitionTimingFunction });
    expect(motion(fold)).toEqual({ duration: "var(--sidebar-fold-open)", curve: "var(--sidebar-fold-open-ease)" });
    expect(motion(chevron)).toEqual(motion(fold));
    expect(fold.style.opacity).toBe("1");

    act(() => title.click());
    expect(motion(fold)).toEqual({ duration: "var(--sidebar-fold-close)", curve: "var(--sidebar-fold-close-ease)" });
    expect(motion(chevron)).toEqual(motion(fold));
    expect(fold.style.opacity).toBe("1");
  });

  it("folds the whole Agents block,「+」 staying on its title", async () => {
    act(() => root.render(<Sidebar route={{ name: "home" }} onSearch={() => {}} onNewAgent={() => {}} />));
    await flush();
    const title = button("Agents")!;
    act(() => title.click());
    expect(title.getAttribute("aria-expanded")).toBe("false");
    expect(localStorage.getItem("fold:agents")).toBe("1");
    expect(button("新建 Agent")).toBeDefined();
  });

  it("offers the groupings as segments above the quick filter, 按待办 first and the default (#2812)", async () => {
    act(() => root.render(<Sidebar route={{ name: "home" }} onSearch={() => {}} onNewAgent={() => {}} />));
    await flush();
    const bar = host.querySelector<HTMLElement>('[role="radiogroup"][aria-label="Agent 分组"]')!;
    const segs = [...bar.querySelectorAll<HTMLButtonElement>('[role="radio"]')];
    expect(segs.map((b) => b.textContent)).toEqual(["待办", "机器", "仓库", "项目", "状态"]);
    expect(segs.map((b) => b.getAttribute("aria-label"))).toEqual(["按待办", "按机器", "按仓库", "按项目", "按状态"]);
    expect(segs[0].getAttribute("aria-checked")).toBe("true");
    expect(segs.every((b) => b.className.includes("whitespace-nowrap"))).toBe(true);
    act(() => segs[1].click());
    expect(localStorage.getItem("persona.agentGrouping")).toBe("machine");
    expect(segs[1].getAttribute("aria-checked")).toBe("true");
    expect(segs[0].getAttribute("aria-checked")).toBe("false");
    // no dropdown on the title row any more
    expect(host.querySelector('button[aria-haspopup="menu"][aria-label^="Agent 分组"]')).toBeNull();
  });

  it("keeps a grouping chosen before", async () => {
    localStorage.setItem("persona.agentGrouping", "state");
    act(() => root.render(<Sidebar route={{ name: "home" }} onSearch={() => {}} onNewAgent={() => {}} />));
    await flush();
    expect(host.querySelector('[role="radio"][aria-label="按状态"]')!.getAttribute("aria-checked")).toBe("true");
  });

  it("starts as it was left", async () => {
    localStorage.setItem("fold:pages", "1");
    localStorage.setItem("fold:agents", "1");
    act(() => root.render(<Sidebar route={{ name: "home" }} onSearch={() => {}} onNewAgent={() => {}} />));
    await flush();
    expect(button("页面")!.getAttribute("aria-expanded")).toBe("false");
    expect(button("Agents")!.getAttribute("aria-expanded")).toBe("false");
  });

  it("leaves 页面 and Agents open when every group folds (their titles stay to read)", async () => {
    act(() => root.render(<Sidebar route={{ name: "home" }} onSearch={() => {}} onNewAgent={() => {}} />));
    await flush();
    act(() => setAllFolded(true));
    expect(button("页面")!.getAttribute("aria-expanded")).toBe("true");
    expect(button("Agents")!.getAttribute("aria-expanded")).toBe("true");
    expect(localStorage.getItem("fold:pages")).toBeNull();
  });
});

describe("ordering the Agents list (G099)", () => {
  const item = (label: string) => [...document.querySelectorAll<HTMLElement>('[role^="menuitem"]')].find((el) => el.textContent?.includes(label));

  it("offers 状态优先 / 最近更新 under the grouping and keeps the choice", async () => {
    act(() => root.render(<Sidebar route={{ name: "home" }} onSearch={() => {}} onNewAgent={() => {}} />));
    await flush();
    act(() => button("Agent 列表选项")!.click());
    expect(item("状态优先")).toBeDefined();
    expect(item("按机器")).toBeUndefined(); // the groupings are the segments now (#2812)
    act(() => item("最近更新")!.click());
    await flush();
    expect(localStorage.getItem("persona.agentSort")).toBe("updated");
    expect(button("Agent 列表选项：最近更新")).toBeDefined();
  });

  it("is not offered under 按状态, whose sections are the states", async () => {
    localStorage.setItem("persona.agentGrouping", "state");
    localStorage.setItem("persona.agentSort", "updated");
    act(() => root.render(<Sidebar route={{ name: "home" }} onSearch={() => {}} onNewAgent={() => {}} />));
    await flush();
    expect(host.querySelector('[role="radio"][aria-label="按状态"]')!.getAttribute("aria-checked")).toBe("true");
    act(() => button("Agent 列表选项")!.click());
    expect(item("全部折叠")).toBeDefined();
    expect(item("最近更新")).toBeUndefined();
  });
});

describe("the Agents list menu (#3027)", () => {
  it("has 全部折叠 and 全部展开, which open the spaces folded by hand and leave the other blocks", async () => {
    localStorage.setItem("fold:space:mac/a", "1");
    act(() => root.render(<Sidebar route={{ name: "home" }} onSearch={() => {}} onNewAgent={() => {}} />));
    await flush();
    const menu = [...host.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.getAttribute("aria-label") === "Agent 列表选项")!;
    act(() => menu.click());
    await flush();
    const item = (label: string) => [...document.querySelectorAll<HTMLElement>("[role=menuitem], [role=menuitemradio], [role=menuitemcheckbox]")].find((el) => el.textContent?.includes(label));
    expect(item("全部折叠")).toBeDefined();
    act(() => item("全部展开")!.click());
    await flush();
    expect(localStorage.getItem("fold:space:mac/a")).toBe("0");
    expect(localStorage.getItem("fold:persona")).toBeNull();
  });
});

describe("the main conversation: the column's first row, Persona with the app's mark (#2004, #5109), marked 主对话 (#6293)", () => {
  const conv = (id: string, title: string): Session => ({ machine_id: "hub", session_id: id, title, cwd: "/", status: "idle", state: "idle", assistant: true, pending_interactions: id === "main" ? 2 : 0 });
  const assistant: AssistantInfo = { machine_id: "hub", main: "main", leaders: ["c1"], conversations: [conv("main", "Persona"), conv("c1", "整理周报")] };
  const block = (title: string) => button(title)!.closest(".mt-1, .mt-3") as HTMLElement;
  const mainRow = () => host.querySelector<HTMLAnchorElement>('a[title="Persona 的主对话"]')!;
  const load = async () => {
    vi.stubGlobal("fetch", vi.fn((url: string) => {
      const body = new URL(url, "http://x").pathname === "/api/v1/assistant" ? assistant : {};
      return Promise.resolve(new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } }));
    }));
    await act(() => refresh());
    act(() => root.render(<Sidebar route={{ name: "home" }} onSearch={() => {}} onNewAgent={() => {}} />));
    await flush();
  };

  it("offers New chat in「Persona 对话」, not above the main chat, and an icon in the rail, only with an assistant (#3231)", async () => {
    const newChats = () => [...host.querySelectorAll<HTMLAnchorElement>('a[href="/persona/new"]')];
    act(() => root.render(<Sidebar route={{ name: "home" }} onSearch={() => {}} onNewAgent={() => {}} />));
    await flush();
    expect(newChats()).toEqual([]);
    await load();
    // one entry: the compose icon at the end of the Persona chats title, as on the phone's sidebar, folded or not; not a row
    expect(newChats()).toHaveLength(1);
    const icon = newChats()[0];
    expect(icon.textContent).toBe("");
    expect(icon.getAttribute("aria-label")).toBe("新对话");
    expect(icon.getAttribute("title")).toBe("新对话");
    expect(icon.closest(".group\\/sec")).not.toBeNull();
    expect(block("Persona 对话").contains(icon)).toBe(true);
    const chatRows = [...block("Persona 对话").querySelectorAll("a")];
    expect(chatRows.some((a) => a.textContent?.includes("整理周报"))).toBe(true);
    const all = [...host.querySelectorAll("a")];
    expect(all.indexOf(icon)).toBeGreaterThan(all.indexOf(mainRow()));
    expect(host.querySelector("[data-sidebar-sticky]")!.contains(icon)).toBe(false);
    act(() => button("Persona 对话")!.click());
    await flush();
    expect(newChats().filter((a) => !a.closest("[inert]"))).toHaveLength(1);
    act(() => button("Persona 对话")!.click());
    await flush();
    // the open new chat page lights the icon
    act(() => root.render(<Sidebar route={{ name: "persona-new" }} onSearch={() => {}} onNewAgent={() => {}} />));
    await flush();
    expect(newChats()[0].getAttribute("aria-current")).toBe("page");
    act(() => root.render(<SidebarRail route={{ name: "persona-new" }} onSearch={() => {}} onNewAgent={() => {}} />));
    await flush();
    expect(newChats()[0].getAttribute("aria-label")).toBe("新对话");
    expect(newChats()[0].getAttribute("aria-current")).toBe("page");
  });

  it("is the first row, above the pages, with the app's mark, a 主对话 mark, a line under it, and nowhere else", async () => {
    await load();
    const rows = [...host.querySelectorAll<HTMLAnchorElement>("[data-sidebar-rows] a")];
    expect(rows[0]).toBe(mainRow());
    expect(mainRow().textContent).toContain("Persona");
    // the brand row reads as a conversation, not a heading: a small "主对话" under the name (#6293)
    const mark = mainRow().querySelector("[data-row-line]:not([data-draft])");
    expect(mark?.textContent).toBe("主对话");
    expect(mainRow().querySelector('img[src$="/app-icon.png"]')).not.toBeNull();
    expect(mainRow().textContent).toContain("2"); // its pending count
    // the line parts it from the pages, which come after it
    const line = host.querySelector("[data-persona-divider]")!;
    expect(mainRow().compareDocumentPosition(line) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(line.compareDocumentPosition(button("页面")!) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(host.querySelectorAll('a[title="Persona 的主对话"]')).toHaveLength(1);
    expect(host.querySelectorAll("[data-persona-divider]")).toHaveLength(1);
    expect(host.querySelector('a[href="/persona/main"]')).toBeNull();
    // "Persona chats"starts with the other conversations, the main one not in it
    expect(block("Persona 对话").querySelector('a[title="Persona 的主对话"]')).toBeNull();
    expect(block("Persona 对话").textContent).toContain("整理周报");
    // its ⋯ opens the conversation's menu
    expect(mainRow().parentElement!.querySelector('button[aria-label="对话的操作"]')).not.toBeNull();
  });

  it("stays on show however 页面 and「Persona 对话」are folded", async () => {
    await load();
    act(() => button("Persona 对话")!.click());
    act(() => button("页面")!.click());
    expect(mainRow()).not.toBeNull();
    expect(mainRow().closest("[inert]")).toBeNull();
  });

  it("is drawn while the first load is out, before the hub answers", async () => {
    vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>(() => {})));
    act(() => root.render(<Sidebar route={{ name: "home" }} onSearch={() => {}} onNewAgent={() => {}} />));
    await flush();
    expect(host.querySelector("[data-sidebar-rows] a")).toBe(mainRow());
    expect(host.querySelector("[data-persona-divider]")).not.toBeNull();
  });
});
