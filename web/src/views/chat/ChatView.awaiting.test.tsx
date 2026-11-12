// @vitest-environment jsdom
// Right after a send, before the hub says the agent is working (awaiting, #3654), the run bar already says Working…: the
// composer offers Interrupt and Esc stops it too, as during the turn (#2450). With nothing on its way, Esc sends nothing.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Item, Session } from "../../lib/types";
import { getQuotes, setQuotes } from "../../lib/drafts";

vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
  g.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} };
  g.IntersectionObserver = class { observe() {} unobserve() {} disconnect() {} takeRecords() { return []; } };
});

const captured = vi.hoisted(() => ({
  session: { machine_id: "hub", session_id: "c1", title: "Conversation", cwd: "/code", status: "idle", state: "idle" } as Session,
}));
// one transcript object for every render, as the hook keeps one (a new one each time re-runs the echo checks forever)
const transcript = vi.hoisted(() => ({ items: [] as Item[], loading: false, error: "", hasMore: false, streaming: "" }));
vi.mock("./useTranscript", () => ({ useTranscript: () => transcript }));
vi.mock("../../lib/store", async (importOriginal) => {
  const store = await importOriginal<typeof import("../../lib/store")>();
  return {
    ...store,
    getSession: () => captured.session,
    useStore: <T,>(select: (s: ReturnType<typeof store.getState>) => T) => select({ ...store.getState(), sessions: { hub: [captured.session] } }),
  };
});
vi.mock("../../lib/panel", async (importOriginal) => ({ ...await importOriginal<typeof import("../../lib/panel")>(), usePanel: () => null }));
vi.mock("../../lib/router", async (importOriginal) => ({ ...await importOriginal<typeof import("../../lib/router")>(), useRoute: () => ({ name: "session", mid: "hub", id: "c1" }) }));
vi.mock("../assistant/Persona", () => ({ usePersona: () => ({ name: "Persona" }) }));

const { ChatView } = await import("./ChatView");
const { t } = await import("../../i18n");
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;
let fetcher: ReturnType<typeof vi.fn>;
const flush = async () => {
  for (let i = 0; i < 4; i++) await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
};
const interrupts = () => fetcher.mock.calls.filter(([url]) => String(url).endsWith("/sessions/hub/c1/interrupt"));
const stopButton = () => host.querySelector<HTMLButtonElement>(`button[aria-label="${t("chat-composer.interrupt")}"]`);
const stopControl = () => host.querySelector(`button[aria-label="${t("chat-composer.interrupt")}"], button[aria-label="${t("chat-composer.stopping")}"]`);
const esc = () => act(() => void window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true })));

