// @vitest-environment jsdom
// A message that did not go stays in the chat as a failed bubble with why, Retry and Delete, the box empty (#1122). A
// refused send in a Persona conversation also gets a card above the composer (docs/assistant/onboarding.md 4.6); the next
// send that goes through takes it away, so it does not linger over a later setup step.
import { act, useEffect, useReducer } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Outgoing } from "./Composer";
import type { Session } from "../../lib/types";

vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
  g.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} };
  g.IntersectionObserver = class { observe() {} unobserve() {} disconnect() {} takeRecords() { return []; } };
});

const captured = vi.hoisted(() => ({
  onSend: undefined as ((out: Outgoing) => Promise<boolean>) | undefined,
  session: { machine_id: "hub", session_id: "c1", title: "Conversation", cwd: "/code", status: "idle" } as Session | undefined,
  composer: undefined as { placeholder?: string; disabled?: boolean; blocked?: { label: string } } | undefined,
  // set: the hub answers the exact-session lookup (useRuntimeSession) and the page is connected
  features: [] as string[],
}));

vi.mock("./Composer", async (importOriginal) => ({
  ...await importOriginal<typeof import("./Composer")>(),
  Composer: ({ onSend, ...props }: { onSend: (out: Outgoing) => Promise<boolean>; placeholder?: string; disabled?: boolean; blocked?: { label: string } }) => {
    captured.onSend = onSend;
    captured.composer = props;
    return null;
  },
}));
// one transcript for every render: a new one each time would re-run the sending bubble's echo check forever
const transcript = vi.hoisted(() => ({ items: [] as unknown[], loading: false, firstRead: true, error: "", hasMore: false, streaming: "" }));
vi.mock("./useTranscript", () => ({ useTranscript: () => transcript }));
vi.mock("../../lib/store", async (importOriginal) => {
  const store = await importOriginal<typeof import("../../lib/store")>();
  return {
    ...store,
    getSession: () => captured.session,
    getState: () => (captured.features.length ? { ...store.getState(), features: captured.features } : store.getState()),
    // drawn again on every change, as the real one is (a failed message is kept in the store's outbox)
    useStore: <T,>(select: (s: ReturnType<typeof store.getState>) => T) => {
      const [, changed] = useReducer((n: number) => n + 1, 0);
      useEffect(() => store.subscribe(changed), []);
      return select({ ...store.getState(), sessions: { hub: captured.session ? [captured.session] : [] }, ...(captured.features.length ? { features: captured.features, conn: "online" as const } : {}) });
    },
  };
});
vi.mock("../../lib/panel", async (importOriginal) => ({
  ...await importOriginal<typeof import("../../lib/panel")>(),
  usePanel: () => null,
}));
vi.mock("../../lib/router", async (importOriginal) => ({
  ...await importOriginal<typeof import("../../lib/router")>(),
  useRoute: () => ({ name: "session", mid: "hub", id: "c1" }),
}));
vi.mock("../assistant/Persona", () => ({ usePersona: () => ({ name: "Persona" }) }));
vi.mock("../assistant/AssistantChat", () => ({
  AssistantFace: () => null,
  AssistantLauncherChip: () => null,
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

// the wait for an ultracode change to go through (#3428): the test says when
const ultra = vi.hoisted(() => ({ release: undefined as ((through: boolean) => void) | undefined }));
vi.mock("../../lib/ultracode", async (importOriginal) => ({
  ...await importOriginal<typeof import("../../lib/ultracode")>(),
  ultracodeQueueDone: () => new Promise<boolean>((resolve) => (ultra.release = resolve)),
}));

// what Delete's toast offers (#4626): no toast stack is drawn here
vi.mock("../../ui/atoms", async (importOriginal) => ({
  ...await importOriginal<typeof import("../../ui/atoms")>(),
  toast: vi.fn(),
}));

const { ChatView } = await import("./ChatView");
const { toast } = await import("../../ui/atoms");
const { t } = await import("../../i18n");
const { getState, setOutbox } = await import("../../lib/store");
const { setRoute } = await import("../../lib/feed");
const { keepOutgoing, outboxKey, resetOutboxClaimsForTests, resetRestoredForTests, SENT_GRACE_MS } = await import("../../lib/outbox");
const { SESSIONS_LOOKUP_FEATURE } = await import("../../lib/runtime-list");
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const json = (v: unknown, status = 200) => new Response(JSON.stringify(v), { status, headers: { "Content-Type": "application/json" } });
let reply: () => Response;
let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 6; i++) await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
};

beforeEach(() => {
  localStorage.clear();
  resetRestoredForTests();
  for (const key of Object.keys(getState().outbox)) setOutbox(key, []);
  vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL) => Promise.resolve(String(input).endsWith("/messages") ? reply() : json({}))));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
});

