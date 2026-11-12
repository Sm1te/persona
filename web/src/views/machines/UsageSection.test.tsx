// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { t } from "../../i18n";
import type { MachineUsage, UsageRange } from "../../lib/tokens";
import { UsageSection } from "./UsageSection";

// the providers' names (#5029), without a read of the model configuration among the usage reads
const providerNames = vi.hoisted(() => ({ list: [] as { name: string; display_name?: string }[] }));
vi.mock("../../lib/provider-names", async (original) => ({ ...(await original<typeof import("../../lib/provider-names")>()), useProviderNames: () => providerNames.list }));

const usage: MachineUsage = {
  machine_id: "computer-a", online: true, range: "24h", step: 600,
  points: [{ t: 1_788_480_000, in: 100, out: 23, req: 4 }],
  total: { in: 100, out: 23, req: 4 },
  models: [{ name: "model-a", in: 60 }, { name: "model-b", in: 40 }],
  launchers: [{ name: "launcher-a", in: 60 }, { name: "launcher-b", in: 30 }, { name: "", in: 10 }],
  launchers_known: true,
};
const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), {
  status, headers: { "Content-Type": "application/json" },
});
type Read = { url: URL; signal: AbortSignal; resolve: (response: Response) => void };
let reads: Read[];
let root: Root;
let host: HTMLDivElement;
const flush = async () => { for (let i = 0; i < 6; i++) await act(async () => { await Promise.resolve(); }); };
const total = () => host.querySelector("dl dd")?.textContent;
const section = () => host.querySelector('[data-testid="machine-usage"]')!;
const select = (index: number) => host.querySelectorAll<HTMLSelectElement>("select")[index];
const options = (index: number) => [...select(index).options].map((o) => o.value);
const button = (label: string) => [...host.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent === label)!;
const rangeButton = (range: UsageRange) => button(t(`machines-usage.range_${range}`));
const retryButton = () => button(t("machines-usage.retry"));
const render = async (mid = "computer-a") => {
  await act(async () => root.render(<UsageSection mid={mid} heading={(right) => <header>{right}</header>} />));
  await flush();
};
const answer = async (read: Read, response: Response) => {
  await act(async () => read.resolve(response));
  await flush();
};
const report = (overrides: Partial<MachineUsage> = {}) => json({ usage: { ...usage, ...overrides } });
const clickRange = async (range: UsageRange) => { await act(async () => rangeButton(range).click()); await flush(); };
const choose = async (index: number, value: string) => {
  await act(async () => {
    const control = select(index);
    control.value = value;
    control.dispatchEvent(new Event("change", { bubbles: true }));
  });
  await flush();
};
const retry = async () => { await act(async () => retryButton().click()); await flush(); };
const visible = async (state: DocumentVisibilityState = "visible") => {
  Object.defineProperty(document, "visibilityState", { configurable: true, value: state });
  await act(async () => document.dispatchEvent(new Event("visibilitychange")));
  await flush();
};
const ready = async () => { await render(); await answer(reads[0], report()); expect(total()).toBe("123"); };