beforeEach(() => {
  localStorage.clear();
  setQuotes("hub/c1", []);
  captured.session = { machine_id: "hub", session_id: "c1", title: "Conversation", cwd: "/code", status: "idle", state: "idle", agent: "claude" } as Session;
  transcript.items = [];
  transcript.loading = false;
  transcript.error = "";
  fetcher = vi.fn(async () => new Response("{}", { status: 200, headers: { "Content-Type": "application/json" } }));
  vi.stubGlobal("fetch", fetcher);
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

describe("editing the last Claude prompt (#2945, #4503)", () => {
  const prompt = '> Quoted text\n\nRevise this paragraph';
  const error = "API Error: Opus 5.5's safeguards flagged this message. Details: [frontier_llm]";
  const box = () => host.querySelector<HTMLTextAreaElement>("textarea")!;
  const bar = () => host.querySelector('[data-testid="composer-editing"]');
  const rewinds = () => fetcher.mock.calls.filter(([url]) => String(url).endsWith("/sessions/hub/c1/rewind"));
  const messages = () => fetcher.mock.calls.filter(([url]) => String(url).endsWith("/sessions/hub/c1/messages"));
  const press = (key: string) => act(() => void box().dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true })));
  const pressEsc = () => press("Escape");
  const typeInto = (text: string) =>
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(box(), text);
      box().dispatchEvent(new Event("input", { bubbles: true }));
    });
  const sendButton = () => host.querySelector<HTMLButtonElement>(`button[aria-label="${t("chat-composer.send")}"]`)!;
  const items = (): Item[] => [
    { id: "u1#0", seq: 1, role: "user", kind: "text", text: "Earlier input", ts: "2020-01-01T00:00:00Z" },
    { id: "a1#0", seq: 2, role: "assistant", kind: "text", text: "Earlier reply" },
    { id: "u2#0", seq: 3, role: "user", kind: "text", text: prompt, ts: "2020-01-01T00:01:00Z" },
    { id: "a2#0", seq: 4, role: "assistant", kind: "text", text: error },
  ];
  const mountError = async (assistant = false) => {
    transcript.items = items();
    act(() => root.render(<ChatView mid="hub" sid="c1" assistant={assistant} />));
    await flush();
  };
  const editButton = () => [...host.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent === t("rewind.edit_last"));
  // past EDIT_SEND_SETTLE_MS: the wait for Claude's box after a rewind (#4605)
  const settle = () => act(async () => { await new Promise((r) => setTimeout(r, 2000)); });
  const rewound = (input_cleared = true) => new Response(JSON.stringify({ prompt, restore: "conversation", input_cleared, code_restored: false }));

  it.each(["Esc Esc", "↑"])("%s puts the last prompt in the box to edit; the rewind runs only when the edit is sent", async (how) => {
    let finish!: () => void;
    fetcher.mockImplementation(async (url) => {
      if (String(url).endsWith("/rewind")) {
        await new Promise<void>((resolve) => { finish = resolve; });
        return rewound();
      }
      return new Response("{}");
    });
    await mountError();
    if (how === "↑") press("ArrowUp");
    else { pressEsc(); pressEsc(); }
    await flush();
    expect(box().value).toBe("Revise this paragraph");
    expect(host.textContent).toContain("Quoted text");
    expect(bar()).not.toBeNull();
    expect(rewinds()).toHaveLength(0);
    typeInto("Revised paragraph");
    act(() => sendButton().click());
    await flush();
    expect(rewinds()).toHaveLength(1);
    expect(JSON.parse(rewinds()[0][1].body)).toEqual({ prompt, occurrence: 1, prompts_after: 0, restore: "conversation" });
    // the box keeps the edit, read-only, while the computer rewinds; nothing is sent yet
    expect(box().readOnly).toBe(true);
    expect(box().value).toBe("Revised paragraph");
    expect(messages()).toHaveLength(0);
    await act(async () => finish());
    await flush();
    // still read-only while Claude's box settles (#4605), then the message
    expect(messages()).toHaveLength(0);
    expect(box().readOnly).toBe(true);
    await settle();
    await flush();
    expect(messages()).toHaveLength(1);
    expect(JSON.parse(messages()[0][1].body).clear_draft).toBe(prompt);
    expect(JSON.parse(messages()[0][1].body).text).toBe("> Quoted text\n\nRevised paragraph");
    expect(box().value).toBe("");
    expect(bar()).toBeNull();
    expect(interrupts()).toHaveLength(0);
  });

  it("starts the same edit from the error card's button", async () => {
    await mountError();
    expect(editButton()).toBeDefined();
    await act(async () => editButton()!.click());
    await flush();
    expect(rewinds()).toHaveLength(0);
    expect(box().value).toBe("Revise this paragraph");
    expect(bar()).not.toBeNull();
  });

  it("Esc cancels the edit with nothing asked of the computer", async () => {
    await mountError();
    press("ArrowUp");
    await flush();
    pressEsc();
    await flush();
    expect(box().value).toBe("");
    expect(bar()).toBeNull();
    expect(getQuotes("hub/c1").list).toEqual([]);
    expect(rewinds()).toHaveLength(0);
    expect(messages()).toHaveLength(0);
    expect(interrupts()).toHaveLength(0);
  });

  it.each(["rewind_mismatch", "input_draft"])("sends nothing and keeps the edit when the node refuses the rewind: %s", async (code) => {
    fetcher.mockImplementation(async (url) => String(url).endsWith("/rewind")
      ? new Response(JSON.stringify({ error: "Cannot rewind", code }), { status: 409 })
      : new Response("{}"));
    await mountError();
    press("ArrowUp");
    await flush();
    typeInto("Revised paragraph");
    act(() => sendButton().click());
    await flush();
    expect(rewinds()).toHaveLength(1);
    expect(messages()).toHaveLength(0);
    expect(box().value).toBe("Revised paragraph");
    expect(box().readOnly).toBe(false);
    expect(bar()).not.toBeNull();
  });

  it("after a rewind that left the old prompt in Claude's input, sends with that exact prompt to clear (#4605)", async () => {
    fetcher.mockImplementation(async (url) => (String(url).endsWith("/rewind") ? rewound(false) : new Response("{}")));
    await mountError();
    press("ArrowUp");
    await flush();
    act(() => sendButton().click());
    await flush();
    expect(rewinds()).toHaveLength(1);
    await settle();
    await flush();
    expect(messages()).toHaveLength(1);
    expect(JSON.parse(messages()[0][1].body).clear_draft).toBe(prompt);
  });

  it("a newer message ends the edit: the words stay and go as a new message, with no rewind", async () => {
    await mountError();
    press("ArrowUp");
    await flush();
    typeInto("Revised paragraph");
    transcript.items = [...items(), { id: "u3#0", seq: 5, role: "user", kind: "text", text: "Sent from the phone", ts: "2020-01-01T00:02:00Z" }];
    act(() => root.render(<ChatView mid="hub" sid="c1" />));
    await flush();
    expect(bar()).toBeNull();
    expect(box().value).toBe("Revised paragraph");
    act(() => sendButton().click());
    await flush();
    expect(rewinds()).toHaveLength(0);
    expect(messages()).toHaveLength(1);
  });

  // A model of the node and Claude seen live (#4605): the rewind answers at once, and Claude puts the old prompt back into
  // its input box a moment later. A message typed before that is glued after the old prompt; one typed after it is
  // refused (input_draft) unless it clears exactly that draft.
  const nodeModel = (box: { draft: string; refill?: string }) => {
    const delivered: string[] = [];
    let refilling = false;
    fetcher.mockImplementation(async (url, init) => {
      const u = String(url);
      if (u.endsWith("/rewind")) {
        refilling = true;
        setTimeout(() => {
          refilling = false;
          box.draft = box.refill ?? prompt;
        }, 300);
        return rewound();
      }
      if (u.endsWith("/messages")) {
        const body = JSON.parse(String((init as RequestInit).body));
        if (refilling) {
          delivered.push(prompt + body.text);
          return new Response(JSON.stringify({ ok: true }));
        }
        if (box.draft && body.clear_draft !== box.draft) {
          return new Response(JSON.stringify({ error: "There is a draft in the terminal.", code: "input_draft", delivered: "none", draft: box.draft }), { status: 409 });
        }
        box.draft = "";
        delivered.push(body.text);
        return new Response(JSON.stringify({ ok: true }));
      }
      return new Response("{}");
    });
    return delivered;
  };

  it("sends the edit once Claude's box has settled, clearing exactly the old prompt it put back: never old + new (#4605)", async () => {
    const delivered = nodeModel({ draft: "" });
    await mountError();
    press("ArrowUp");
    await flush();
    typeInto("Revised paragraph");
    act(() => sendButton().click());
    await flush();
    expect(rewinds()).toHaveLength(1);
    await settle();
    await flush();
    expect(delivered).toEqual(["> Quoted text\n\nRevised paragraph"]);
    expect(JSON.parse(messages()[0][1].body).clear_draft).toBe(prompt);
    expect(box().value).toBe("");
  });

  it("keeps the edit in the box, unsent, when Claude's box holds something else after the rewind (#4605)", async () => {
    const delivered = nodeModel({ draft: "", refill: "typed at the computer" });
    await mountError();
    press("ArrowUp");
    await flush();
    typeInto("Revised paragraph");
    act(() => sendButton().click());
    await flush();
    await settle();
    await flush();
    expect(messages()).toHaveLength(1);
    expect(delivered).toEqual([]);
    expect(box().value).toBe("Revised paragraph");
    expect(box().readOnly).toBe(false);
    expect(bar()).toBeNull();
    expect(getQuotes("hub/c1").list.map((q) => q.text)).toEqual(["Quoted text"]);
  });

  it.each(["codex", "working", "offline", "needs_you", "assistant"] as const)("offers no edit in an unsupported state: %s", async (kind) => {
    if (kind === "codex") captured.session.agent = "codex";
    else if (kind !== "assistant") captured.session.state = kind;
    await mountError(kind === "assistant");
    expect(editButton()).toBeUndefined();
    pressEsc(); pressEsc();
    press("ArrowUp");
    await flush();
    expect(bar()).toBeNull();
    expect(rewinds()).toHaveLength(0);
  });
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
});