describe("a refused send's card", () => {
  it("goes away once a later send goes through", async () => {
    act(() => root.render(<ChatView mid="hub" sid="c1" assistant />));
    await flush();
    reply = () => json({ error: "模型没接好", code: "model_missing", action: "connect_model" }, 409);
    await act(async () => void (await captured.onSend!({ text: "你好", images: [] })));
    await flush();
    expect(host.querySelector('[data-testid="readiness-failure"]')).not.toBeNull();
    reply = () => json({});
    await act(async () => void (await captured.onSend!({ text: "再来", images: [] })));
    await flush();
    expect(host.querySelector('[data-testid="readiness-failure"]')).toBeNull();
  });
});

describe("a message that did not go, in an agent's chat (#1122)", () => {
  const failed = () => host.querySelector('[data-user-bubble][data-send-state="failed"]');
  const button = (label: string) => Array.from(host.querySelectorAll("[data-outbox] button")).find((b) => b.textContent === label) as HTMLButtonElement | undefined;
  let bodies: { client_msg_id: string; text: string }[];
  beforeEach(() => {
    bodies = [];
    vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      if (!String(input).endsWith("/messages")) return Promise.resolve(json({}));
      bodies.push(JSON.parse(String(init?.body)));
      return Promise.resolve(reply());
    }));
  });

  it("stays in the chat as a failed bubble with why, Retry and Delete, and the box stays empty", async () => {
    act(() => root.render(<ChatView mid="hub" sid="c1" />));
    await flush();
    reply = () => json({ error: "会话已结束" }, 400);
    let kept: boolean | undefined;
    await act(async () => void (kept = await captured.onSend!({ text: "你好", images: [] })));
    await flush();
    expect(kept).toBe(true); // the composer does not put the words back
    expect(failed()?.textContent).toContain("你好");
    expect(host.querySelector("[data-outbox] [role=alert]")?.textContent).toContain("没发出去：会话已结束");
    expect(button("重试")).toBeDefined();
    expect(button("删除")).toBeDefined();
    expect(localStorage.getItem("draft:hub/c1")).toBeNull();
    expect(host.querySelector('[data-testid="readiness-failure"]')).toBeNull(); // the bubble says why: no card over the box
  });

  it("Retry sends it again, under a new id (the hub keeps its answer to the first), and the bubble waits for the transcript", async () => {
    act(() => root.render(<ChatView mid="hub" sid="c1" />));
    await flush();
    reply = () => json({ error: "会话已结束" }, 400);
    await act(async () => void (await captured.onSend!({ text: "你好", images: [] })));
    await flush();
    reply = () => json({});
    await act(async () => button("重试")!.click());
    await flush();
    expect(bodies.map((b) => b.text)).toEqual(["你好", "你好"]);
    expect(bodies[1].client_msg_id).not.toBe(bodies[0].client_msg_id);
    expect(failed()).toBeNull();
    expect(host.querySelector("[data-outbox] [data-user-bubble]")?.getAttribute("data-send-state")).toBe("sent");
  });

  it("Delete takes it away", async () => {
    act(() => root.render(<ChatView mid="hub" sid="c1" />));
    await flush();
    reply = () => json({ error: "会话已结束" }, 400);
    await act(async () => void (await captured.onSend!({ text: "你好", images: [] })));
    await flush();
    act(() => button("删除")!.click());
    await flush();
    expect(host.querySelector("[data-outbox]")).toBeNull();
    expect(bodies).toHaveLength(1);
  });

  it("its message menu ends in Delete too, which sends nothing and offers Undo (#4626)", async () => {
    act(() => root.render(<ChatView mid="hub" sid="c1" />));
    await flush();
    reply = () => json({ error: "会话已结束" }, 400);
    await act(async () => void (await captured.onSend!({ text: "你好", images: [] })));
    await flush();
    const words = [...host.querySelectorAll<HTMLElement>("[data-outbox] [data-user-bubble] *")].filter((el) => el.textContent === "你好").at(-1)!;
    act(() => void words.dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 20, clientY: 20 })));
    const last = [...document.querySelectorAll<HTMLElement>('[role="menuitem"]')].at(-1)!;
    expect(last.textContent?.trim()).toBe("删除");
    act(() => last.click());
    await flush();
    expect(host.querySelector("[data-outbox]")).toBeNull();
    expect(bodies).toHaveLength(1);
    expect(toast).toHaveBeenLastCalledWith("消息已删除", "ok", expect.objectContaining({ label: "撤销" }));
    act(() => vi.mocked(toast).mock.lastCall![2]!.run());
    await flush();
    expect(failed()?.textContent).toContain("你好");
    expect(bodies).toHaveLength(1);
  });

  it("a send that never went (nothing shows it) gives the words back", async () => {
    captured.session = { ...captured.session, status: "offline", source_kind: "codex_history" } as Session;
    try {
      act(() => root.render(<ChatView mid="hub" sid="c1" />));
      await flush();
      let went: boolean | undefined;
      await act(async () => void (went = await captured.onSend!({ text: "你好", images: [] })));
      expect(went).toBe(false);
      expect(host.querySelector("[data-outbox]")).toBeNull();
    } finally {
      captured.session = { machine_id: "hub", session_id: "c1", title: "Conversation", cwd: "/code", status: "idle" } as Session;
    }
  });
});

