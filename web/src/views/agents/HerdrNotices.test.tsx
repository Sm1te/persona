// @vitest-environment jsdom
import { act, memo } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { HerdrWatch } from "../../lib/herdr-notice";
import { applyMachine, refresh, reset } from "../../lib/store";
import type { Machine } from "../../lib/types";
import { HerdrNotices } from "./HerdrNotices";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const machine: Machine = { id: "m1", name: "Laptop", online: true };
const Notices = memo(HerdrNotices);
let host: HTMLDivElement;
let root: Root;

beforeEach(async () => {
  vi.useFakeTimers();
  reset();
  vi.spyOn(document, "visibilityState", "get").mockReturnValue("visible");
  vi.stubGlobal("fetch", vi.fn(async (url: string) => new Response(JSON.stringify(
    new URL(url, "http://hub.example.com").pathname === "/api/v1/machines" ? { machines: [machine] } : {},
  ), { headers: { "Content-Type": "application/json" } })));
  await refresh();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  reset();
  vi.clearAllTimers();
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

it("updates a stopped engine's computer name without restarting its poll", async () => {
  const read = vi.fn(async () => ({ autostart: false, running: false, known: true }));
  const watch = new HerdrWatch({ read, start: read });
  await act(async () => root.render(<Notices watch={watch} />));
  expect(host.querySelector('[data-testid="herdr-notice"]')?.textContent).toContain("Laptop");
  expect(read).toHaveBeenCalledTimes(1);

  await act(async () => applyMachine({ ...machine, name: "Renamed computer" }));
  expect(host.querySelector('[data-testid="herdr-notice"]')?.textContent).toContain("Renamed computer");
  expect(host.textContent).not.toContain("Laptop");
  expect(read).toHaveBeenCalledTimes(1);

  await act(async () => applyMachine({ ...machine, online: false }));
  expect(host.querySelector('[data-testid="herdr-notice"]')).toBeNull();
  await act(async () => applyMachine({ ...machine, name: "Back online" }));
  expect(host.querySelector('[data-testid="herdr-notice"]')?.textContent).toContain("Back online");
  expect(read).toHaveBeenCalledTimes(2);
});

it("asks once a minute while shown, and at once when the tab comes back", async () => {
  const read = vi.fn(async () => ({ autostart: false, running: true, known: true }));
  const watch = new HerdrWatch({ read, start: read });
  await act(async () => root.render(<Notices watch={watch} />));
  expect(read).toHaveBeenCalledTimes(1);

  await act(async () => vi.advanceTimersByTime(59_000));
  expect(read).toHaveBeenCalledTimes(1);
  await act(async () => vi.advanceTimersByTime(1_000));
  expect(read).toHaveBeenCalledTimes(2);

  // coming back to the tab still reads right away, without waiting for the next minute
  await act(async () => document.dispatchEvent(new Event("visibilitychange")));
  expect(read).toHaveBeenCalledTimes(3);
});
