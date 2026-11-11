// @vitest-environment jsdom
// The effort slider (#5032): keys move one level (Home / End to the ends), a drag changes the level once, when it is let
// go, the colors are the level's own, and the default level's bubble says it is recommended.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { EffortSlider } from "./EffortSlider";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const STOPS = ["low", "medium", "high", "xhigh", "max"];
let host: HTMLDivElement;
let root: Root;
beforeEach(() => {
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

function draw(value: string, onChange = vi.fn(), disabled = false) {
  act(() => root.render(<EffortSlider stops={STOPS} value={value} recommended="high" labelOf={(e) => e.toUpperCase()} onChange={onChange} disabled={disabled} />));
  const slider = host.querySelector<HTMLElement>('[role="slider"]')!;
  return { slider, onChange };
}
const key = (el: Element, k: string) => act(() => { el.dispatchEvent(new KeyboardEvent("keydown", { key: k, bubbles: true })); });
function pointer(el: EventTarget, type: string, clientX: number) {
  const e = new MouseEvent(type, { bubbles: true, clientX, button: 0 });
  Object.assign(e, { pointerId: 1, isPrimary: true });
  act(() => { el.dispatchEvent(e); });
}

describe("the effort slider", () => {
  it("says its level and moves one level per key", () => {
    const { slider, onChange } = draw("high");
    expect(slider.getAttribute("aria-valuenow")).toBe("2");
    expect(slider.getAttribute("aria-valuetext")).toBe("HIGH");
    expect(host.textContent).toContain("HIGH");
    expect(host.textContent).toContain("推荐");
    key(slider, "ArrowRight");
    key(slider, "ArrowDown");
    key(slider, "Home");
    key(slider, "End");
    expect(onChange.mock.calls.map((c) => c[0])).toEqual(["xhigh", "medium", "low", "max"]);
  });

  it("does not go past its ends", () => {
    const { slider, onChange } = draw("max");
    key(slider, "ArrowUp");
    key(slider, "End");
    expect(onChange).not.toHaveBeenCalled();
  });

  it("a drag changes the level once, where it is let go", () => {
    const { slider, onChange } = draw("low");
    slider.getBoundingClientRect = () => ({ left: 0, width: 400, right: 400, top: 0, bottom: 38, height: 38, x: 0, y: 0, toJSON() {} });
    pointer(slider, "pointerdown", 10);
    pointer(document, "pointermove", 150);
    pointer(document, "pointermove", 290);
    expect(onChange).not.toHaveBeenCalled();
    expect(slider.getAttribute("aria-valuenow")).toBe("3");
    pointer(document, "pointerup", 300);
    expect(onChange.mock.calls).toEqual([["xhigh"]]);
  });

  it("draws the level's own color, and nothing moves when it is off", () => {
    const { slider, onChange } = draw("medium", vi.fn(), true);
    expect(host.querySelector<HTMLElement>("[data-effort-fill]")!.style.backgroundColor).toBe("var(--effort-medium)");
    expect(slider.tabIndex).toBe(-1);
    key(slider, "ArrowRight");
    expect(onChange).not.toHaveBeenCalled();
  });
});