describe("a message the node held back for an ultracode change (#3428)", () => {
  const HELD = { code: "ultracode_pending", error: "ultracode 等这一轮结束后生效，请稍后再发送消息" };
  let bodies: { client_msg_id: string; text: string }[];
  beforeEach(() => {
    bodies = [];
    ultra.release = undefined;
    vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      if (!String(input).endsWith("/messages")) return Promise.resolve(json({}));
      bodies.push(JSON.parse(String(init?.body)));
      return Promise.resolve(reply());
    }));
  });

  it("waits in its bubble saying so, leaves the box free, and goes again with a new id once the change is through", async () => {
    act(() => root.render(<ChatView mid="hub" sid="c1" />));
    await flush();
    reply = () => json(HELD, 409);
    let went: boolean | undefined;
    await act(async () => void (went = await captured.onSend!({ text: "再看看测试", images: [] })));
    await flush();
    expect(went).toBe(true); // the box is free: its stop button too, while the turn runs
    expect(host.textContent).toContain("等 ultracode 的切换在这一轮结束后生效，再发这条");
    expect(host.textContent).toContain("再看看测试");
    expect(host.querySelector('[data-testid="readiness-failure"]')).toBeNull();
    reply = () => json({});
    await act(async () => ultra.release!(true));
    await flush();
    expect(bodies.map((b) => b.text)).toEqual(["再看看测试", "再看看测试"]);
    expect(bodies[1].client_msg_id).not.toBe(bodies[0].client_msg_id);
    expect(host.textContent).not.toContain("等 ultracode 的切换");
  });

  it("not through in time: it stays as a failed bubble that says why, the box empty", async () => {
    act(() => root.render(<ChatView mid="hub" sid="c1" />));
    await flush();
    reply = () => json(HELD, 409);
    await act(async () => void (await captured.onSend!({ text: "再看看测试", images: [] })));
    await flush();
    await act(async () => ultra.release!(false));
    await flush();
    expect(bodies).toHaveLength(1);
    expect(localStorage.getItem("draft:hub/c1")).toBeNull();
    expect(host.querySelector('[data-user-bubble][data-send-state="failed"]')?.textContent).toContain("再看看测试");
    expect(host.querySelector("[data-outbox] [role=alert]")?.textContent).toContain("ultracode 等这一轮结束后生效");
  });
});

