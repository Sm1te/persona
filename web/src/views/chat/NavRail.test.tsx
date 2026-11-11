// @vitest-environment jsdom
// Component contracts: passive cached scroll tracking, transient visibility, stable selection and accessible input.
import { act, Profiler, StrictMode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { NAV_RAIL_MIN_TICKS, type NavRailTick } from "../../lib/nav-rail";
import { NAV_RAIL_METRICS as M } from "../../ui/nav-rail-metrics";

// `matches` is read live and `set` notifies subscribers: viewport.ts caches the MediaQueryList, so a later test (or a
// later flip in one test) changes the pointer the mounted rail already follows.
const finePointer = vi.hoisted(() => {
  const listeners = new Set<() => void>();
  const state = {
    matches: true,
    set(matches: boolean) {
      state.matches = matches;
      for (const l of [...listeners]) l();
    },
  };
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = (query: string) => ({
    get matches() {
      return query === "(any-pointer: fine)" ? state.matches : true;
    },
    addEventListener(_type: string, l: () => void) { listeners.add(l); },
    removeEventListener(_type: string, l: () => void) { listeners.delete(l); },
  });
  g.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} };
  return state;
});

const { NavRail } = await import("./NavRail");

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const tick = (i: number): NavRailTick => ({ id: `seg:${i}`, preview: `question ${i}` });
// jsdom reports every box as 0: a scroller wide and tall enough for the rail needs real numbers
const scrollerWith = (width: number, height: number) => {
  const el = document.createElement("div");
  Object.defineProperty(el, "clientWidth", { value: width });
  Object.defineProperty(el, "clientHeight", { value: height });
  return el;
};
// the rail measures on requestAnimationFrame, which jsdom runs as a ~16 ms timer
const settle = async () => {
  for (let i = 0; i < 3; i++) await act(async () => { await new Promise((r) => setTimeout(r, 20)); });
};

let host: HTMLDivElement;
let root: Root;
let commits: number;
beforeEach(() => {
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  commits = 0;
});
afterEach(() => {
  vi.useRealTimers();
  finePointer.matches = true;
  act(() => root.unmount());
  host.remove();
});

