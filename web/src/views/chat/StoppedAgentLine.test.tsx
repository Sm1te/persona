// @vitest-environment jsdom
// A stopped Agent's chat (#4754): one read-only line where the composer goes, with Terminal and Resume. Resume opens the
// terminal and posts the resume (no message); a refusal shows in words on the line.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { t } from "../../i18n";
import { clearResume } from "../../lib/agent-resume";
import { StoppedAgentLine } from "./StoppedAgentLine";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let el: HTMLDivElement;
let root: Root;
let onTerminal: ReturnType<typeof vi.fn<() => void>>;
const button = (text: string) => [...el.querySelectorAll("button")].find((b) => b.textContent === text)!;
const flush = async () => {
  for (let i = 0; i < 5; i++) await act(async () => { await Promise.resolve(); });
};

beforeEach(() => {
  vi.stubGlobal("localStorage", { getItem: () => "device-token", setItem() {}, removeItem() {} });
  onTerminal = vi.fn<() => void>();
  el = document.createElement("div");
  document.body.append(el);
  root = createRoot(el);
  act(() => root.render(<StoppedAgentLine mid="m1" sid="s1" onTerminal={onTerminal} />));
});

afterEach(() => {
  act(() => root.unmount());
  el.remove();
  clearResume("m1", "s1");
  vi.unstubAllGlobals();
});

describe("a stopped Agent's read-only line", () => {
  it("says it has stopped and opens its terminal", () => {
    expect(el.textContent).toContain(t("chat-chatview.stopped_line"));
    act(() => button(t("chat-chatview.terminal")).click());
    expect(onTerminal).toHaveBeenCalledTimes(1);
  });

  it("resumes it from the terminal: no message, progress while it goes, the error in words when refused", async () => {
    const fetch = vi.fn(async () => new Response(JSON.stringify({ error: "computer offline", code: "node_unreachable", delivered: "none" }), { status: 503 }));
    vi.stubGlobal("fetch", fetch);
    act(() => button(t("chat-chatview.resume")).click());
    expect(onTerminal).toHaveBeenCalledTimes(1);
    expect(button(t("chat-chatview.resume")).getAttribute("aria-busy")).toBe("true");
    await flush();
    const [url, init] = (fetch.mock.calls as unknown as [string, RequestInit][])[0];
    expect(url).toBe("/api/v1/sessions");
    expect(JSON.parse(String(init.body))).toEqual({ machine_id: "m1", resume_session_id: "s1" });
    expect(el.querySelector('[role="alert"]')!.textContent).toBe(t("chat-chatview.resume_failed", { error: t("lib-api.computer_offline") }));
  });
});
