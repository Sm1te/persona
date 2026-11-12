// @vitest-environment jsdom
// A parent's chat with the child sessions folded under it (#5120, after cindy): each child is a card in the exchange
// during which it started, inside that exchange's folded work (super rule 2); the children's requests are answered on
// the parent's page, each saying which child asks; a link to a child's own conversation opens the parent at its card.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Interaction, Item, Session } from "../../lib/types";

vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
  g.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} };
  g.IntersectionObserver = class { observe() {} unobserve() {} disconnect() {} takeRecords() { return []; } };
  (g.CSS as { escape?: (s: string) => string } | undefined) ?? (g.CSS = {});
  (g.CSS as { escape?: (s: string) => string }).escape ??= (s: string) => s;
});

const transcript = vi.hoisted(() => ({ items: [] as Item[], loading: false, error: "", hasMore: false, streaming: "", loadingEarlier: false, loadEarlier: async () => {}, expandFold: async () => {}, reload: () => {} }));
vi.mock("./useTranscript", () => ({ useTranscript: () => transcript }));
vi.mock("../../lib/panel", async (importOriginal) => ({ ...await importOriginal<typeof import("../../lib/panel")>(), usePanel: () => null }));

const { ChatView } = await import("./ChatView");
const { getState, reset, upsertSession } = await import("../../lib/store");
const { followChildSession, useSubagentLink } = await import("../../lib/subagent-link");
const { forgetExpanded } = await import("./expanded");
// the cards' own chunk (lazy, like a subagent's): in before the first chat opens its work
await import("./ChildSessionCard");
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const T0 = Date.parse("2026-11-12T10:00:00Z");
const at = (s: number) => new Date(T0 + s * 1000).toISOString();
let n = 0;
const item = (role: Item["role"], kind: Item["kind"], s: number, extra: Partial<Item> = {}): Item => ({ id: `i${++n}#0`, seq: n, role, kind, ts: at(s), ...extra });
const call = (name: string, s: number, end: number): Item[] => {
  const id = `c${n + 1}`;
  return [item("assistant", "tool_use", s, { tool_name: name, tool_use_id: id, tool_input: { command: "npm test" } }), item("user", "tool_result", end, { tool_use_id: id, text: "ok" })];
};
const items = (): Item[] => [
  item("user", "text", 0, { text: "Rework the login flow." }),
  ...call("Bash", 2, 3),
  ...call("Bash", 20, 21),
  item("assistant", "text", 30, { text: "The refresh now runs in the middleware." }),
  item("user", "text", 100, { text: "Have agents add the tests and check CI." }),
  ...call("Bash", 102, 103),
  ...call("Bash", 160, 161),
  item("assistant", "text", 200, { text: "Two agents are checking the change." }),
];
const parent: Session = { machine_id: "m", session_id: "p", title: "Rework the login flow", cwd: "/w", status: "idle", state: "idle", agent: "claude", children: { total: 3, working: 1 } };
const child = (sid: string, s: number, extra: Partial<Session> = {}): Session => ({ machine_id: "m", session_id: sid, title: `Task ${sid}`, cwd: "/w", status: "idle", state: "idle", agent: "codex", started_at: at(s), updated_at: at(s + 30), folded_under: "p", is_subagent: true, parent_session_id: "p", ...extra });

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 8; i++) await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
};
/** Until `ok` (the cards' chunk and their groups opening take a few turns on a loaded machine), at most about a second. */
const until = async (ok: () => boolean) => {
  for (let i = 0; i < 200 && !ok(); i++) await act(async () => { await new Promise((r) => setTimeout(r, 5)); });
};
const cardCount = () => host.querySelectorAll("[data-child-card]").length;
const segments = () => [...host.querySelectorAll("[data-segment]")];
const cardsIn = (el: Element) => [...el.querySelectorAll("[data-child-card]")].map((c) => c.getAttribute("data-tool-use"));
const openWork = async () => {
  for (const b of host.querySelectorAll<HTMLButtonElement>("[data-work-group] > button[aria-expanded='false']")) act(() => b.click());
  await flush();
};

beforeEach(() => {
  localStorage.clear();
  forgetExpanded();
  n = 0;
  transcript.items = items();
  transcript.hasMore = false;
  Object.assign(getState(), { loaded: true });
  upsertSession(parent);
  vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ items: [] }), { status: 200, headers: { "Content-Type": "application/json" } })));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  reset();
  vi.unstubAllGlobals();
});

