// @vitest-environment jsdom
// The plan pill's "is the plan card out of view" (usePlanOutOfView): an IntersectionObserver rooted at the chat's scroller
// says so, so scrolling the chat queries no DOM and reads no layout; the body's ResizeObserver, a new body, or a scroll that
// finds the watched card no longer in the DOM, looks for it again. Without IntersectionObserver it measures on every scroll.
import { act, useRef } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Todo } from "../../lib/work-groups";
import { usePlanOutOfView } from "./latest-plan";
import { PlanCard } from "./PlanCard";

const observers = vi.hoisted(() => ({
  io: undefined as undefined | { cb: IntersectionObserverCallback; root: Element | Document | null | undefined; targets: Set<Element> },
  ro: undefined as undefined | ResizeObserverCallback,
}));

vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  // jsdom has no CSS namespace
  g.CSS ??= { escape: (s: string) => s.replace(/["\\]/g, "\\$&") };
  g.ResizeObserver = class {
    constructor(cb: ResizeObserverCallback) {
      observers.ro = cb;
    }
    observe() {}
    unobserve() {}
    disconnect() {}
  };
  g.IntersectionObserver = class {
    private me: { cb: IntersectionObserverCallback; root: Element | Document | null | undefined; targets: Set<Element> };
    constructor(cb: IntersectionObserverCallback, opts?: IntersectionObserverInit) {
      this.me = { cb, root: opts?.root, targets: new Set() };
      observers.io = this.me;
    }
    observe(t: Element) {
      this.me.targets.add(t);
    }
    unobserve(t: Element) {
      this.me.targets.delete(t);
    }
    disconnect() {
      this.me.targets.clear();
    }
    takeRecords() {
      return [];
    }
  };
});

const todos: Todo[] = [
  { content: "Read the code", status: "completed" },
  { content: "Change it", status: "in_progress" },
];

const seen: boolean[] = [];

/** A chat body of 200 rows with the plan cards `cards` under them (a new `gen` draws them again; a new `box` draws the
 *  body again, a new `frame` the scroller), and the hook watching card `id`. */
function Chat({ id, cards, gen = 0, box = 0, frame = 0, bare = false }: { id?: string; cards: string[]; gen?: number; box?: number; frame?: number; bare?: boolean }) {
  const scroller = useRef<HTMLDivElement>(null);
  seen.push(usePlanOutOfView(scroller, id));
  return (
    <div key={frame} ref={scroller} data-scroller>
      {!bare && <div key={box}>
        {Array.from({ length: 200 }, (_, i) => (
          <div key={i} data-row>
            row {i}
          </div>
        ))}
        {cards.map((c) => (
          <PlanCard key={`${c}:${gen}`} id={c} todos={todos} />
        ))}
      </div>}
    </div>
  );
}

let host: HTMLDivElement;
let root: Root;
const render = (props: { id?: string; cards: string[]; gen?: number; box?: number; frame?: number; bare?: boolean }) => act(() => root.render(<Chat {...props} />));
const scroller = () => host.querySelector<HTMLElement>("[data-scroller]")!;
const card = (id: string) => host.querySelector(`[data-plan-id="${id}"]`)!;
const out = () => seen[seen.length - 1];
const intersect = (target: Element, isIntersecting: boolean) =>
  act(() => observers.io!.cb([{ target, isIntersecting } as unknown as IntersectionObserverEntry], {} as IntersectionObserver));
const resize = () => act(() => observers.ro!([], {} as ResizeObserver));

beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement("div");
  document.body.appendChild(host);
  root = createRoot(host);
  seen.length = 0;
  observers.io = undefined;
  observers.ro = undefined;
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.restoreAllMocks();
});