describe("accepted messages waiting for their transcript copy (#2792)", () => {
  let bodies: { client_msg_id: string; text: string }[];
  beforeEach(() => {
    bodies = [];
    resetOutboxClaimsForTests();
    vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input).endsWith("/messages")) bodies.push(JSON.parse(String(init?.body)));
      return Promise.resolve(json({}));
    }));
  });
  afterEach(() => { transcript.items = []; });

  it.each(["reload", "navigation"])("keeps one pending copy after %s without sending again, then replaces it with the echo", async (reopen) => {
    act(() => root.render(<ChatView mid="hub" sid="c1" />));
    await flush();
    await act(async () => void (await captured.onSend!({ text: "Is the museum open on Sunday?", images: [] })));
    await flush();
    const id = bodies[0].client_msg_id;
    const key = `outbox-msg:hub/c1/${id}`;
    expect(JSON.parse(localStorage.getItem(key)!)).toMatchObject({ id, state: "sent" });
    expect(host.querySelectorAll("[data-user-bubble]")).toHaveLength(1);

    act(() => root.render(null));
    if (reopen === "reload") {
      for (const chat of Object.keys(getState().outbox)) setOutbox(chat, []);
      resetOutboxClaimsForTests();
      resetRestoredForTests();
    }
    act(() => root.render(<ChatView mid="hub" sid="c1" />));
    await flush();
    expect(host.querySelectorAll("[data-user-bubble]")).toHaveLength(1);
    expect(host.querySelector(`[data-outbox="${id}"] [data-user-bubble]`)?.getAttribute("data-send-state")).toBe("sent");
    expect(host.querySelector("[data-outbox] [role=alert]")).toBeNull();
    expect([...host.querySelectorAll("[data-outbox] button")].some((b) => b.textContent === t("chat-outboxbubbles.retry"))).toBe(false);
    expect(bodies).toHaveLength(1);

    transcript.items = [{ id: "accepted-copy", seq: 1, role: "user", kind: "text", text: bodies[0].text, client_msg_id: id }];
    act(() => root.render(<ChatView mid="hub" sid="c1" />));
    await flush();
    expect(host.querySelectorAll("[data-user-bubble]")).toHaveLength(1);
    expect(host.querySelector("[data-user-bubble]")?.hasAttribute("data-send-state")).toBe(false);
    expect(localStorage.getItem(key)).toBeNull();
    expect(bodies).toHaveLength(1);
  });

  it("does not save an accepted bubble again when its echo arrived before HTTP 200", async () => {
    let accept!: () => void;
    vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      if (!String(input).endsWith("/messages")) return Promise.resolve(json({}));
      bodies.push(JSON.parse(String(init?.body)));
      return new Promise<Response>((resolve) => (accept = () => resolve(json({}))));
    }));
    act(() => root.render(<ChatView mid="hub" sid="c1" />));
    await flush();
    let sending!: Promise<boolean>;
    act(() => { sending = captured.onSend!({ text: "Please check the opening hours", images: [] }); });
    await flush();
    const id = bodies[0].client_msg_id;
    const key = `outbox-msg:hub/c1/${id}`;
    expect(localStorage.getItem(key)).not.toBeNull();
    transcript.items = [{ id: "early-copy", seq: 1, role: "user", kind: "text", text: bodies[0].text, client_msg_id: id }];
    act(() => root.render(<ChatView mid="hub" sid="c1" />));
    await flush();
    expect(localStorage.getItem(key)).toBeNull();
    expect(host.querySelectorAll("[data-user-bubble]")).toHaveLength(1);

    accept();
    await act(async () => void (await sending));
    await flush();
    expect(localStorage.getItem(key)).toBeNull();
    expect(host.querySelectorAll("[data-user-bubble]")).toHaveLength(1);
    expect(bodies).toHaveLength(1);
  });

  it("updates a bubble restored on a fresh page when its original POST is accepted", async () => {
    let accept!: () => void;
    vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      if (!String(input).endsWith("/messages")) return Promise.resolve(json({}));
      bodies.push(JSON.parse(String(init?.body)));
      return new Promise<Response>((resolve) => (accept = () => resolve(json({}))));
    }));
    act(() => root.render(<ChatView mid="hub" sid="c1" />));
    await flush();
    let sending!: Promise<boolean>;
    act(() => { sending = captured.onSend!({ text: "Please check the visiting hours", images: [] }); });
    await flush();
    const id = bodies[0].client_msg_id;
    const key = `outbox-msg:hub/c1/${id}`;

    act(() => root.render(null));
    resetRestoredForTests(); // a newly opened page has not checked this conversation yet
    act(() => root.render(<ChatView mid="hub" sid="c1" />));
    await flush();
    expect(host.querySelector(`[data-outbox="${id}"] [data-user-bubble]`)?.getAttribute("data-send-state")).toBe("failed");
    accept();
    await act(async () => void (await sending));
    await flush();
    expect(host.querySelectorAll("[data-user-bubble]")).toHaveLength(1);
    expect(host.querySelector(`[data-outbox="${id}"] [data-user-bubble]`)?.getAttribute("data-send-state")).toBe("sent");
    expect(host.querySelector("[data-outbox] [role=alert]")).toBeNull();
    expect(JSON.parse(localStorage.getItem(key)!)).toMatchObject({ id, state: "sent" });
    expect(bodies).toHaveLength(1);

    transcript.items = [{ id: "navigated-copy", seq: 1, role: "user", kind: "text", text: bodies[0].text, client_msg_id: id }];
    act(() => root.render(<ChatView mid="hub" sid="c1" />));
    await flush();
    expect(host.querySelectorAll("[data-user-bubble]")).toHaveLength(1);
    expect(host.querySelector("[data-user-bubble]")?.hasAttribute("data-send-state")).toBe(false);
    expect(localStorage.getItem(key)).toBeNull();
    expect(bodies).toHaveLength(1);
  });

  it("does not turn another tab's active send into a failure on transcript updates, but restores it once accepted", async () => {
    act(() => root.render(<ChatView mid="hub" sid="c1" />));
    await flush();
    const key = outboxKey("hub", "c1");
    const sending = { id: "other-tab", text: "Sent from another tab", images: [], at: Date.now() };
    keepOutgoing(key, { ...sending, state: "sending" });

    transcript.items = [{ id: "unrelated-update", seq: 1, role: "notice", kind: "notice", text: "The session changed" }];
    act(() => root.render(<ChatView mid="hub" sid="c1" />));
    await flush();
    expect(host.querySelector('[data-outbox="other-tab"]')).toBeNull();
    expect(host.querySelector('[data-user-bubble][data-send-state="failed"]')).toBeNull();
    expect(JSON.parse(localStorage.getItem(`outbox-msg:${key}/other-tab`)!)).toMatchObject({ state: "sending" });

    keepOutgoing(key, { ...sending, state: "sent", acceptedAt: Date.now() });
    transcript.items = [...transcript.items, { id: "another-update", seq: 2, role: "notice", kind: "notice", text: "The session changed again" }];
    act(() => root.render(<ChatView mid="hub" sid="c1" />));
    await flush();
    expect(host.querySelector('[data-outbox="other-tab"] [data-user-bubble]')?.getAttribute("data-send-state")).toBe("sent");
    expect(host.querySelector("[data-outbox] [role=alert]")).toBeNull();
    expect(bodies).toEqual([]);
  });

  it("expires a restored accepted bubble even when no further transcript arrives", async () => {
    vi.useFakeTimers();
    const key = "outbox-msg:hub/c1/accepted";
    keepOutgoing(outboxKey("hub", "c1"), { id: "accepted", text: "Waiting for a copy", images: [], at: Date.now(), state: "sent" });
    try {
      act(() => root.render(<ChatView mid="hub" sid="c1" />));
      await act(async () => { await vi.advanceTimersByTimeAsync(100); });
      expect(host.querySelector('[data-outbox="accepted"] [data-user-bubble]')?.getAttribute("data-send-state")).toBe("sent");
      expect(localStorage.getItem(key)).not.toBeNull();

      await act(async () => { await vi.advanceTimersByTimeAsync(SENT_GRACE_MS); });
      await act(async () => { await vi.advanceTimersByTimeAsync(100); });
      expect(host.querySelector('[data-outbox="accepted"]')).toBeNull();
      expect(localStorage.getItem(key)).toBeNull();
      expect(bodies).toEqual([]);
    } finally {
      act(() => root.render(null));
      vi.useRealTimers();
    }
  });
});

