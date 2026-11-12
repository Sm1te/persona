// @vitest-environment jsdom
// The status line changes about once a second while an agent works (#5417): only the live group's thinking card reads
// it. It used to come down through ChatView and SegmentView into every work group's props, so each push regrouped the
// live turn and rendered every one of its work groups again.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});

// every WorkGroup render reads its title once: counted
const titles = vi.hoisted(() => ({ n: 0 }));
vi.mock("../../lib/work-groups", async (load) => {
  const real = await load<typeof import("../../lib/work-groups")>();
  return { ...real, groupTitle: (...a: Parameters<typeof real.groupTitle>) => (titles.n++, real.groupTitle(...a)) };
});

import { receiveLive, resetLive } from "../../lib/live";
import { reset, upsertSession } from "../../lib/store";
import { segments } from "../../lib/turns";
import type { Item, Session } from "../../lib/types";
import { SegmentView } from "./SegmentView";
import { forgetExpanded } from "./expanded";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const T0 = Date.parse("2026-11-06T10:00:00Z");
const at = (s: number) => new Date(T0 + s * 1000).toISOString();
let seq = 0;
const it_ = (x: Partial<Item>, ts: number): Item => ({ id: `i${++seq}#0`, seq, ts: at(ts), ...x }) as Item;
const toolRun = (n: number, ts: number) => [
  it_({ role: "assistant", kind: "tool_use", tool_name: "Bash", tool_use_id: `c${n}`, tool_input: { command: `step ${n}` } }, ts),
  it_({ role: "user", kind: "tool_result", tool_use_id: `c${n}`, text: "ok" }, ts + 1),
];
// one exchange still going: three runs of tools with words between them, the last run live
const items: Item[] = [
  it_({ role: "user", kind: "text", text: "run the checks" }, 0),
  ...toolRun(1, 1),
  it_({ role: "assistant", kind: "text", text: "first part done" }, 3),
  ...toolRun(2, 4),
  it_({ role: "assistant", kind: "text", text: "second part done" }, 6),
  ...toolRun(3, 7),
];

let host: HTMLDivElement;
let root: Root;
beforeEach(() => {
  forgetExpanded();
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(T0 + 10_000);
  upsertSession({ machine_id: "m", session_id: "s", status: "working", state: "working", title: "checks" } as Session);
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  resetLive();
  reset();
  vi.useRealTimers();
});

describe("status-line pushes", () => {
  it("do not redraw the folded work; the thinking card follows only after manual expansion", () => {
    const [seg] = segments(items);
    act(() => root.render(<SegmentView seg={seg} live mid="m" sid="s" expandFold={async () => {}} />));
    expect(titles.n).toBeGreaterThan(0);
    expect(host.querySelector('[data-thinking="live"]')).toBeNull();
    titles.n = 0;
    for (let s = 1; s <= 5; s++) act(() => receiveLive({ machine_id: "m", session_id: "s", live: { verb: "Frosting", thinking: true, elapsed_s: s } }));
    expect(titles.n).toBe(0);
    expect(host.querySelector('[data-thinking="live"]')).toBeNull();
    act(() => (host.querySelector("[data-work-group] > button") as HTMLButtonElement).click());
    expect(host.querySelectorAll('[data-thinking="live"]')).toHaveLength(1); // once, in the live inner group (#5155)
    titles.n = 0;
    act(() => receiveLive({ machine_id: "m", session_id: "s", live: { verb: "Frosting", thinking: false, elapsed_s: 6 } }));
    expect(titles.n).toBe(0);
    expect(host.querySelector('[data-thinking="live"]')).toBeNull();
  });

  it("keeps narration through new tool calls, replaces it on new words, and folds it when the turn ends", () => {
    let transcript: Item[] = [it_({ role: "user", kind: "text", text: "Check the change" }, 0),
      it_({ role: "assistant", kind: "text", text: "The fix is ready; checking it now" }, 1)];
    const draw = (live = true) => act(() => root.render(<SegmentView seg={segments(transcript)[0]} live={live} mid="m" sid="s" expandFold={async () => {}} />));
    const preview = () => host.querySelector("[data-latest-text]")!;
    draw();
    for (let n = 1; n <= 7; n++) {
      transcript = [...transcript, ...toolRun(n, n * 2)];
      draw();
      expect(preview().textContent).toContain("The fix is ready; checking it now");
      expect(host.querySelectorAll("[data-latest-text]")).toHaveLength(1);
      expect(host.querySelector("[data-live-preview]")).toBeNull();
    }
    transcript = [...transcript, it_({ role: "assistant", kind: "text", text: "Reviewing the final result" }, 17),
      it_({ role: "assistant", kind: "thinking", text: "  " }, 18)];
    draw();
    expect(preview().textContent).toContain("Reviewing the final result");
    expect(preview().textContent).not.toContain("The fix is ready; checking it now");

    act(() => (host.querySelector('[data-work-group="live"] > button') as HTMLButtonElement).click());
    expect(host.querySelector("[data-work-detail]")?.textContent).toContain("The fix is ready; checking it now");
    act(() => (host.querySelector('[data-work-group="live"] > button') as HTMLButtonElement).click());
    transcript = [...transcript, it_({ role: "assistant", kind: "text", text: "The change is verified." }, 19)];
    draw(false);
    expect(host.querySelector("[data-live-preview]")).toBeNull();
    expect(host.textContent).toContain("The change is verified.");
    expect(host.querySelector("[data-latest-text]")?.textContent).not.toContain("Reviewing the final result");
    expect(host.querySelector("[data-work-group] > button")?.getAttribute("aria-expanded")).toBe("false");
    expect((host.querySelector("[data-work-detail]")?.parentElement?.parentElement as HTMLElement)?.style.opacity).toBe("0");
  });
});
