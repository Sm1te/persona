// @vitest-environment jsdom
// An agent's chat on screen is seen (lib/store.ts viewSession): opened, it registers; closed or switched, it lets go.
import { act, StrictMode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Item, Session } from "../../lib/types";

vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
  HTMLElement.prototype.scrollTo = () => {};
  g.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} };
  g.IntersectionObserver = class { observe() {} unobserve() {} disconnect() {} takeRecords() { return []; } };
});

const captured = vi.hoisted(() => ({
  session: { machine_id: "hub", session_id: "c1", title: "Conversation", cwd: "/code", status: "done", state: "unread" } as Session,
  send: undefined as undefined | ((out: { text: string; images: [] }) => Promise<boolean>),
  views: [] as string[],
  released: [] as string[],
}));

vi.mock("./Composer", async (importOriginal) => ({ ...await importOriginal<typeof import("./Composer")>(), Composer: ({ onSend }: { onSend: NonNullable<typeof captured.send> }) => { captured.send = onSend; return null; } }));
const transcript = vi.hoisted(() => ({ items: [] as Item[], loading: false, error: "", hasMore: false, streaming: "" }));
vi.mock("./useTranscript", () => ({ useTranscript: () => transcript }));
vi.mock("../../lib/store", async (importOriginal) => {
  const store = await importOriginal<typeof import("../../lib/store")>();
  return {
    ...store,
    getSession: () => captured.session,
    useStore: <T,>(select: (s: ReturnType<typeof store.getState>) => T) => select({ ...store.getState(), sessions: { hub: [captured.session] } }),
    viewSession: (mid: string, sid: string) => {
      captured.views.push(`${mid}/${sid}`);
      return () => captured.released.push(`${mid}/${sid}`);
    },
  };
});
vi.mock("../../lib/panel", async (importOriginal) => ({ ...await importOriginal<typeof import("../../lib/panel")>(), usePanel: () => null }));
vi.mock("../../lib/router", async (importOriginal) => ({
  ...await importOriginal<typeof import("../../lib/router")>(),
  useRoute: () => ({ name: "session", mid: "hub", id: "c1" }),
}));
vi.mock("../assistant/Persona", () => ({ usePersona: () => ({ name: "Persona" }) }));
vi.mock("../assistant/AssistantChat", () => ({
  AssistantFace: () => null,
  CompactionStrip: () => null,
  useAssistantMenu: () => ({ items: [], dialogs: null }),
}));
vi.mock("../assistant/Tasks", () => ({
  DayLine: () => null,
  FeedLineView: () => null,
  QuickResultCard: () => null,
  RouteChip: () => null,
  AssistantActionBar: () => null,
  TaskDock: () => null,
  TaskQuestions: () => null,
}));

const { ChatView } = await import("./ChatView");
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;
beforeEach(() => {
  transcript.items = [];
  captured.send = undefined;
  captured.views = [];
  captured.released = [];
  localStorage.clear();
  vi.stubGlobal("fetch", vi.fn(() => Promise.resolve(new Response("{}", { status: 200 }))));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
});

describe("an agent's chat on screen", () => {
  it("is viewed while it is shown, and let go when another one opens", () => {
    act(() => root.render(<ChatView mid="hub" sid="c1" inPage />));
    expect(captured.views).toEqual(["hub/c1"]);
    act(() => root.render(<ChatView mid="hub" sid="c2" inPage />));
    expect(captured.released).toEqual(["hub/c1"]);
    expect(captured.views).toEqual(["hub/c1", "hub/c2"]);
  });

  it("is not viewed again on every push (a state change is the store's to follow)", () => {
    act(() => root.render(<ChatView mid="hub" sid="c1" inPage />));
    captured.session = { ...captured.session, state: "working", status: "working" };
    act(() => root.render(<ChatView mid="hub" sid="c1" inPage />));
    captured.session = { ...captured.session, state: "unread", status: "done" };
    act(() => root.render(<ChatView mid="hub" sid="c1" inPage />));
    expect(captured.views).toEqual(["hub/c1"]);
  });
});

it("removes the sending bubble when its transcript echo arrives under StrictMode", async () => {
  const text = "Example message for StrictMode";
  const render = () => root.render(<StrictMode><ChatView mid="hub" sid="c1" inPage /></StrictMode>);
  await act(async () => render());
  await act(async () => { expect(await captured.send!({ text, images: [] })).toBe(true); });
  expect(host.querySelectorAll("[data-user-bubble]:not([data-items])")).toHaveLength(1);
  expect(host.querySelector("[data-user-bubble]")?.textContent).toContain(text);

  transcript.items = [{ id: "echo-1", seq: 1, role: "user", kind: "text", text, ts: new Date().toISOString() } as Item];
  await act(async () => render());
  expect(host.querySelectorAll("[data-user-bubble]:not([data-items])")).toHaveLength(0);
  expect(host.querySelectorAll("[data-user-bubble]")).toHaveLength(1);
  expect(host.querySelector('[data-user-bubble][data-items="echo-1"]')?.textContent).toContain(text);
});
