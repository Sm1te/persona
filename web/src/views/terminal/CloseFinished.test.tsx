// @vitest-environment jsdom
// "Close finished terminals" (#4754): a preview that closes nothing, a confirmation that lists what will close, then
// exactly the confirmed panes, a toast and the list read again. Nothing closes without the confirmation.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
const toasts = vi.hoisted(() => [] as { text: string; tone?: string }[]);
vi.mock("../../ui/atoms", async (load) => {
  const real = await load<typeof import("../../ui/atoms")>();
  return { ...real, toast: (text: string, tone?: string) => void toasts.push({ text, tone }) };
});
import { t } from "../../i18n";
import { buildTerminalTree, type PaneHostDTO } from "../../lib/terminals";
import { useCloseFinished } from "./CloseFinished";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const host: PaneHostDTO = {
  kind: "herdr", stream: true, control: true, new_tab: true,
  workspaces: [{ id: "w1", label: "app" }],
  tabs: [{ id: "t1", workspace_id: "w1", label: "agent-1" }],
  panes: [{ pane: "w1:p2", terminal: "pane:w1:p2", tab_id: "t1", workspace_id: "w1", agent_session: "stopped-1", foreground: "zsh" }],
};
const tree = buildTerminalTree({ hosts: [host] }, [{ machine_id: "m1", session_id: "stopped-1", title: "Write the docs", cwd: "/app", status: "offline", capabilities: { read: true, terminal: true } }]);

let el: HTMLDivElement;
let root: Root;
let reload: ReturnType<typeof vi.fn<(mid: string) => void>>;

function Harness() {
  const finished = useCloseFinished({ spacesOf: () => tree, reload });
  return (
    <>
      <button type="button" data-start onClick={() => void finished.start("m1")}>start</button>
      <span data-previewing>{finished.previewing ?? ""}</span>
      {finished.dialog}
    </>
  );
}

const flush = async () => {
  for (let i = 0; i < 5; i++) await act(async () => { await Promise.resolve(); });
};
const button = (text: string) => [...document.querySelectorAll("button")].find((b) => b.textContent === text)!;
const dialog = () => document.querySelector('[role="dialog"]');
const bodies = (fetch: ReturnType<typeof vi.fn>) => (fetch.mock.calls as [string, RequestInit][]).map(([url, init]) => [url, JSON.parse(String(init.body))]);

beforeEach(() => {
  vi.spyOn(Element.prototype, "getClientRects").mockReturnValue([{}] as unknown as DOMRectList);
  vi.stubGlobal("matchMedia", (q: string) => ({ matches: false, media: q, addEventListener() {}, removeEventListener() {} }));
  vi.stubGlobal("localStorage", { getItem: () => "device-token", setItem() {}, removeItem() {} });
  toasts.length = 0;
  reload = vi.fn<(mid: string) => void>();
  el = document.createElement("div");
  document.body.append(el);
  root = createRoot(el);
  act(() => root.render(<Harness />));
});

afterEach(() => {
  act(() => root.unmount());
  el.remove();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("Close finished terminals", () => {
  it("lists what the preview found, closes exactly those once confirmed, says how many and reads the list again", async () => {
    const replies = [
      { closed: [{ pane: "w1:p2", session_id: "stopped-1", tab_id: "t1", workspace_id: "w1" }], kept: [{ pane: "w1:p9", session_id: "s9", why: "vim" }], preview: true },
      { closed: [{ pane: "w1:p2", session_id: "stopped-1", tab_id: "t1", workspace_id: "w1" }], kept: [] },
    ];
    const fetch = vi.fn(async () => new Response(JSON.stringify(replies.shift()), { status: 200 }));
    vi.stubGlobal("fetch", fetch);

    act(() => (document.querySelector("[data-start]") as HTMLButtonElement).click());
    expect(document.querySelector("[data-previewing]")!.textContent).toBe("m1");
    await flush();
    expect(dialog()!.textContent).toContain(t("terminal-terminalpage.close_finished_title", { count: 1 }));
    expect([...document.querySelectorAll("[data-finished-list] li")].map((li) => li.textContent)).toEqual([t("lib-terminals.finished", { title: "Write the docs" })]);
    expect(dialog()!.textContent).toContain(t("terminal-terminalpage.close_finished_kept", { count: 1 }));
    expect(bodies(fetch)).toEqual([["/api/v1/machines/m1/panes/close_finished", { preview: true }]]);

    act(() => button(t("terminal-terminalpage.close_finished_confirm")).click());
    await flush();
    expect(bodies(fetch)).toEqual([
      ["/api/v1/machines/m1/panes/close_finished", { preview: true }],
      ["/api/v1/machines/m1/panes/close_finished", { panes: "w1:p2" }],
    ]);
    expect(toasts).toEqual([{ text: t("terminal-terminalpage.closed_finished", { count: 1 }), tone: "ok" }]);
    expect(reload).toHaveBeenCalledWith("m1");
  });

  it("closes nothing when the user cancels", async () => {
    const fetch = vi.fn(async () => new Response(JSON.stringify({ closed: [{ pane: "w1:p2", session_id: "stopped-1" }], kept: [], preview: true }), { status: 200 }));
    vi.stubGlobal("fetch", fetch);
    act(() => (document.querySelector("[data-start]") as HTMLButtonElement).click());
    await flush();
    act(() => button(t("common.cancel")).click());
    await flush();
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(toasts).toEqual([]);
  });

  it("says so when there is nothing to close, and when the computer refuses", async () => {
    const fetch = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ closed: [], kept: [], preview: true }), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ error: "denied" }), { status: 403 }));
    vi.stubGlobal("fetch", fetch);
    act(() => (document.querySelector("[data-start]") as HTMLButtonElement).click());
    await flush();
    expect(dialog()).toBeNull();
    expect(toasts[0]).toEqual({ text: t("terminal-terminalpage.finished_none"), tone: undefined });
    act(() => (document.querySelector("[data-start]") as HTMLButtonElement).click());
    await flush();
    expect(dialog()).toBeNull();
    expect(toasts[1].tone).toBe("error");
  });
});