describe("sending in order, and with no row yet", () => {
  const known = captured.session;
  afterEach(() => void (captured.session = known));
  let bodies: { text: string }[];
  let release: (() => void)[];
  beforeEach(() => {
    bodies = [];
    release = [];
    vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      if (!String(input).endsWith("/messages")) return Promise.resolve(json({}));
      bodies.push(JSON.parse(String(init?.body)));
      return new Promise<Response>((resolve) => release.push(() => resolve(json({}))));
    }));
  });

  it("posts one message at a time, in the order they were sent", async () => {
    act(() => root.render(<ChatView mid="hub" sid="c1" />));
    await flush();
    let first!: Promise<boolean>, second!: Promise<boolean>;
    act(() => {
      first = captured.onSend!({ text: "one", images: [] });
      second = captured.onSend!({ text: "two", images: [] });
    });
    await flush();
    expect(bodies.map((b) => b.text)).toEqual(["one"]);
    release[0]();
    await flush();
    expect(bodies.map((b) => b.text)).toEqual(["one", "two"]);
    release[1]();
    await act(async () => void (await Promise.all([first, second])));
  });

  it("keeps a message waiting behind a slow one on screen and in this browser, past the grace", async () => {
    act(() => root.render(<ChatView mid="hub" sid="c1" />));
    await flush();
    let first!: Promise<boolean>, second!: Promise<boolean>;
    const sentAt = Date.now();
    act(() => {
      first = captured.onSend!({ text: "one", images: [] });
      second = captured.onSend!({ text: "two", images: [] });
    });
    await flush();
    const later = Date.now() + 3 * SENT_GRACE_MS;
    const clock = vi.spyOn(Date, "now").mockImplementation(() => later);
    transcript.items = [{ id: "n1", seq: 1, role: "notice", kind: "notice", text: "something else" }];
    try {
      act(() => root.render(<ChatView mid="hub" sid="c1" />));
      await flush();
      expect(host.textContent).toContain("two");
      expect(Object.keys(localStorage).filter((k) => k.startsWith("outbox-msg:"))).toHaveLength(2);
      release[0]();
      await flush();
      release[1]();
      await act(async () => void (await Promise.all([first, second])));
      expect(Object.keys(localStorage).filter((k) => k.startsWith("outbox-msg:"))).toHaveLength(2);
      // Untagged echoes use the original send time, even when acceptance took longer than the clock slack.
      transcript.items = bodies.map((body, i) => ({ id: `slow-copy-${i}`, seq: i + 1, role: "user", kind: "text", text: body.text, ts: new Date(sentAt).toISOString() }));
      act(() => root.render(<ChatView mid="hub" sid="c1" />));
      await flush();
      expect(host.querySelectorAll("[data-user-bubble]")).toHaveLength(2);
      expect(host.querySelector("[data-user-bubble][data-send-state]")).toBeNull();
      expect(Object.keys(localStorage).filter((k) => k.startsWith("outbox-msg:"))).toHaveLength(0);
    } finally {
      clock.mockRestore();
      transcript.items = [];
    }
  });

  it("sends a message after a routed one only once that one has used the route", async () => {
    const posts: string[] = [];
    const open: (() => void)[] = [];
    vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL) => {
      const path = String(input);
      if (!path.endsWith("/route") && !path.endsWith("/messages")) return Promise.resolve(json({}));
      posts.push(path.slice(path.lastIndexOf("/") + 1));
      return new Promise<Response>((resolve) => open.push(() => resolve(json({}))));
    }));
    setRoute("c1", "todo1");
    act(() => root.render(<ChatView mid="hub" sid="c1" assistant />));
    await flush();
    let first!: Promise<boolean>, second!: Promise<boolean>;
    act(() => {
      first = captured.onSend!({ text: "for the task", images: [] });
      second = captured.onSend!({ text: "for persona", images: [] });
    });
    await flush();
    expect(posts).toEqual(["route"]);
    open[0]();
    await flush();
    expect(posts).toEqual(["route", "messages"]);
    open[1]();
    await act(async () => void (await Promise.all([first, second])));
  });

  it("keeps an agent's box open but does not send before its row is here", async () => {
    captured.session = undefined;
    act(() => root.render(<ChatView mid="hub" sid="c1" />));
    await flush();
    expect(captured.composer).toMatchObject({ disabled: false, blocked: { label: expect.any(String) } });
    expect(captured.composer?.placeholder).toBe(t("chat-chatview.connecting_placeholder"));
    let sent: boolean | undefined;
    await act(async () => void (sent = await captured.onSend!({ text: "too early", images: [] })));
    expect(sent).toBe(false);
    expect(bodies).toEqual([]);
  });

  it("lets only the main conversation send before its row is here", async () => {
    captured.session = undefined;
    act(() => root.render(<ChatView mid="hub" sid="c1" assistant />));
    await flush();
    expect(captured.composer).toMatchObject({ disabled: false, blocked: { label: expect.any(String) } });
    let refused: boolean | undefined;
    await act(async () => void (refused = await captured.onSend!({ text: "too early", images: [] })));
    expect(refused).toBe(false);
    expect(bodies).toEqual([]);

    act(() => root.render(<ChatView mid="hub" sid="c1" assistant mainCache={{ scope: "test" }} />));
    await flush();
    expect(captured.composer?.blocked).toBeUndefined();
    let sending!: Promise<boolean>;
    act(() => {
      sending = captured.onSend!({ text: "already here", images: [] });
    });
    await flush();
    expect(bodies.map((b) => b.text)).toEqual(["already here"]);
    release[0]();
    await act(async () => void (await sending));
  });

  it("says a chat the hub reports missing is gone: typing works, sending stays refused, Retry is hidden", async () => {
    captured.session = undefined;
    captured.features = [SESSIONS_LOOKUP_FEATURE];
    vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input).endsWith("/messages")) bodies.push(JSON.parse(String(init?.body)));
      return Promise.resolve(json(String(input).includes("/sessions?") ? { sessions: [], remove: [], has_more: false, revision: "r", total: 0 } : {}));
    }));
    const key = outboxKey("hub", "c1");
    setOutbox(key, [{ id: "live", text: "did not go", images: [], at: Date.now(), state: "failed", error: "no" }]);
    try {
      act(() => root.render(<ChatView mid="hub" sid="c1" />));
      await flush();
      expect(captured.composer).toMatchObject({ disabled: false, blocked: { label: t("chat-chatview.session_gone") }, placeholder: t("chat-chatview.session_gone_placeholder") });
      expect(host.querySelector('[data-outbox="live"]')?.textContent).not.toContain(t("chat-outboxbubbles.retry"));
      expect(host.querySelector('[data-outbox="live"]')?.textContent).toContain(t("chat-outboxbubbles.delete"));
      let sent: boolean | undefined;
      await act(async () => void (sent = await captured.onSend!({ text: "too late", images: [] })));
      expect(sent).toBe(false);
      expect(bodies).toEqual([]);
    } finally {
      captured.features = [];
    }
  });

  it("the main conversation with no row is not connecting, and its failed bubble keeps Retry", async () => {
    captured.session = undefined;
    setOutbox(outboxKey("hub", "c1"), [{ id: "live", text: "did not go", images: [], at: Date.now(), state: "failed", error: "no" }]);
    act(() => root.render(<ChatView mid="hub" sid="c1" assistant mainCache={{ scope: "test" }} />));
    await flush();
    expect(captured.composer?.blocked).toBeUndefined();
    expect(captured.composer?.placeholder).toBe(t("chat-chatview.ask_placeholder", { name: "Persona" }));
    expect(host.querySelector('[data-outbox="live"]')?.textContent).toContain(t("chat-outboxbubbles.retry"));
  });

  it("brings a reload's unconfirmed message back only once the chat is known and the hub's first page is in", async () => {
    keepOutgoing(outboxKey("hub", "c1"), { id: "kept", text: "unconfirmed", images: [], at: Date.now(), state: "sending" });
    captured.session = undefined;
    transcript.firstRead = false;
    try {
      act(() => root.render(<ChatView mid="hub" sid="c1" />));
      await flush();
      expect(host.querySelector('[data-outbox="kept"]')).toBeNull(); // no row, no first page

      captured.session = known;
      act(() => root.render(<ChatView mid="hub" sid="c1" />));
      await flush();
      expect(host.querySelector('[data-outbox="kept"]')).toBeNull(); // a known chat, but only a cached page so far

      transcript.firstRead = true;
      act(() => root.render(<ChatView mid="hub" sid="c1" />));
      await flush();
      expect(host.querySelector('[data-outbox="kept"] [data-user-bubble]')?.getAttribute("data-send-state")).toBe("failed");
      expect(host.querySelector('[data-outbox="kept"]')?.textContent).toContain(t("chat-outboxbubbles.retry"));
      expect(bodies).toEqual([]);
    } finally {
      transcript.firstRead = true;
    }
  });
});