describe("the children's cards in the parent's chat", () => {
  it("each sits in the exchange during which it started, in the folded work, by when it started", async () => {
    upsertSession(child("k1", 10));
    upsertSession(child("k2", 150, { state: "working", status: "working" }));
    upsertSession(child("late", 9000));
    // a grandchild folded under the same session, and a row of another session's: only the first is a card here
    upsertSession(child("grand", 170, { parent_session_id: "k2" }));
    upsertSession(child("other", 50, { folded_under: "q" }));
    act(() => root.render(<ChatView mid="m" sid="p" onToggleSidebar={() => {}} />));
    await flush();
    // folded (super rule 2): no card until the work summary is opened
    expect(host.querySelectorAll("[data-child-card]").length).toBe(0);
    await openWork();
    await until(() => cardCount() === 4);
    const [first, second] = segments();
    expect(cardsIn(first)).toEqual(["child:k1"]);
    expect(cardsIn(second)).toEqual(["child:k2", "child:grand", "child:late"]);
    // inside the work summary, never beside it
    for (const c of host.querySelectorAll("[data-child-card]")) expect(c.closest("[data-work-detail]")).not.toBeNull();
    expect(host.querySelector('[data-tool-use="child:k2"]')?.getAttribute("data-child-card")).toBe("running");
    // the exchange's last words stay outside the summary
    expect(second.querySelector("[data-latest-text]")?.textContent).toContain("Two agents are checking the change.");
  });

  it("one that started before the loaded history shows once that history is all in", async () => {
    upsertSession(child("old", -500));
    transcript.hasMore = true;
    act(() => root.render(<ChatView mid="m" sid="p" onToggleSidebar={() => {}} />));
    await flush();
    await openWork();
    expect(host.querySelectorAll("[data-child-card]").length).toBe(0);
    transcript.hasMore = false;
    transcript.items = [...transcript.items];
    act(() => root.render(<ChatView mid="m" sid="p" onToggleSidebar={() => {}} />));
    await flush();
    await openWork();
    await until(() => cardCount() === 1);
    expect(cardsIn(segments()[0])).toEqual(["child:old"]);
  });

  it("a push for a child redraws its card, not the exchanges", async () => {
    upsertSession(child("k1", 10, { state: "working", status: "working" }));
    act(() => root.render(<ChatView mid="m" sid="p" onToggleSidebar={() => {}} />));
    await flush();
    await openWork();
    await until(() => cardCount() === 1);
    const before = host.querySelector("[data-segment]");
    act(() => upsertSession(child("k1", 10, { state: "unread", status: "idle", updated_at: at(70) })));
    await flush();
    expect(host.querySelector("[data-segment]")).toBe(before);
    expect(host.querySelector('[data-tool-use="child:k1"]')?.getAttribute("data-child-card")).toBe("completed");
  });
});

describe("the children's requests on the parent's page", () => {
  const ask: Interaction = {
    id: "ix_kid", machine_id: "m", session_id: "k1", session_title: "Write the regression tests", folded_under: "p", kind: "question",
    questions: [{ question: "Which database should the tests use?", options: [{ label: "test" }, { label: "staging" }] }], created_at: at(40),
  } as Interaction;
  const approve: Interaction = {
    id: "ix_kid2", machine_id: "m", session_id: "k2", session_title: "Check the CI config", folded_under: "p", kind: "permission", tool_name: "Bash",
    tool_input: { command: "npm run migrate -- --env test" }, created_at: at(45),
  } as Interaction;
  const own: Interaction = { id: "ix_own", machine_id: "m", session_id: "p", kind: "permission", tool_name: "Bash", tool_input: { command: "ls" }, created_at: at(30) } as Interaction;

  it("shows them with its own, oldest first, each saying which child asks", async () => {
    upsertSession(child("k1", 10, { state: "needs_you", status: "blocked" }));
    upsertSession(child("k2", 150));
    Object.assign(getState(), { interactions: [own, ask, approve, { ...ask, id: "ix_elsewhere", folded_under: "q", session_id: "k9" }] });
    act(() => root.render(<ChatView mid="m" sid="p" onToggleSidebar={() => {}} />));
    await flush();
    const cards = [...host.querySelectorAll("[data-card]")].map((c) => c.getAttribute("data-card"));
    expect(cards).toEqual(["ix_own", "ix_kid", "ix_kid2"]);
    const from = (id: string) => host.querySelector(`[data-card="${id}"] [data-permission-from]`)?.textContent;
    expect(from("ix_kid")).toBe("来自子 Agent · Write the regression tests");
    expect(from("ix_kid2")).toBe("来自子 Agent · Check the CI config");
    expect(from("ix_own")).toBeUndefined();
    // answerable here: the child's row says it may be approved
    expect(host.querySelector<HTMLFieldSetElement>('[data-card="ix_kid2"] fieldset')?.disabled).toBe(false);
    // never inside the work summary
    expect(host.querySelector('[data-card="ix_kid"]')?.closest("[data-work-detail]")).toBeNull();
  });
});

describe("a link to a child's own conversation", () => {
  it("opens the parent's chat with the child's card opened, and is then done", async () => {
    upsertSession(child("k2", 150));
    let asked: string | undefined;
    function Probe() {
      asked = useSubagentLink("m", "p");
      return null;
    }
    act(() => void followChildSession("m", "k2", "p"));
    act(() => root.render(<><Probe /><ChatView mid="m" sid="p" onToggleSidebar={() => {}} /></>));
    await until(() => Boolean(host.querySelector('[data-tool-use="child:k2"] button[aria-expanded="true"]')));
    expect(asked).toBeUndefined();
    const card = host.querySelector('[data-tool-use="child:k2"]');
    expect(card).not.toBeNull();
    expect(card?.querySelector("button")?.getAttribute("aria-expanded")).toBe("true");
  });

  it("a child that is not this session's: just the chat", async () => {
    let asked: string | undefined;
    function Probe() {
      asked = useSubagentLink("m", "p");
      return null;
    }
    act(() => void followChildSession("m", "gone", "p"));
    act(() => root.render(<><Probe /><ChatView mid="m" sid="p" onToggleSidebar={() => {}} /></>));
    await flush();
    expect(asked).toBeUndefined();
    expect(host.querySelectorAll("[data-child-card]").length).toBe(0);
  });
});