beforeEach(() => {
  vi.useFakeTimers();
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  Object.defineProperty(document, "visibilityState", { configurable: true, value: "visible" });
  vi.stubGlobal("ResizeObserver", class { observe() {} disconnect() {} });
  vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockReturnValue(640);
  reads = [];
  providerNames.list = [];
  // Keep late responses deliverable after abort, exercising ownership beyond cooperative cancellation.
  vi.stubGlobal("fetch", vi.fn((url: string, init: RequestInit) => new Promise<Response>((resolve) => {
    reads.push({ url: new URL(url, "http://localhost"), signal: init.signal!, resolve });
  })));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  vi.clearAllTimers();
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("scope replacement through UsageSection and the REST client", () => {
  it.each([
    ["range", "7d"], ["model", "model-b"], ["launcher", "launcher-b"], ["launcher", ""],
  ])("hides the previous report during a failed %s replacement (%s) and retries the selected scope", async (filter, value) => {
    await ready();
    const catalogs = [options(0), options(1)];
    if (filter === "range") await clickRange(value as UsageRange);
    else await choose(filter === "model" ? 0 : 1, value);
    const failed = reads.at(-1)!;
    expect(failed.url.pathname).toBe("/api/v1/machines/computer-a/usage");
    expect(failed.url.searchParams.get(filter)).toBe(value === "" ? "-" : value);
    expect(total()).toBeUndefined();
    expect(host.querySelector('svg[role="img"]')).toBeNull();
    expect(section().getAttribute("aria-busy")).toBe("true");
    expect([options(0), options(1)]).toEqual(catalogs);
    expect(select(0).disabled || select(1).disabled).toBe(false);
    await answer(failed, json({ error: "Synthetic usage read failed" }, 503));
    expect(total()).toBeUndefined();
    expect(host.querySelector('[role="alert"]')?.textContent).toContain("Synthetic usage read failed");
    expect(host.querySelector('[role="alert"]')?.textContent).toContain("HTTP 503");
    expect(host.textContent).not.toContain(t("machines-usage.stale_report"));
    expect(retryButton().disabled).toBe(false);
    await retry();
    expect(retryButton().disabled).toBe(true);
    expect(reads.at(-1)!.url.href).toBe(failed.url.href);
    await answer(reads.at(-1)!, report({ range: filter === "range" ? "7d" : "24h", total: { in: 4, out: 2 } }));
    expect(total()).toBe("6");
    expect(host.querySelector('[role="alert"]')).toBeNull();
    expect(section().getAttribute("aria-busy")).toBe("false");
    expect(host.querySelectorAll('svg[role="img"]')).toHaveLength(2);
  });

  it("keeps both filters in the scope and allows changing another selector after a failure", async () => {
    await ready();
    await choose(0, "model-b");
    await answer(reads.at(-1)!, report({ total: { in: 50 } }));
    await choose(1, "launcher-b");
    expect(total()).toBeUndefined();
    expect(reads.at(-1)!.url.searchParams.get("model")).toBe("model-b");
    expect(reads.at(-1)!.url.searchParams.get("launcher")).toBe("launcher-b");
    await answer(reads.at(-1)!, json({ error: "Synthetic combined read failed" }, 500));
    await choose(0, "model-a");
    expect(host.querySelector('[role="alert"]')).toBeNull();
    expect(reads.at(-1)!.url.searchParams.get("launcher")).toBe("launcher-b");
    await answer(reads.at(-1)!, report({ total: { in: 8 } }));
    expect(total()).toBe("8");
  });

  it("retires reports, catalogs, errors and requests when the actual machine prop changes", async () => {
    await ready();
    await clickRange("7d");
    const retired = reads.at(-1)!;
    await render("computer-b");
    expect(retired.signal.aborted).toBe(true);
    expect(total()).toBeUndefined();
    expect(options(0)).not.toContain("model-a");
    const current = reads.at(-1)!;
    expect(current.url.pathname).toBe("/api/v1/machines/computer-b/usage");
    await answer(current, json({ error: "Synthetic computer read failed" }, 500));
    await answer(retired, report({ range: "7d", total: { in: 999 } }));
    expect(total()).toBeUndefined();
    expect(host.querySelector('[role="alert"]')?.textContent).toContain("Synthetic computer read failed");
    await retry();
    await answer(reads.at(-1)!, report({ machine_id: "computer-b", models: [{ name: "model-c" }], total: { in: 9 } }));
    expect(total()).toBe("9");
    expect(options(0)).toContain("model-c");
    expect(options(0)).not.toContain("model-a");
    await render("computer-c");
    expect(host.querySelector('[role="alert"]')).toBeNull();
    await answer(reads.at(-1)!, json({ error: "Synthetic old hub" }, 404));
    await render("computer-a");
    expect(host.textContent).not.toContain(t("machines-usage.old_hub"));
    await answer(reads.at(-1)!, report());
    expect(total()).toBe("123");
  });

  it("exposes an initial failure and retry without disabling filter recovery", async () => {
    await render();
    expect(host.querySelector('[role="status"]')).not.toBeNull();
    await answer(reads[0], json({ error: "Synthetic initial read failed" }, 500));
    expect(host.querySelector('[role="alert"]')?.textContent).toContain("Synthetic initial read failed");
    expect(select(0).disabled).toBe(false);
    await retry();
    await answer(reads.at(-1)!, report());
    expect(total()).toBe("123");
  });

  it("keeps a selected option visible when a successful cross-filter catalog no longer contains it", async () => {
    await ready();
    await choose(0, "model-b");
    await choose(1, "launcher-b");
    await answer(reads.at(-1)!, report({ models: [], launchers: [], total: {} }));
    expect(total()).toBe("0");
    expect(select(0).value).toBe("model-b");
    expect(select(1).value).toBe("launcher-b");
    await choose(0, "\u0000all");
    expect(reads.at(-1)!.url.searchParams.has("model")).toBe(false);
    expect(reads.at(-1)!.url.searchParams.get("launcher")).toBe("launcher-b");
  });
});

describe("polling, ordering and compatibility", () => {
  it.each(["24h", "1h"] as const)("retains only same-scope data with explicit stale error and recovers on the %s poll", async (range) => {
    await ready();
    if (range === "1h") { await clickRange(range); await answer(reads.at(-1)!, report({ range })); }
    const count = reads.length;
    const interval = range === "1h" ? 20_000 : 60_000;
    await act(async () => vi.advanceTimersByTime(interval - 1));
    expect(reads).toHaveLength(count);
    await act(async () => vi.advanceTimersByTime(1));
    await answer(reads.at(-1)!, json({ error: "Synthetic poll failed" }, 503));
    expect(total()).toBe("123");
    expect(host.querySelector('[role="alert"]')?.textContent).toContain(t("machines-usage.stale_report"));
    expect(host.querySelector('[role="alert"]')?.textContent).toContain("Synthetic poll failed");
    await retry();
    expect(total()).toBe("123");
    await answer(reads.at(-1)!, report({ range, total: { in: 12 } }));
    expect(total()).toBe("12");
    expect(host.querySelector('[role="alert"]')).toBeNull();
    await act(async () => vi.advanceTimersByTime(interval));
    await answer(reads.at(-1)!, report({ range, total: { in: 15 } }));
    expect(total()).toBe("15");
  });

  it.each([200, 500, 404])("ignores a retired scope's late HTTP %s including its completion while the replacement is pending", async (status) => {
    await ready();
    await clickRange("7d");
    const retired = reads.at(-1)!;
    await clickRange("1h");
    const current = reads.at(-1)!;
    expect(retired.signal.aborted).toBe(true);
    await answer(retired, status === 200 ? report({ range: "7d", models: [{ name: "retired-model" }] }) : json({ error: "Retired failure" }, status));
    expect(total()).toBeUndefined();
    expect(section().getAttribute("aria-busy")).toBe("true");
    expect(host.textContent).not.toContain("Retired failure");
    expect(host.textContent).not.toContain(t("machines-usage.old_hub"));
    expect(options(0)).not.toContain("retired-model");
    await answer(current, report({ range: "1h", total: { in: 11 } }));
    expect(total()).toBe("11");
  });

  it.each([200, 500, 404])("orders overlapping same-scope refreshes even when the old HTTP %s ignores abort", async (status) => {
    await ready();
    await visible();
    const retired = reads.at(-1)!;
    await visible();
    const current = reads.at(-1)!;
    await answer(current, report({ total: { in: 14 } }));
    await answer(retired, status === 200 ? report({ total: { in: 999 } }) : json({ error: "Retired poll failure" }, status));
    expect(total()).toBe("14");
    expect(host.querySelector('[role="alert"]')).toBeNull();
    expect(host.textContent).not.toContain(t("machines-usage.old_hub"));
    expect(section().getAttribute("aria-busy")).toBe("false");
  });

  it("does not clear the current failure when an older same-scope success arrives", async () => {
    await ready();
    await visible();
    const retired = reads.at(-1)!;
    await visible();
    await answer(reads.at(-1)!, json({ error: "Current poll failure" }, 500));
    await answer(retired, report({ total: { in: 999 } }));
    expect(total()).toBe("123");
    expect(host.querySelector('[role="alert"]')?.textContent).toContain("Current poll failure");
  });

  it("pauses polling while hidden, refreshes on visibility and retires work on unmount", async () => {
    await ready();
    await visible("hidden");
    await act(async () => vi.advanceTimersByTime(120_000));
    expect(reads).toHaveLength(1);
    await visible();
    expect(reads).toHaveLength(2);
    await answer(reads.at(-1)!, report({ total: { in: 10 } }));
    await visible();
    const pending = reads.at(-1)!;
    await act(async () => root.unmount());
    expect(pending.signal.aborted).toBe(true);
    await answer(pending, json({ error: "Retired unmounted failure" }, 500));
    const count = reads.length;
    await act(async () => vi.advanceTimersByTime(120_000));
    await visible();
    expect(reads).toHaveLength(count);
    root = createRoot(host);
  });

  it("keeps same-scope unsupported truth through deferred retries until a successful recovery", async () => {
    await ready();
    const catalogs = [options(0), options(1)];
    await visible();
    await answer(reads.at(-1)!, json({ error: "Synthetic endpoint unavailable" }, 404));
    expect(total()).toBeUndefined();
    expect(host.querySelector('svg[role="img"]')).toBeNull();
    expect(host.textContent).toContain(t("machines-usage.old_hub"));
    await retry();
    expect(section().getAttribute("aria-busy")).toBe("true");
    expect(total()).toBeUndefined();
    expect(host.querySelector('svg[role="img"]')).toBeNull();
    expect(host.textContent).toContain(t("machines-usage.old_hub"));
    expect([options(0), options(1)]).toEqual(catalogs);
    expect(select(0).disabled || select(1).disabled).toBe(false);
    await answer(reads.at(-1)!, json({ error: "Synthetic endpoint still unavailable" }, 404));
    expect(total()).toBeUndefined();
    expect(host.textContent).toContain(t("machines-usage.old_hub"));
    await retry();
    expect(total()).toBeUndefined();
    expect(host.textContent).toContain(t("machines-usage.old_hub"));
    await answer(reads.at(-1)!, report({ total: { in: 18 } }));
    expect(total()).toBe("18");
    expect(host.textContent).not.toContain(t("machines-usage.old_hub"));
    expect(host.querySelectorAll('svg[role="img"]')).toHaveLength(2);
  });

  it("keeps unsupported-hub guidance scoped and recoverable, and accepts old nullable catalogs", async () => {
    await ready();
    await clickRange("7d");
    await answer(reads.at(-1)!, json({ error: "Synthetic missing endpoint" }, 404));
    expect(total()).toBeUndefined();
    expect(host.textContent).toContain(t("machines-usage.old_hub"));
    expect(host.querySelector('[role="alert"]')).toBeNull();
    expect(options(0)).toContain("model-b");
    await retry();
    await answer(reads.at(-1)!, json({ usage: { ...usage, range: "7d", points: null, models: null, launchers: null, launchers_known: false, total: {} } }));
    expect(total()).toBe("0");
    expect(host.textContent).not.toContain(t("machines-usage.old_hub"));
    expect(host.textContent).toContain(t("machines-usage.old_node"));
    expect(select(1).disabled).toBe(true);
    expect(select(0).disabled).toBe(false);
    await clickRange("1h");
    await answer(reads.at(-1)!, json({ error: "Synthetic upgraded read failed" }, 500));
    expect(host.querySelector('[role="alert"]')?.textContent).toContain("Synthetic upgraded read failed");
    expect(host.textContent).not.toContain(t("machines-usage.old_hub"));
  });
});

describe("hovering the usage charts", () => {
  // A day at 10-minute steps (144 points), the four kinds stacked, two offline steps.
  const step = 600;
  const points = Array.from({ length: 144 }, (_, i) =>
    i === 60 || i === 61 ? { t: 1_788_480_000 + i * step, gap: true } : { t: 1_788_480_000 + i * step, in: 4000 + 37 * i, out: 900 + 11 * i, cache_write: 300 + (i % 7) * 40, cache_read: 30_000 + 211 * i, req: 3 + (i % 4) });
  const shapes = () => [...host.querySelectorAll("polygon, polyline")].map((p) => p.getAttribute("points"));
  const charts = () => [...host.querySelectorAll<SVGSVGElement>('svg[role="img"]')];
  const crosshair = () => charts()[0].querySelector(':scope > g[pointer-events="none"] text')?.textContent;
  const move = (svg: SVGSVGElement, x: number) =>
    act(() => {
      svg.querySelector(':scope > rect[fill="transparent"]')!.dispatchEvent(new PointerEvent("pointermove", { bubbles: true, clientX: x }));
    });

  it("redraws the crosshair and the read-outs, not the areas and lines", async () => {
    vi.spyOn(Element.prototype, "getBoundingClientRect").mockReturnValue({ left: 0, top: 0, right: 588, bottom: 170, width: 588, height: 170, x: 0, y: 0, toJSON() {} } as DOMRect);
    await render();
    await answer(reads[0], report({ step, points, total: { in: 1e6, out: 2e5, cache_write: 5e4, cache_read: 8e6, req: 600 } }));
    expect(charts()).toHaveLength(2);
    const drawn = shapes();
    expect(drawn.length).toBeGreaterThanOrEqual(5);

    const toFixed = vi.spyOn(Number.prototype, "toFixed");
    for (let i = 0; i < 20; i++) move(charts()[i % 2], 15 + i * 27);
    const calls = toFixed.mock.calls.length;
    toFixed.mockRestore();
    // Before: both charts rebuilt every area and line on every move (51,920 toFixed calls for these 20 moves).
    expect(calls).toBeLessThan(200);
    expect(shapes()).toEqual(drawn);

    // positive control: the moment still follows the pointer
    move(charts()[1], 100);
    const first = crosshair();
    move(charts()[1], 400);
    expect(first).toMatch(/^\d\d:\d\d$/);
    expect(crosshair()).toMatch(/^\d\d:\d\d$/);
    expect(crosshair()).not.toBe(first);
  });
});

describe("launchers by their display names (#5029)", () => {
  it("names a provider by its display name with its ID; an rc alias and Other read as before", async () => {
    providerNames.list = [{ name: "launcher-a", display_name: "我的中转 站" }];
    await ready();
    expect([...select(1).options].map((o) => o.textContent)).toEqual([t("machines-usage.all_launchers"), "我的中转 站 (launcher-a)", "launcher-b", t("machines-usage.other_launcher")]);
    const card = [...host.querySelectorAll("h3")].find((h) => h.textContent === t("machines-usage.by_launcher"))!.parentElement!;
    const labels = [...card.querySelectorAll("button")].map((b) => b.querySelector("span")!);
    expect(labels.map((l) => [l.textContent, l.classList.contains("font-mono")])).toEqual([
      ["我的中转 站 launcher-a", false], ["launcher-b", true], [t("machines-usage.other_launcher"), true],
    ]);
    expect(labels[0].querySelector(".font-mono")!.textContent).toBe("launcher-a");
    // picked, then gone from the catalog: still said by its display name
    await choose(1, "launcher-a");
    await answer(reads.at(-1)!, report({ launchers: [], total: {} }));
    expect(select(1).selectedOptions[0].textContent).toBe("我的中转 站 (launcher-a)");
  });
});
