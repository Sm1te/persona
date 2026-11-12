// @vitest-environment jsdom
// The hand-off card (`secret_handoff`, #3962) on the web: it says which variables an Agent gets, from which item, on
// which computer, where it runs and what it is asked, never a reference or a value. Only its own buttons approve it, and
// only their approval carries the card's feature (X-Persona-Features: secret_handoff), the hub's condition for letting it
// through; the quick paths (Overview's row, a notification's Allow) never take it, and an approval from anywhere else is
// refused before a request. A no goes from anywhere. The hub is faked (apiOp).
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
const calls = vi.hoisted(() => [] as { path: string; body: Record<string, unknown>; headers?: Record<string, string> }[]);
vi.mock("../../lib/ops", () => ({
  apiOp: vi.fn(async (path: string, init: { json: Record<string, unknown>; headers?: Record<string, string> }) => {
    calls.push({ path, body: init.json, headers: init.headers });
    return {};
  }),
}));
import { InteractionCard } from "./Interactions";
import { answerCard } from "../../lib/card-answer";
import { NOTE_MESSAGE, planClick } from "../../lib/notification-answer";
import { quickAnswers } from "../../lib/quick-answers";
import { getState } from "../../lib/store";
import type { Interaction, Session } from "../../lib/types";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 5; i++) await act(async () => { await Promise.resolve(); });
};
const button = (label: string) => [...host.querySelectorAll("button")].find((b) => b.textContent?.trim() === label) as HTMLButtonElement | undefined;

const card = (extra: Partial<Interaction> = {}): Interaction => ({
  id: "ix_handoff", machine_id: "server", session_id: "s", session_title: "Persona", kind: "permission", tool_name: "secret_handoff",
  tool_input: {
    variables: [
      { name: "GITHUB_TOKEN", provider: "onepassword", provider_name: "1Password", title: "GitHub token › credential" },
      { name: "NPM_TOKEN", provider: "onepassword", provider_name: "1Password", title: "npm › credential" },
    ],
    agent: "Fix CI", machine_name: "MacBook", cwd: "/code/app", prompt: "Fix the release CI",
  },
  summary: "Give 1Password › GitHub token › credential as GITHUB_TOKEN to “Fix CI” on MacBook, for this run only. Approve on iPhone or the web.",
  always_label: "以后在你自己开的对话里，MacBook 上的 claude 用这些条目不再询问",
  ...extra,
});

const session = () => {
  Object.assign(getState(), { sessions: { server: [{ machine_id: "server", session_id: "s" } as Session] }, interactions: [], answered: {} });
};
const render = (ix: Interaction) => act(() => root.render(<InteractionCard ix={ix} />));

beforeEach(() => {
  calls.length = 0;
  session();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

describe("the hand-off card", () => {
  it("says what goes where, never a reference", async () => {
    await render(card());
    const text = host.textContent ?? "";
    expect(text).toContain("GITHUB_TOKEN");
    expect(text).toContain("1Password › GitHub token › credential");
    expect(text).toContain("MacBook");
    expect(text).toContain("Fix CI");
    expect(host.querySelector("[data-handoff-cwd]")?.textContent).toBe("/code/app");
    expect(host.querySelector("[data-handoff-prompt]")?.textContent).toBe("Fix the release CI");
    expect(text).not.toContain("op://");
    expect(host.querySelectorAll("[data-handoff-variable]")).toHaveLength(2);
  });

  it("approves only from its buttons, with the card's feature on that request", async () => {
    await render(card());
    await act(async () => button("允许")!.click());
    await flush();
    expect(calls).toHaveLength(1);
    expect(calls[0].body).toEqual({ allow: true });
    expect(calls[0].headers).toEqual({ "X-Persona-Features": "secret_handoff" });
  });

  it("keeps an Always when it offers one", async () => {
    await render(card());
    await act(async () => button("总是允许")!.click());
    await flush();
    expect(calls[0].body).toEqual({ allow: true, always: true });
    expect(calls[0].headers).toEqual({ "X-Persona-Features": "secret_handoff" });
    calls.length = 0;
    await render(card({ id: "ix_handoff_2", always_label: undefined }));
    expect(button("总是允许")).toBeUndefined();
  });

  it("denies without the feature", async () => {
    await render(card());
    await act(async () => button("拒绝")!.click());
    await flush();
    expect(calls).toHaveLength(1);
    expect(calls[0].body).toEqual({ allow: false });
    expect(calls[0].headers).toBeUndefined();
  });

  it("offers only Deny when it says nothing it hands over", async () => {
    await render(card({ tool_input: {} }));
    expect(button("允许")!.disabled).toBe(true);
    expect(button("拒绝")!.disabled).toBe(false);
  });

  it("is never approved from a quick path or another sender", async () => {
    expect(quickAnswers(card())).toBeNull();
    const ix = card();
    expect(planClick({ type: NOTE_MESSAGE, ix: ix.id, action: "allow", until: Date.now() + 60_000 }, [ix], Date.now())).toBeUndefined();
    const r = await answerCard(card(), { allow: true });
    expect(r.ok).toBe(false);
    expect(calls).toHaveLength(0);
    const no = await answerCard(card(), { allow: false });
    expect(no.ok).toBe(true);
    expect(calls).toHaveLength(1);
    expect(calls[0].headers).toBeUndefined();
  });
});