// #5035: a message that was to carry out a switch to another launcher first, refused by the node (the launcher went, the
// agent did not come up there…): nothing went, so its words go back into the box with the hub's reason, as an edit's
// refused send does, not into a failed bubble; any other refusal still leaves its failed bubble. Only the user's message
// from this chat carries a switch out: it says so (apply_switch) while one waits, and only then.
describe("a message the launcher switch refused", () => {
  const box = () => host.querySelector<HTMLTextAreaElement>("textarea")!;
  const typeInto = (text: string) =>
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(box(), text);
      box().dispatchEvent(new Event("input", { bubbles: true }));
    });
  const sent = () => fetcher.mock.calls.filter(([url]) => String(url).endsWith("/messages")).map(([, init]) => JSON.parse(String((init as RequestInit).body)));
  const send = async (text: string, answer: Record<string, unknown>, waits = true, status = 409) => {
    fetcher.mockImplementation(async (url) => (String(url).endsWith("/messages") ? new Response(JSON.stringify(answer), { status }) : new Response("{}")));
    captured.session = { ...captured.session, pending_switch: waits ? { launcher: "work" } : undefined };
    act(() => root.render(<ChatView mid="hub" sid="c1" />));
    await flush();
    typeInto(text);
    act(() => host.querySelector<HTMLButtonElement>(`button[aria-label="${t("chat-composer.send")}"]`)!.click());
    await flush();
  };

  // the page's words outside the box (a textarea's own text is its value)
  const outsideBox = () => {
    const copy = host.cloneNode(true) as HTMLElement;
    copy.querySelectorAll("textarea").forEach((e) => e.remove());
    return copy.textContent ?? "";
  };

  it.each(["switch_launcher_missing", "switch_pick_model", "switch_failed", "switch_failed_stopped"])("keeps the words in the box: %s", async (code) => {
    await send("Carry on with the tests", { error: "The agent did not come up there", code, delivered: "none" });
    expect(box().value).toBe("Carry on with the tests");
    expect(outsideBox()).not.toContain("Carry on with the tests"); // no failed bubble
  });

  it("asks for the switch with the user's message while one waits, and only then", async () => {
    await send("Carry on", {}, true, 200);
    expect(sent().at(-1)).toMatchObject({ text: "Carry on", apply_switch: true });
    fetcher.mockClear();
    await send("And then this", {}, false, 200);
    expect(sent().at(-1)).toMatchObject({ text: "And then this" });
    expect(sent().at(-1)).not.toHaveProperty("apply_switch");
  });

  it("any other refusal is the failed bubble it always was", async () => {
    await send("Carry on with the tests", { error: "A dialog is open in the terminal", code: "dialog_open", delivered: "none" });
    expect(box().value).toBe("");
    expect(outsideBox()).toContain("Carry on with the tests");
  });
});

