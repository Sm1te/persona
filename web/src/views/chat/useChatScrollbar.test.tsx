// @vitest-environment jsdom
import { act, Profiler, StrictMode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { CHAT_SCROLLBAR, useChatScrollbar } from "./useChatScrollbar";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let host: HTMLDivElement;
let root: Root;
let sc: HTMLDivElement;
let commits: number;
function Harness({ el }: { el: HTMLDivElement | null }) { useChatScrollbar(el); return null; }
beforeEach(() => {
  vi.useFakeTimers();
  vi.stubGlobal("CSS", { supports: vi.fn(() => true) });
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  sc = document.createElement("div");
  Object.defineProperties(sc, { offsetWidth: { value: 500 }, clientWidth: { value: 490 } });
  sc.getBoundingClientRect = () => ({ right: 500, top: 0, bottom: 600 } as DOMRect);
  commits = 0;
  act(() => root.render(<StrictMode><Profiler id="scrollbar" onRender={() => commits++}><Harness el={sc} /></Profiler></StrictMode>));
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});
const wait = () => act(() => vi.advanceTimersByTime(CHAT_SCROLLBAR.idleMs + 1));
const mouse = (type: string, x: number) => sc.dispatchEvent(new MouseEvent(type, { clientX: x, button: 0 }));

describe("native conversation scrollbar", () => {
  it("rearms after StrictMode replay, fades after quiet, and never rerenders the conversation", () => {
    const mounted = commits;
    expect(sc.dataset.chatScrollbar).toBe("native");
    for (let i = 0; i < 50; i++) sc.dispatchEvent(new Event("scroll"));
    expect(sc.dataset.scrollbarVisible).toBe("true");
    wait();
    expect(sc.dataset.scrollbarVisible).toBeUndefined();
    expect(commits).toBe(mounted);
  });
  it("reveals only on the scrollbar edge, not over messages or the inset navigation rail", () => {
    mouse("mousemove", 470);
    expect(sc.dataset.scrollbarVisible).toBeUndefined();
    mouse("mousemove", 495);
    wait();
    expect(sc.dataset.scrollbarVisible).toBe("true");
    mouse("mousemove", 470);
    wait();
    expect(sc.dataset.scrollbarVisible).toBeUndefined();
  });
  it("holds visibility through dragging outside the edge and releases on mouseup or window blur", () => {
    mouse("mousedown", 495);
    mouse("mousemove", 400);
    wait();
    expect(sc.dataset.scrollbarVisible).toBe("true");
    window.dispatchEvent(new MouseEvent("mouseup"));
    wait();
    expect(sc.dataset.scrollbarVisible).toBeUndefined();
    mouse("mousedown", 495);
    window.dispatchEvent(new Event("blur"));
    wait();
    expect(sc.dataset.scrollbarVisible).toBeUndefined();
  });
  it("fades after a native drag releases outside without any intervening mousemove", () => {
    mouse("mousedown", 495);
    window.dispatchEvent(new MouseEvent("mouseup", { clientX: 400, clientY: 200 }));
    wait();
    expect(sc.dataset.scrollbarVisible).toBeUndefined();
    mouse("mousedown", 495);
    window.dispatchEvent(new MouseEvent("mouseup", { clientX: 495, clientY: 700 }));
    wait();
    expect(sc.dataset.scrollbarVisible).toBeUndefined();
  });
  it("cleans up the old scroller and idle timer when React replaces the element", () => {
    sc.dispatchEvent(new Event("scroll"));
    act(() => root.render(<Harness el={document.createElement("div")} />));
    expect(sc.dataset.chatScrollbar).toBeUndefined();
    expect(sc.dataset.scrollbarVisible).toBeUndefined();
    sc.dispatchEvent(new Event("scroll"));
    wait();
    expect(sc.dataset.scrollbarVisible).toBeUndefined();
  });
  it("leaves unsupported browsers' native scrollbar untouched", () => {
    act(() => root.render(<Harness el={null} />));
    vi.mocked(CSS.supports).mockReturnValue(false);
    act(() => root.render(<Harness el={sc} />));
    expect(sc.dataset.chatScrollbar).toBeUndefined();
    sc.dispatchEvent(new Event("scroll"));
    expect(sc.dataset.scrollbarVisible).toBeUndefined();
  });
});
