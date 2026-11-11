// @vitest-environment jsdom
// #3740: the To-dos list's filter and sort on the web. The menu beside the search field picks This week, which shows as
// a chip that clears it; a line says what waits on the user outside the week; Due date orders the rows inside each
// section; both are kept in this browser. The hub is faked; the data made up.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Todo } from "../lib/todos";

vi.hoisted(() => {
  window.matchMedia = ((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addEventListener() {},
    removeEventListener() {},
    addListener() {},
    removeListener() {},
    dispatchEvent: () => false,
  })) as typeof window.matchMedia;
});

vi.mock("../lib/api", async (orig) => ({ ...(await orig<typeof import("../lib/api")>()), api: async () => ({}) }));

const { getState } = await import("../lib/store");
const { TodosView } = await import("./Todos");

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 6; i++) await act(async () => { await Promise.resolve(); });
};
const day = (offset: number) => {
  const d = new Date();
  d.setDate(d.getDate() + offset);
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
};
const now = new Date().toISOString();
const todo = (id: string, title: string, patch: Partial<Todo> = {}): Todo =>
  ({ id, title, created_at: now, updated_at: now, done_at: null, agents: [], ...patch }) as Todo;

const seed = (patch: Record<string, unknown>) => Object.assign(getState() as unknown as Record<string, unknown>, patch);
const before = { ...getState() };

beforeEach(async () => {
  localStorage.clear();
  seed({
    todos: [
      todo("today", "Renew the domain", { due_date: day(0) }),
      todo("later", "Plan the offsite", { due_date: day(40) }),
      todo("soon", "Back up the photos", { due_date: day(0), priority: "high" }),
      // waits on the owner, no due date: outside This week
      todo("ask", "Pick a cache", { origin: "conv", ask: { outcome: "decide", verdict: "Pick one", asked_at: now } } as Partial<Todo>),
    ],
    todosLoaded: true,
    sessions: {},
    interactions: [],
    answered: {},
  });
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  await act(async () => root.render(<TodosView />));
  await flush();
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  document.body.innerHTML = "";
  seed(before);
});

const titles = () => [...host.querySelectorAll("[data-testid=todo-triage-row] .truncate.font-semibold")].map((el) => el.textContent);
const menuRow = (label: string) => [...document.querySelectorAll<HTMLElement>("[role=menuitemradio]")].find((el) => el.textContent?.includes(label));

describe("the To-dos list's filter and sort", () => {
  it("filters to This week from the menu, says what waits outside it, and the chip clears it", async () => {
    expect(host.querySelector("[data-testid=todos-filter-chip]")).toBeNull();
    expect(host.querySelector("[data-testid=todos-filter-changed]")).toBeNull();
    await act(async () => host.querySelector<HTMLElement>("[data-testid=todos-filter] button")!.click());
    await act(async () => menuRow("本周")!.click());
    await flush();
    expect(titles()).not.toContain("Plan the offsite");
    expect(titles()).not.toContain("Pick a cache");
    expect(host.querySelector("[data-testid=todos-hidden-waiting]")?.textContent).toContain("1");
    expect(localStorage.getItem("persona.todos.filter")).toBe("week");
    await act(async () => host.querySelector<HTMLElement>("[data-testid=todos-filter-chip]")!.click());
    await flush();
    expect(titles()).toContain("Plan the offsite");
    expect(host.querySelector("[data-testid=todos-hidden-waiting]")).toBeNull();
  });

  it("orders rows by priority inside a section", async () => {
    await act(async () => host.querySelector<HTMLElement>("[data-testid=todos-filter] button")!.click());
    await act(async () => menuRow("优先级")!.click());
    await flush();
    // Other open starts folded while Needs you has a row
    await act(async () => host.querySelector<HTMLElement>("[data-testid=todo-section][data-section=other] button[aria-expanded]")!.click());
    await flush();
    const open = titles().filter((x) => x === "Back up the photos" || x === "Renew the domain");
    expect(open).toEqual(["Back up the photos", "Renew the domain"]);
    expect(localStorage.getItem("persona.todos.sort")).toBe("priority");
    // not the default order: the button says so, with no filter on
    expect(host.querySelector("[data-testid=todos-filter-changed]")).not.toBeNull();
    expect(host.querySelector("[data-testid=todos-filter-chip]")).toBeNull();
  });
});