describe("a message waiting for its turn to start (#2450)", () => {
  it("can be interrupted: the composer shows Interrupt, Esc sends it, and the wait ends there", async () => {
    transcript.items = [{ id: "u1", seq: 1, role: "user", kind: "text", text: "Write a status line", ts: new Date().toISOString() }];
    act(() => root.render(<ChatView mid="hub" sid="c1" />));
    await flush();
    expect(stopButton()).not.toBeNull();
    await esc();
    await flush();
    expect(interrupts()).toHaveLength(1);
    expect(interrupts()[0][1]).toMatchObject({ method: "POST" });
    // the hub took it: the wait ends now instead of running out, so no stop button (nor its "Stopping" form) stays
    expect(stopControl()).toBeNull();
  });

  it("with nothing on its way, Esc sends nothing and there is no Interrupt", async () => {
    transcript.items = [
      { id: "u1", seq: 1, role: "user", kind: "text", text: "Write a status line", ts: new Date(Date.now() - 60_000).toISOString() },
      { id: "a1", seq: 2, role: "assistant", kind: "text", text: "Done.", ts: new Date().toISOString() },
    ];
    act(() => root.render(<ChatView mid="hub" sid="c1" />));
    await flush();
    expect(stopButton()).toBeNull();
    await esc();
    await flush();
    expect(interrupts()).toHaveLength(0);
  });
});