describe("NavRail", () => {
  it("separates the hit strip from native and overlay scrollbars and clears narrow content padding on unmount", async () => {
    const sc = scrollerWith(500, 600);
    Object.defineProperty(sc, "offsetWidth", { value: 510 });
    const content = document.createElement("div");
    content.dataset.chatContent = "";
    Object.defineProperty(content, "clientWidth", { value: 500 });
    sc.append(content);
    act(() => root.render(<NavRail ticks={[tick(1), tick(2)]} scroller={sc} onJump={() => {}} reset="example/gutter" />));
    await settle();
    expect(host.querySelector<HTMLElement>("[data-nav-rail]")!.style.right).toBe(`${10 + M.edgeGap}px`);
    expect(sc.style.getPropertyValue("--chat-nav-clearance")).toBe(`${M.width + M.edgeGap + M.endInset}px`);
    act(() => root.render(null));
    expect(sc.style.getPropertyValue("--chat-nav-clearance")).toBe("");
    const overlay = scrollerWith(500, 600);
    Object.defineProperty(overlay, "offsetWidth", { value: 500 });
    act(() => root.render(<NavRail ticks={[tick(1), tick(2)]} scroller={overlay} onJump={() => {}} reset="example/overlay" />));
    await settle();
    expect(host.querySelector<HTMLElement>("[data-nav-rail]")!.style.right).toBe(`${M.edgeGap}px`);
  });

  it("rearms a frame cancelled by StrictMode's initial effect replay", async () => {
    const sc = scrollerWith(700, 600);
    act(() => root.render(<StrictMode><NavRail ticks={[]} scroller={sc} onJump={() => {}} reset="example/strict" /></StrictMode>));
    await settle();
    act(() => root.render(<StrictMode><NavRail ticks={[tick(1), tick(2)]} scroller={sc} onJump={() => {}} reset="example/strict" /></StrictMode>));
    await settle();
    expect(host.querySelectorAll("[data-nav-rail] button")).toHaveLength(2);
  });

  it("draws a tick per question in a wide chat once measured", async () => {
    const sc = scrollerWith(1200, 600);
    act(() => root.render(
      <Profiler id="rail" onRender={() => commits++}>
        <NavRail ticks={[tick(1), tick(2), tick(3), tick(4)]} scroller={sc} onJump={() => {}} reset="mac/s1" />
      </Profiler>,
    ));
    await settle();
    const buttons = [...host.querySelectorAll("[data-nav-rail] button[data-tick]")];
    expect(buttons).toHaveLength(4);
    expect(commits).toBeGreaterThan(1); // the mount, then the measure that draws it
  });

  it("uses equal short marks before hover and after exit, including the current input", async () => {
    const sc = scrollerWith(700, 600);
    const row = document.createElement("div");
    row.dataset.line = "seg:1";
    row.append(document.createElement("div"));
    sc.append(row);
    act(() => root.render(<NavRail ticks={[tick(1), tick(2), tick(3)]} scroller={sc} onJump={() => {}} reset="example/idle-width" />));
    await settle();
    const marks = () => [...host.querySelectorAll<HTMLElement>("[data-nav-mark]")];
    const short = () => {
      expect(marks().map((mark) => mark.style.width)).toEqual(Array(3).fill(`${M.tickWidth}px`));
      expect(marks().map((mark) => mark.style.height)).toEqual(Array(3).fill(`${M.tickHeight}px`));
      expect(marks()[0].dataset.navMark).toBe("current");
      expect(marks()[0].classList.contains("bg-ink")).toBe(true);
      expect(marks()[0].style.opacity).toBe("1");
    };
    short();
    act(() => sc.dispatchEvent(new Event("scroll")));
    await settle();
    short();
    const button = host.querySelector('[data-tick="1"]')!;
    act(() => button.dispatchEvent(new MouseEvent("mouseover", { bubbles: true, relatedTarget: document.body })));
    expect(marks()[1].style.width).toBe(`${M.hoverWidth}px`);
    expect(parseFloat(marks()[0].style.width)).toBeCloseTo(M.tickWidth + (M.hoverWidth - M.tickWidth) * Math.exp(-0.5));
    expect(marks()[0].classList.contains("bg-ink")).toBe(true);
    expect(host.querySelector('[data-tick="0"]')?.getAttribute("aria-current")).toBe("true");
    act(() => button.dispatchEvent(new MouseEvent("mouseout", { bubbles: true, relatedTarget: document.body })));
    expect(host.querySelector("[data-nav-preview]")).toBeNull();
    short();
    const rail = host.querySelector<HTMLElement>("[data-nav-rail]")!;
    act(() => rail.focus());
    expect(marks()[0].style.width).toBe(`${M.currentHoverWidth}px`);
    expect(host.querySelector("[data-nav-preview]")?.textContent).toBe("question 1");
    act(() => rail.blur());
    short();
  });

  it("measures nothing for a chat below the tick minimum", async () => {
    const sc = scrollerWith(1200, 600);
    act(() => root.render(
      <Profiler id="rail" onRender={() => commits++}>
        <NavRail ticks={Array.from({ length: NAV_RAIL_MIN_TICKS - 1 }, (_, i) => tick(i + 1))} scroller={sc} onJump={() => {}} reset="mac/s1" />
      </Profiler>,
    ));
    const mounted = commits;
    await settle();
    expect(commits).toBe(mounted); // no measure state, no redraw — whatever the scroller's size
    expect(host.querySelector("[data-nav-rail]")).toBeNull();
  });

  it("measures nothing on a touch screen, including while the chat scrolls", async () => {
    finePointer.matches = false;
    let reads = 0;
    const sc = document.createElement("div");
    Object.defineProperty(sc, "clientWidth", { get() { reads++; return 1200; } });
    Object.defineProperty(sc, "clientHeight", { get() { reads++; return 600; } });
    const boxes = vi.spyOn(sc, "getBoundingClientRect");
    const lines = vi.spyOn(sc, "querySelectorAll");
    const listen = vi.spyOn(sc, "addEventListener");
    act(() => root.render(
      <Profiler id="rail" onRender={() => commits++}>
        <NavRail ticks={Array.from({ length: 8 }, (_, i) => tick(i + 1))} scroller={sc} onJump={() => {}} reset="mac/s1" />
      </Profiler>,
    ));
    await settle();
    const mounted = commits;
    sc.dispatchEvent(new Event("scroll"));
    await settle();
    expect(host.querySelector("[data-nav-rail]")).toBeNull();
    expect(commits).toBe(mounted); // scrolling writes no state
    expect(reads).toBe(0); // no clientWidth / clientHeight
    expect(boxes).not.toHaveBeenCalled();
    expect(lines).not.toHaveBeenCalled();
    expect(listen).not.toHaveBeenCalled(); // no scroll listener, so the idle timer is never armed
  });

  it("does not paint a frozen rail when a precise pointer comes back", async () => {
    const width = { value: 1200 };
    const sc = document.createElement("div");
    Object.defineProperty(sc, "clientWidth", { get() { return width.value; } });
    Object.defineProperty(sc, "clientHeight", { value: 600 });
    let watch = false;
    let flashed = false;
    act(() => root.render(
      <Profiler id="rail" onRender={() => { if (watch && host.querySelector("[data-nav-rail]")) flashed = true; }}>
        <NavRail ticks={Array.from({ length: 8 }, (_, i) => tick(i + 1))} scroller={sc} onJump={() => {}} reset="mac/s1" />
      </Profiler>,
    ));
    await settle();
    expect(host.querySelectorAll("[data-nav-rail] button")).toHaveLength(8);
    // the chat then loses its margin (a rotation, a panel) while the pointer is gone
    width.value = 700;
    act(() => { finePointer.set(false); });
    await settle();
    expect(host.querySelector("[data-nav-rail]")).toBeNull();
    watch = true;
    act(() => { finePointer.set(true); });
    expect(flashed).toBe(false); // the commit that learns the pointer is back must not draw the old geometry
    await settle();
    expect(host.querySelectorAll("[data-nav-rail] button")).toHaveLength(8); // the right edge works in narrow desktops too
  });

  it("does not restore a hover preview after the precise pointer comes back", async () => {
    const sc = scrollerWith(1200, 600);
    act(() => root.render(
      <NavRail ticks={Array.from({ length: 8 }, (_, i) => tick(i + 1))} scroller={sc} onJump={() => {}} reset="mac/s1" />,
    ));
    await settle();
    const button = host.querySelector("button");
    expect(button).not.toBeNull();
    act(() => { button!.dispatchEvent(new MouseEvent("mouseover", { bubbles: true })); });
    expect(host.querySelector("[data-nav-rail-preview]")).not.toBeNull();
    act(() => { finePointer.set(false); });
    await settle();
    act(() => { finePointer.set(true); });
    await settle();
    expect(host.querySelectorAll("[data-nav-rail] button")).toHaveLength(8);
    expect(host.querySelector("[data-nav-rail-preview]")).toBeNull();
  });

  it("dismisses a hover card synchronously while the rail's independent idle timer remains", async () => {
    const sc = scrollerWith(700, 600);
    act(() => root.render(<NavRail ticks={[{ ...tick(1), answerExcerpt: "A short response." }, tick(2)]} scroller={sc} onJump={() => {}} reset="example/exit" />));
    await settle();
    vi.useFakeTimers();
    const button = host.querySelector("button")!;
    act(() => button.dispatchEvent(new MouseEvent("mouseover", { bubbles: true, relatedTarget: document.body })));
    const surface = host.querySelector<HTMLElement>("[data-nav-preview-surface]")!;
    expect(surface.style.padding).toBe(`${M.previewPadding}px`);
    expect(surface.classList.contains("rounded-nav-preview")).toBe(true);
    expect(host.querySelector("[data-nav-preview-title]")?.classList.contains("truncate")).toBe(true);
    expect(host.querySelector("[data-nav-preview-excerpt]")?.classList.contains("line-clamp-2")).toBe(true);
    act(() => button.dispatchEvent(new MouseEvent("mouseout", { bubbles: true, relatedTarget: document.body })));
    expect(host.querySelector("[data-nav-preview]")).toBeNull(); // no timer advancement
    expect(host.querySelector<HTMLElement>("[data-nav-rail]")!.dataset.visible).toBe("true");
  });

  it("keeps a preview across the rail/card hit zones but closes immediately when both are left", async () => {
    const sc = scrollerWith(700, 600);
    const jump = vi.fn();
    act(() => root.render(<NavRail ticks={[tick(1), tick(2)]} scroller={sc} onJump={jump} reset="example/card" />));
    await settle();
    const button = host.querySelector("button")!;
    act(() => button.dispatchEvent(new MouseEvent("mouseover", { bubbles: true, relatedTarget: document.body })));
    const card = host.querySelector<HTMLElement>("[data-nav-preview]")!;
    act(() => button.dispatchEvent(new MouseEvent("mouseout", { bubbles: true, relatedTarget: card })));
    expect(host.querySelector("[data-nav-preview]")).toBe(card);
    expect(card.style.paddingRight).toBe(`${M.previewGap}px`);
    const rail = host.querySelector<HTMLElement>("[data-nav-rail]")!;
    rail.setPointerCapture = vi.fn();
    const press = new MouseEvent("pointerdown", { bubbles: true, cancelable: true, button: 0 });
    Object.defineProperty(press, "pointerId", { value: 7 });
    act(() => card.dispatchEvent(press));
    expect(press.defaultPrevented).toBe(false); // selecting card text must not scrub or jump
    expect(rail.setPointerCapture).not.toHaveBeenCalled();
    expect(jump).not.toHaveBeenCalled();
    act(() => card.dispatchEvent(new MouseEvent("mouseout", { bubbles: true, relatedTarget: document.body })));
    expect(host.querySelector("[data-nav-preview]")).toBeNull();
  });

  it("retains a keyboard-owned preview until focus leaves, independently of mouse exit", async () => {
    const sc = scrollerWith(700, 600);
    act(() => root.render(<NavRail ticks={[tick(1), tick(2)]} scroller={sc} onJump={() => {}} reset="example/keyboard" />));
    await settle();
    const rail = host.querySelector<HTMLElement>("[data-nav-rail]")!;
    const button = host.querySelector("button")!;
    act(() => button.dispatchEvent(new MouseEvent("mouseover", { bubbles: true, relatedTarget: document.body })));
    act(() => rail.focus());
    act(() => rail.dispatchEvent(new KeyboardEvent("keydown", { key: "End", bubbles: true })));
    act(() => button.dispatchEvent(new MouseEvent("mouseout", { bubbles: true, relatedTarget: document.body })));
    expect(host.querySelector("[data-nav-preview]")?.textContent).toBe("question 2");
    expect(host.querySelector<HTMLElement>('[data-tick="1"] [data-nav-mark]')!.style.width).toBe(`${M.hoverWidth}px`);
    act(() => rail.blur());
    expect(host.querySelector("[data-nav-preview]")).toBeNull();
    expect([...host.querySelectorAll<HTMLElement>("[data-nav-mark]")].every((mark) => mark.style.width === `${M.tickWidth}px`)).toBe(true);
  });

  it("does not move a keyboard selection out of the window when entering its card", async () => {
    const sc = scrollerWith(700, 120);
    act(() => root.render(<NavRail ticks={Array.from({ length: 50 }, (_, i) => tick(i))} scroller={sc} onJump={() => {}} reset="example/card-window" />));
    await settle();
    const rail = host.querySelector<HTMLElement>("[data-nav-rail]")!;
    act(() => rail.focus());
    act(() => rail.dispatchEvent(new KeyboardEvent("keydown", { key: "Home", bubbles: true })));
    const card = host.querySelector<HTMLElement>("[data-nav-preview]")!;
    act(() => card.dispatchEvent(new MouseEvent("mouseover", { bubbles: true, relatedTarget: document.body })));
    expect(host.querySelector('[data-tick="0"]')).not.toBeNull();
    expect(card.textContent).toBe("question 0");
  });

  it("holds a captured drag preview outside the rail and clears it at outside release", async () => {
    const sc = scrollerWith(700, 600);
    const jump = vi.fn();
    act(() => root.render(<NavRail ticks={[tick(1), tick(2), tick(3)]} scroller={sc} onJump={jump} reset="example/drag-exit" />));
    await settle();
    const rail = host.querySelector<HTMLElement>("[data-nav-rail]")!;
    const button = host.querySelector("button")!;
    rail.setPointerCapture = vi.fn();
    rail.releasePointerCapture = vi.fn();
    const pointer = (type: string) => {
      const e = new MouseEvent(type, { bubbles: true, cancelable: true, button: 0, clientY: 300 });
      Object.defineProperty(e, "pointerId", { value: 7 });
      act(() => rail.dispatchEvent(e));
    };
    act(() => button.dispatchEvent(new MouseEvent("mouseover", { bubbles: true, relatedTarget: document.body })));
    pointer("pointerdown");
    act(() => button.dispatchEvent(new MouseEvent("mouseout", { bubbles: true, relatedTarget: document.body })));
    expect(host.querySelector("[data-nav-preview]")).not.toBeNull();
    expect(host.querySelector<HTMLElement>('[data-tick="1"] [data-nav-mark]')!.style.width).toBe(`${M.hoverWidth}px`);
    pointer("pointerup");
    expect(jump).toHaveBeenCalledWith("seg:2");
    expect(host.querySelector("[data-nav-preview]")).toBeNull();
    expect([...host.querySelectorAll<HTMLElement>("[data-nav-mark]")].every((mark) => mark.style.width === `${M.tickWidth}px`)).toBe(true);
  });

  it("starts hidden, wakes for scrolling and keyboard focus, and fades after quiet", async () => {
    const sc = scrollerWith(700, 600);
    act(() => root.render(<NavRail ticks={[tick(1), tick(2)]} scroller={sc} onJump={() => {}} reset="example/s1" />));
    await settle();
    const rail = host.querySelector<HTMLElement>("[data-nav-rail]")!;
    expect(rail.dataset.visible).toBe("false");
    vi.useFakeTimers();
    act(() => sc.dispatchEvent(new Event("scroll")));
    expect(rail.dataset.visible).toBe("true");
    await act(async () => vi.advanceTimersByTimeAsync(2100));
    expect(rail.dataset.visible).toBe("false");
    act(() => rail.focus());
    expect(rail.dataset.visible).toBe("true");
    await act(async () => vi.advanceTimersByTimeAsync(2100));
    expect(rail.dataset.visible).toBe("true");
    act(() => rail.blur());
    await act(async () => vi.advanceTimersByTimeAsync(2100));
    expect(rail.dataset.visible).toBe("false");
    vi.useRealTimers();
  });

  it("selects every loaded input with the keyboard in a bounded window without jumping until Enter", async () => {
    const sc = scrollerWith(700, 120);
    const jump = vi.fn();
    act(() => root.render(<NavRail ticks={Array.from({ length: 50 }, (_, i) => tick(i))} scroller={sc} onJump={jump} reset="example/s1" />));
    await settle();
    const rail = host.querySelector<HTMLElement>("[data-nav-rail]")!;
    const press = (key: string) => act(() => rail.dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true })));
    act(() => rail.focus());
    press("Home");
    expect(host.querySelector("[data-nav-preview]")?.textContent).toBe("question 0");
    for (let i = 1; i < 50; i++) {
      press("ArrowDown");
      expect(host.querySelector("[data-nav-preview]")?.textContent).toBe(`question ${i}`);
      expect(host.querySelector('[aria-live="polite"]')?.textContent).toContain(`question ${i}`);
      expect(host.querySelector(`[data-tick="${i}"]`)).not.toBeNull();
    }
    expect(host.querySelectorAll("button").length).toBeLessThan(20);
    expect(jump).not.toHaveBeenCalled();
    press("Enter");
    expect(jump).toHaveBeenLastCalledWith("seg:49");
    press("Escape");
    expect(host.querySelector("[data-nav-preview]")).toBeNull();
  });

  it("keeps preview identity when older inputs are prepended", async () => {
    const sc = scrollerWith(700, 600);
    const jump = vi.fn();
    const render = (ticks: NavRailTick[]) => act(() => root.render(<NavRail ticks={ticks} scroller={sc} onJump={jump} reset="example/s1" />));
    render([tick(2), tick(3)]);
    await settle();
    const rail = host.querySelector<HTMLElement>("[data-nav-rail]")!;
    act(() => rail.dispatchEvent(new KeyboardEvent("keydown", { key: "End", bubbles: true })));
    render([tick(0), tick(1), tick(2), tick(3)]);
    await settle();
    expect(host.querySelector("[data-nav-preview]")?.textContent).toBe("question 3");
    act(() => rail.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })));
    expect(jump).toHaveBeenCalledWith("seg:3");
  });

  it("freezes drag identities across prepends, commits once on release and never on cancellation", async () => {
    const sc = scrollerWith(700, 600);
    const jump = vi.fn();
    const render = (ticks: NavRailTick[]) => act(() => root.render(<NavRail ticks={ticks} scroller={sc} onJump={jump} reset="example/s1" />));
    render([tick(2), tick(3), tick(4)]);
    await settle();
    const rail = host.querySelector<HTMLElement>("[data-nav-rail]")!;
    rail.setPointerCapture = vi.fn();
    rail.releasePointerCapture = vi.fn();
    const pointer = (type: string, y: number) => {
      const e = new MouseEvent(type, { bubbles: true, cancelable: true, button: 0, clientY: y });
      Object.defineProperty(e, "pointerId", { value: 7 });
      act(() => rail.dispatchEvent(e));
    };
    pointer("pointerdown", 291);
    render([tick(0), tick(1), tick(2), tick(3), tick(4)]);
    await settle();
    pointer("pointermove", 300);
    expect(host.querySelector("[data-nav-preview]")?.textContent).toBe("question 3");
    expect(jump).not.toHaveBeenCalled();
    pointer("pointerup", 300);
    act(() => host.querySelector('[data-tick="3"]')!.dispatchEvent(new MouseEvent("click", { bubbles: true })));
    expect(jump).toHaveBeenCalledTimes(1);
    expect(jump).toHaveBeenLastCalledWith("seg:3");
    pointer("pointerdown", 291);
    pointer("pointercancel", 291);
    pointer("pointerup", 291);
    expect(jump).toHaveBeenCalledTimes(1);
    expect(host.querySelector("[data-nav-preview]")).toBeNull();
  });

  it("reads rows from the content's own children, and does no per-message layout reads on scroll frames", async () => {
    const sc = scrollerWith(700, 600);
    const content = document.createElement("div");
    content.dataset.chatContent = "";
    sc.append(content);
    const boxes = Array.from({ length: 40 }, (_, i) => {
      const row = document.createElement("div");
      row.dataset.line = `seg:${i}`;
      const child = document.createElement("div");
      child.getBoundingClientRect = vi.fn(() => ({ top: i * 100 - sc.scrollTop } as DOMRect));
      row.append(child);
      content.append(row);
      return child;
    });
    const query = vi.spyOn(sc, "querySelectorAll");
    act(() => root.render(<NavRail ticks={boxes.map((_, i) => tick(i))} scroller={sc} onJump={() => {}} reset="example/s1" />));
    await settle();
    expect(query).not.toHaveBeenCalled(); // the measure walks the content's children, not the transcript subtree
    for (const child of boxes) vi.mocked(child.getBoundingClientRect).mockClear();
    act(() => { sc.scrollTop = 530; sc.dispatchEvent(new Event("scroll")); });
    await settle();
    expect(host.querySelector('[aria-current="true"]')?.getAttribute("data-tick")).toBe("5");
    expect(query).not.toHaveBeenCalled();
    for (const child of boxes) expect(child.getBoundingClientRect).not.toHaveBeenCalled();
  });
});
