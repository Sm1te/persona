// @vitest-environment jsdom
// A task card (#2670): Enter or Space on the card opens the task, as a click does; Enter on one of its own buttons
// ("Message", "Retry", "Remove"…) does only that button's thing and leaves the task's detail closed.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { refresh } from "../../lib/store";
import { TaskCard } from "./Tasks";

// where a click went: a page of its own (#5096), never a side panel
const opened = () => location.pathname + location.search;

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const TODO = { id: "todo-1", title: "Example task", created_at: new Date().toISOString(), updated_at: new Date().toISOString() };

let host: HTMLDivElement;
let root: Root;

const card = () => host.querySelector<HTMLElement>('[role="button"]')!;
const press = (el: HTMLElement, key: string) => {
  const e = new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true });
  act(() => {
    el.dispatchEvent(e);
  });
  return e;
};

beforeEach(async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn((url: string) => Promise.resolve(new Response(JSON.stringify(String(url).includes("/todos") ? { todos: [TODO] } : {}), { headers: { "Content-Type": "application/json" } }))),
  );
  await refresh();
  history.replaceState(null, "", "/");
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  act(() => root.render(<TaskCard todoId={TODO.id} conversation="c1" />));
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  history.replaceState(null, "", "/");
  vi.unstubAllGlobals();
});

describe("a task card from the keyboard", () => {
  it("names itself by its title and status", () => {
    expect(card().getAttribute("aria-label")).toMatch(/^Example task，.+/);
  });

  it("opens the task on Enter on the card itself", () => {
    const e = press(card(), "Enter");
    expect(opened()).toBe(`/todos?id=${TODO.id}`);
    expect(e.defaultPrevented).toBe(true);
  });

  it("opens the task on Space on the card itself, without scrolling the page", () => {
    const e = press(card(), " ");
    expect(opened()).toBe(`/todos?id=${TODO.id}`);
    expect(e.defaultPrevented).toBe(true);
  });

  it("leaves the task closed on Enter or Space on one of its own buttons", () => {
    const buttons = [...card().querySelectorAll<HTMLButtonElement>("button")];
    expect(buttons.length).toBeGreaterThan(0);
    for (const b of buttons) {
      for (const key of ["Enter", " "]) {
        const e = press(b, key);
        expect(opened()).toBe("/");
        // The button keeps its own activation (the browser turns Enter / Space into its click).
        expect(e.defaultPrevented).toBe(false);
      }
    }
  });

  it("ignores other keys", () => {
    press(card(), "a");
    press(card(), "Tab");
    expect(opened()).toBe("/");
  });
});
