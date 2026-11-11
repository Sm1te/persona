// @vitest-environment jsdom
// The failure notice above the composer (iOS FailureNote): when it shows, what "Retry" does, "Open in Lark".
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Session } from "../../lib/types";
import { FailureNote, showsFailureNote } from "./FailureNote";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const failed: Session = { machine_id: "m", session_id: "s", title: "t", cwd: "/code", status: "idle", failure_reason: "⚠️ API Error: 529 overloaded" };

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

describe("showsFailureNote", () => {
  it("shows for an idle or offline session with a notice", () => {
    expect(showsFailureNote(failed)).toBe(true);
    expect(showsFailureNote({ ...failed, status: "offline" })).toBe(true);
  });

  it("hides while working, on a card, view-only or without send", () => {
    expect(showsFailureNote({ ...failed, status: "working" })).toBe(false);
    expect(showsFailureNote({ ...failed, status: "blocked" })).toBe(false);
    expect(showsFailureNote({ ...failed, pending_interactions: 1 })).toBe(false);
    expect(showsFailureNote({ ...failed, source_kind: "codex_history", capabilities: {} })).toBe(false);
    expect(showsFailureNote({ ...failed, failure_reason: undefined })).toBe(false);
  });

  it("stays gone for the notice 重试 was pressed on, through needs-you and back, until a new one comes", () => {
    const retried = "API Error: 529 overloaded";
    expect(showsFailureNote(failed, { retried })).toBe(false);
    expect(showsFailureNote({ ...failed, status: "blocked" }, { retried })).toBe(false);
    expect(showsFailureNote(failed, { retried })).toBe(false);
    expect(showsFailureNote({ ...failed, failure_reason: "⚠️ API Error: 500" }, { retried })).toBe(true);
  });
});

describe("FailureNote", () => {
  it("shows the reason without the warning sign and retries on 重试", () => {
    const onRetry = vi.fn();
    act(() => root.render(<FailureNote session={failed} onRetry={onRetry} />));
    expect(host.querySelector('[data-testid="chat-failure"]')!.textContent).toContain("API Error: 529 overloaded");
    expect(host.querySelector('[data-testid="chat-failure"]')!.textContent).not.toContain("⚠");
    act(() => host.querySelector<HTMLButtonElement>('[data-testid="chat-failure-retry"]')!.click());
    expect(onRetry).toHaveBeenCalledTimes(1);
  });

  it("links to Lark only for an https place", () => {
    act(() => root.render(<FailureNote session={{ ...failed, external_url: "https://example.com/chat" }} onRetry={() => {}} />));
    expect(host.querySelector<HTMLAnchorElement>('[data-testid="chat-failure-lark"]')!.href).toBe("https://example.com/chat");
    act(() => root.render(<FailureNote session={{ ...failed, external_url: "javascript:alert(1)" }} onRetry={() => {}} />));
    expect(host.querySelector('[data-testid="chat-failure-lark"]')).toBeNull();
    act(() => root.render(<FailureNote session={failed} onRetry={() => {}} />));
    expect(host.querySelector('[data-testid="chat-failure-lark"]')).toBeNull();
  });
});