describe("usePlanOutOfView", () => {
  it("watches the card with an IntersectionObserver rooted at the scroller", async () => {
    await render({ id: "p1", cards: ["p1"] });
    expect(observers.io?.root).toBe(scroller());
    expect([...observers.io!.targets]).toEqual([card("p1")]);
  });

  it("queries nothing and reads no layout while the chat scrolls or grows", async () => {
    await render({ id: "p1", cards: ["p1"] });
    const sc = scroller();
    const query = vi.spyOn(Element.prototype, "querySelector");
    const rect = vi.spyOn(Element.prototype, "getBoundingClientRect");
    const calls = () => ({ querySelector: query.mock.calls.length, getBoundingClientRect: rect.mock.calls.length });
    for (let i = 0; i < 10; i++) await act(() => sc.dispatchEvent(new Event("scroll")));
    expect(calls()).toEqual({ querySelector: 0, getBoundingClientRect: 0 });
    // streaming: the body grows while the card stays where it is
    for (let i = 0; i < 10; i++) await resize();
    expect(calls()).toEqual({ querySelector: 0, getBoundingClientRect: 0 });
  });

  it("is out exactly while the card does not intersect the view", async () => {
    await render({ id: "p1", cards: ["p1"] });
    expect(out()).toBe(false);
    await intersect(card("p1"), false);
    expect(out()).toBe(true);
    await intersect(card("p1"), true);
    expect(out()).toBe(false);
  });

  it("is out once the card is gone, and finds it again when it is back", async () => {
    await render({ id: "p1", cards: ["p1"] });
    await render({ id: "p1", cards: [] }); // the hub folded it away with the work around it
    await resize();
    expect(out()).toBe(true);
    expect(observers.io!.targets.size).toBe(0);

    await render({ id: "p1", cards: ["p1"] });
    await resize();
    expect([...observers.io!.targets]).toEqual([card("p1")]);
    await intersect(card("p1"), true);
    expect(out()).toBe(false);
  });

  it("is out at once when the card is not drawn, and a scroll looks for nothing then", async () => {
    await render({ id: "p1", cards: [] });
    expect(out()).toBe(true);
    const sc = scroller();
    const query = vi.spyOn(Element.prototype, "querySelector");
    for (let i = 0; i < 10; i++) await act(() => sc.dispatchEvent(new Event("scroll")));
    expect(query).not.toHaveBeenCalled();
  });

  it("follows a card redrawn at the same size, which the body's ResizeObserver does not see", async () => {
    await render({ id: "p1", cards: ["p1"] });
    const old = card("p1");
    await render({ id: "p1", cards: ["p1"], gen: 1 }); // React draws the card again, in a new element
    expect(card("p1")).not.toBe(old);
    // the observer's last report of the old element: gone, so not intersecting
    await intersect(old, false);
    expect(out()).toBe(false);
    expect([...observers.io!.targets]).toEqual([card("p1")]);
    await intersect(card("p1"), false);
    expect(out()).toBe(true);
  });

  it("follows a card redrawn at the same size while out of view on the next scroll", async () => {
    await render({ id: "p1", cards: ["p1"] });
    await intersect(card("p1"), false);
    expect(out()).toBe(true);
    const old = card("p1");
    // redrawn in a new element of the same size while out of view: the body's ResizeObserver does not fire, and the
    // browser reports nothing for the old element, which was not intersecting before it left and is not after
    await render({ id: "p1", cards: ["p1"], gen: 1 });
    const fresh = card("p1");
    expect(fresh).not.toBe(old);
    expect(out()).toBe(true);
    const sc = scroller();
    const query = vi.spyOn(Element.prototype, "querySelector");
    const rect = vi.spyOn(Element.prototype, "getBoundingClientRect");
    await act(() => sc.dispatchEvent(new Event("scroll")));
    // toEqual would match the old element too (same markup): compare identity
    expect(observers.io!.targets.size).toBe(1);
    expect(observers.io!.targets.has(fresh)).toBe(true);
    // the new card scrolled into view: the pill goes away
    await intersect(fresh, true);
    expect(out()).toBe(false);
    // it looked once and read no layout; scrolls after it look for nothing
    expect(query).toHaveBeenCalledTimes(1);
    for (let i = 0; i < 10; i++) await act(() => sc.dispatchEvent(new Event("scroll")));
    expect(query).toHaveBeenCalledTimes(1);
    expect(rect).not.toHaveBeenCalled();
  });

  it("follows a new plan, and ignores what the old card reports", async () => {
    await render({ id: "p1", cards: ["p1"] });
    const old = card("p1");
    await render({ id: "p2", cards: ["p1", "p2"] });
    expect([...observers.io!.targets]).toEqual([card("p2")]);
    await intersect(old, false);
    expect(out()).toBe(false);
    await intersect(card("p2"), false);
    expect(out()).toBe(true);
  });

  it("stops listening to the scroller once it watches another plan", async () => {
    await render({ id: "p1", cards: ["p1"] });
    await render({ id: "p2", cards: ["p2"] }); // the old plan's card is gone with it
    await intersect(card("p2"), true);
    expect(out()).toBe(false);
    const sc = scroller();
    const query = vi.spyOn(Element.prototype, "querySelector");
    for (let i = 0; i < 3; i++) await act(() => sc.dispatchEvent(new Event("scroll")));
    // a scroll listener left from p1 would find its card gone, look for p1, find none and show the pill
    expect(out()).toBe(false);
    expect(query).not.toHaveBeenCalled();
  });

  it("follows a body drawn again in a new element, which its ResizeObserver no longer sees", async () => {
    await render({ id: "p1", cards: [] }); // inside a folded summary
    expect(out()).toBe(true);
    // the page draws the chat again (a view switch that wraps it differently), this time with the summary open
    await render({ id: "p1", cards: ["p1"], box: 1 });
    await act(async () => {}); // the MutationObserver's report
    expect([...observers.io!.targets]).toEqual([card("p1")]);
    await intersect(card("p1"), true);
    expect(out()).toBe(false);
  });

  it("watches a scroller with nothing in it yet, and finds the card once its content box comes", async () => {
    await render({ id: "p1", cards: [], bare: true });
    expect(out()).toBe(true); // not drawn
    await render({ id: "p1", cards: ["p1"] });
    await act(async () => {}); // the MutationObserver's report
    expect([...observers.io!.targets]).toEqual([card("p1")]);
    await intersect(card("p1"), true);
    expect(out()).toBe(false);
  });

  it("follows a scroller drawn again in a new element", async () => {
    await render({ id: "p1", cards: ["p1"] });
    const old = scroller();
    await render({ id: "p1", cards: ["p1"], frame: 1 });
    expect(scroller()).not.toBe(old);
    expect(observers.io!.root).toBe(scroller());
    await intersect(card("p1"), true);
    expect(out()).toBe(false);
  });

  it("is not out without a plan", async () => {
    await render({ cards: ["p1"] });
    expect(out()).toBe(false);
    expect(observers.io).toBeUndefined();
  });

  it("measures on scroll where there is no IntersectionObserver", async () => {
    const g = globalThis as Record<string, unknown>;
    const saved = g.IntersectionObserver;
    g.IntersectionObserver = undefined;
    try {
      let cardTop = 100;
      vi.spyOn(Element.prototype, "getBoundingClientRect").mockImplementation(function (this: Element) {
        const top = this.hasAttribute("data-scroller") ? 0 : cardTop;
        const height = this.hasAttribute("data-scroller") ? 600 : 40;
        return { top, bottom: top + height, left: 0, right: 0, width: 0, height, x: 0, y: top, toJSON() {} } as DOMRect;
      });
      await render({ id: "p1", cards: ["p1"] });
      expect(out()).toBe(false);
      cardTop = -500; // scrolled past
      await act(() => scroller().dispatchEvent(new Event("scroll")));
      expect(out()).toBe(true);
      cardTop = 600; // the card's top on the view's bottom edge still counts as in view
      await act(() => scroller().dispatchEvent(new Event("scroll")));
      expect(out()).toBe(false);
    } finally {
      g.IntersectionObserver = saved;
    }
  });
});
