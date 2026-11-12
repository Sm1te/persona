// @vitest-environment jsdom
// The shared chat sync scenarios (spec/fixtures/sync, #1106) against the web app's own state code: the real store,
// useTranscript, ChatView, the composer and the cards. Only the socket, fetch and the clock are the case's
// (sync/fake-hub.ts). Each step: deliver it, let the page do what is due, project the page (sync/project.ts) and check
// the step's expect and the invariants; at the end, settle and compare with the case's end.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { FakeSocket, SyncHub, type Message, type Request, type Seen, type Step, type SyncCase, type View } from "./fake-hub";
import { project } from "./project";

vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  // a desktop browser: a precise pointer (Enter sends), a wide window
  g.matchMedia = (q: string) => ({ matches: /pointer: *fine|hover: *hover/.test(q), addEventListener() {}, removeEventListener() {} });
  g.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} };
  g.IntersectionObserver = class { observe() {} unobserve() {} disconnect() {} takeRecords() { return []; } };
});

// What sits around a chat and is not about its sync: the avatar, the assistant's menus and task cards, the side panel.
vi.mock("../../../lib/panel", async (importOriginal) => ({ ...await importOriginal<typeof import("../../../lib/panel")>(), usePanel: () => null }));
vi.mock("../../assistant/Persona", () => ({ usePersona: () => ({ name: "Persona" }), PersonaAvatar: () => null }));
vi.mock("../../assistant/AssistantChat", () => ({
  AssistantFace: () => null,
  CompactionStrip: () => null,
  useAssistantMenu: () => ({ items: [], dialogs: null }),
}));
// The new-conversation page's setup checks: ready, as a hub without readiness reports it. A refused start still shows
// its card (FailureCard), with the words back in the box.
vi.mock("../../assistant/Readiness", () => ({
  useReadinessGate: () => ({ readiness: null, step: null, blocked: undefined, dialog: null, help() {}, confirm() {} }),
  ReadinessCard: () => null,
  ChatOnlyNote: () => null,
  FailureCard: () => <div data-start-failure />,
}));
vi.mock("../../assistant/Tasks", () => ({
  DayLine: () => null,
  FeedLineView: () => null,
  QuickResultCard: () => null,
  RouteChip: () => null,
  AssistantActionBar: () => null,
  TaskDock: () => null,
  TaskQuestions: () => null,
}));

const store = await import("../../../lib/store");
const { setToken } = await import("../../../lib/api");
const { t } = await import("../../../i18n");
const { ChatView } = await import("../ChatView");
const { PersonaAssistant } = await import("../../PersonaAssistant");
const { navigate, useRoute } = await import("../../../lib/router");
const { resetOutboxClaimsForTests } = await import("../../../lib/outbox");
const { resetStalePullsForTests } = await import("../useTranscript");
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const files = import.meta.glob<{ cases: SyncCase[] }>("../../../../../spec/fixtures/sync/*.json", { eager: true, import: "default" });

/** Cases this page does not pass yet: the bug, and what every failure of the case must say. A known case that fails
 *  for another reason, or passes, fails the suite: the fixed bug comes off this list, and a new one is not hidden. */
const KNOWN: Record<string, { issue: string; because: RegExp }> = {};

let host: HTMLDivElement;
let root: Root;
let hub: SyncHub;

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "setInterval", "clearInterval", "Date", "requestAnimationFrame", "cancelAnimationFrame"] });
  vi.setSystemTime(new Date("2026-01-02T03:05:00Z"));
  vi.stubGlobal("WebSocket", FakeSocket);
  setToken("tok");
  host = document.createElement("div");
  document.body.appendChild(host);
});

afterEach(async () => {
  await act(async () => root?.unmount());
  host.remove();
  store.disconnect();
  store.reset();
  resetOutboxClaimsForTests();
  resetStalePullsForTests();
  FakeSocket.hub = null;
  vi.unstubAllGlobals();
  vi.useRealTimers();
  localStorage.clear();
});

/** Lets the page do what is due: `ms` of its clock, then a few frames (the store batches its notices per frame). */
async function settle(ms = 0) {
  for (let i = 0; i < 4; i++) {
    await act(async () => {
      await vi.advanceTimersByTimeAsync(i === 0 ? ms : 17);
    });
  }
}

/** The composer: the chat's, or the new-conversation page's before the chat exists. */
const BOX = "[data-chat-dock] textarea, [data-new-conversation] textarea";

function typeAndSend(words: string) {
  const box = host.querySelector<HTMLTextAreaElement>(BOX);
  if (!box) throw new Error("no composer");
  const set = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!;
  set.call(box, words);
  box.dispatchEvent(new Event("input", { bubbles: true }));
  box.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true }));
}

/** Taps a failed bubble's Retry or Delete for the message with these words. */
function onFailed(words: string, button: "retry" | "delete") {
  const label = t(`chat-outboxbubbles.${button}`);
  const entry = [...host.querySelectorAll<HTMLElement>("[data-outbox]")].find(
    (el) => el.querySelector('[data-user-bubble][data-send-state="failed"] [data-bubble-text]')?.textContent?.trim() === words,
  );
  if (!entry) throw new Error(`no failed bubble for ${JSON.stringify(words)}`);
  const el = [...entry.querySelectorAll("button")].find((b) => b.textContent === label);
  if (!el) throw new Error(`no ${label} on the failed bubble for ${JSON.stringify(words)}`);
  el.click();
}

function answer(id: string, resolution: Record<string, unknown>) {
  const card = [...host.querySelectorAll<HTMLElement>("[data-card]")].find((el) => el.dataset.card === id);
  if (!card) throw new Error(`no card ${id} on screen`);
  const label = resolution.allow === false ? t("chat-interactions.deny") : t("chat-interactions.allow");
  const button = [...card.querySelectorAll<HTMLButtonElement>("button")].find((b) => (b.textContent ?? "").trim() === label);
  if (!button) throw new Error(`card ${id} has no "${label}"`);
  button.click();
}

/** The requests since the last check, matched in order against what the case names (others may come between). */
function matchRequests(want: Request[], seen: Seen[], ops: (key: string) => Set<string>, before: Set<string>): string | null {
  let at = 0;
  const one = (w: Request, s: Seen): boolean => {
    if (w.one_of) return w.one_of.some((o) => one(o, s));
    if (w.get === "/ops") return s.get === "/ops" && s.key === w.id;
    if (w.get) return s.get === w.get && (w.after_seq === undefined || s.after_seq === w.after_seq);
    if (w.ws) return s.ws === w.ws && (w.since_seq === undefined || s.since_seq === w.since_seq);
    if (w.post !== s.post) return false;
    if (w.new_op) return s.op !== undefined && !before.has(s.op);
    return s.key === w.id;
  };
  for (const w of want) {
    const k = seen.findIndex((s, i) => i >= at && one(w, s));
    if (k < 0) return `no ${JSON.stringify(w)} in ${JSON.stringify(seen.slice(at))}`;
    at = k + 1;
  }
  void ops;
  return null;
}

/** Compares the page with a view: every message in its place, failed ones too. */
function differences(got: ReturnType<typeof project> & { status?: string }, want: View): string[] {
  const out: string[] = [];
  if (want.messages) {
    const listed = (ms: Message[]) => ms.map((m) => `${m.from}:${m.state ?? ""}:${m.text}`);
    if (JSON.stringify(listed(got.messages)) !== JSON.stringify(listed(want.messages))) out.push(`messages ${JSON.stringify(listed(got.messages))}, want ${JSON.stringify(listed(want.messages))}`);
  }
  if (want.cards) {
    const open = (cs: Record<string, string>) => Object.keys(cs).filter((id) => cs[id] === "open").sort();
    if (JSON.stringify(open(got.cards)) !== JSON.stringify(open(want.cards))) out.push(`open cards ${JSON.stringify(open(got.cards))}, want ${JSON.stringify(open(want.cards))}`);
  }
  if (want.live !== undefined && (got.live ?? null) !== want.live) out.push(`live ${JSON.stringify(got.live)}, want ${JSON.stringify(want.live)}`);
  if (want.status !== undefined && got.status !== want.status) out.push(`status ${got.status}, want ${want.status}`);
  return out;
}

/** A `new` chat: the new-conversation page, then the conversation it starts, where the page goes once it knows it. */
function NewConversation() {
  const r = useRoute();
  if (r.name === "persona" && r.sid) return <PersonaAssistant sid={r.sid} onToggleSidebar={() => {}} />;
  return <PersonaAssistant fresh onToggleSidebar={() => {}} />;
}

async function run(c: SyncCase, device: string): Promise<string[]> {
  hub = new SyncHub(c, device);
  FakeSocket.hub = hub;
  vi.stubGlobal("fetch", hub.fetch);
  const { machine_id: mid, session_id: sid, assistant } = c.chat;
  store.connect();
  await settle();
  root = createRoot(host);
  if (c.chat.new) navigate({ name: "persona-new" }, true);
  await act(async () => root.render(c.chat.new ? <NewConversation /> : <ChatView mid={mid} sid={sid} assistant={assistant} />));
  await settle(100);

  const sent: { id: string; text: string; discarded?: boolean; start?: boolean }[] = [];
  const ended = new Set<string>();
  const answering = new Set<string>();
  let checked = 0;
  const failures: string[] = [];
  const look = () => ({ ...project(host, sent.filter((s) => !s.discarded).map((s) => s.text)), status: store.getSession(mid, sid)?.state });

  const invariants = (where: string) => {
    const v = look();
    for (const s of sent) {
      if (s.discarded) continue;
      const n = v.messages.filter((m) => m.from === "user" && m.text === s.text).length;
      if (n !== 1) failures.push(`${where}: sent message ${s.id} shows ${n} times`);
    }
    for (const [id, state] of Object.entries(v.cards)) {
      if (state === "open" && (ended.has(id) || answering.has(id))) failures.push(`${where}: card ${id} is answerable again`);
    }
  };

  for (const [i, st] of c.steps.entries()) {
    const where = `step ${i + 1} (${Object.keys(st).filter((k) => k !== "expect").join("+")})`;
    await step(st, device, { sent, ended, answering });
    await settle();
    invariants(where);
    const want = st.expect?.[device];
    if (want) {
      const got = look();
      for (const d of differences(got, want)) failures.push(`${where}: ${d}`);
      if (want.requests) {
        const before = new Set(hub.seen.slice(0, checked).flatMap((s) => (s.op ? [s.op] : [])));
        const miss = matchRequests(want.requests, hub.seen.slice(checked), (k) => hub.opsOf(k), before);
        if (miss) failures.push(`${where}: requests: ${miss}`);
        checked = hub.seen.length;
      }
    }
  }

  // the end: connected, every read answered, the page's timers run
  if (!hub.socket) {
    await settle(1000);
    const s = hub.waiting.at(-1);
    if (s) await act(async () => hub.open(s, true));
  }
  await settle(10_000);
  invariants("end");
  for (const d of differences(look(), c.end[device])) failures.push(`end: ${d}`);
  return failures;
}

async function step(st: Step, device: string, t: { sent: { id: string; text: string; discarded?: boolean; start?: boolean }[]; ended: Set<string>; answering: Set<string> }) {
  if (st.hub) hub.apply(st.hub);
  if (st.socket && st.socket.device === device) {
    if (st.socket.drop) await act(async () => hub.drop());
    if (st.socket.open) {
      // the page reconnects on its own clock: wait for its next socket
      for (let i = 0; i < 120 && !hub.waiting.length; i++) await settle(500);
      const s = hub.waiting.at(-1);
      if (!s) throw new Error("the page never reconnected");
      await act(async () => hub.open(s, st.socket!.open!.resumed));
      if (!st.socket.open.resumed) for (const id of t.answering) if (!hub.truth.pending.some((p) => p.id === id)) t.ended.add(id);
    }
  }
  for (const f of st.frames?.[device] ?? []) {
    if (f.type === "interaction_resolved") t.ended.add(String(f.id));
    await act(async () => hub.push(f));
  }
  const u = st.user;
  if (u && u.device === device) {
    if (u.send) {
      hub.expectSend(u.send.id, u.send.text);
      t.sent.push({ id: u.send.id, text: u.send.text });
      await act(async () => typeAndSend(u.send!.text));
    } else if (u.start) {
      // the first message on the new-conversation page; it counts as a sent message (README "Steps")
      hub.expectStart(u.start.id);
      t.sent.push({ id: u.start.id, text: u.start.text, start: true });
      await act(async () => typeAndSend(u.start!.text));
    } else if (u.retry && t.sent.find((s) => s.id === u.retry)?.start && !hub.started) {
      // a refused start is sent again from the new-conversation page, where its words are back in the box
      await act(async () => typeAndSend(t.sent.find((s) => s.id === u.retry)!.text));
    } else if (u.answer) {
      t.answering.add(u.answer.id);
      await act(async () => answer(u.answer!.id, u.answer!.resolution));
    } else if (u.retry) {
      await act(async () => onFailed(t.sent.find((s) => s.id === u.retry)!.text, "retry"));
    } else if (u.discard) {
      const s = t.sent.find((x) => x.id === u.discard)!;
      s.discarded = true;
      await act(async () => onFailed(s.text, "delete"));
    }
  }
  const r = st.reply;
  if (r && r.device === device) {
    await act(async () => hub.reply(r));
    if (t.answering.has(r.write) && !r.cut) {
      t.answering.delete(r.write);
      if ((r.status ?? 0) < 300 || r.status === 410) t.ended.add(r.write);
    }
  }
  if (st.wait) await settle(st.wait);
  if (st.hold && st.hold.device === device) hub.hold(st.hold.get);
  if (st.release && st.release.device === device) await act(async () => hub.release(st.release!.get));
}

for (const [path, file] of Object.entries(files)) {
  describe(`sync ${path.split("/").pop()}`, () => {
    for (const c of file.cases) {
      for (const device of c.devices) {
        const name = c.devices.length > 1 ? `${c.name} as ${device}` : c.name;
        const known = KNOWN[name];
        it(known ? `${name} (known: ${known.issue})` : name, async () => {
          const failures = await run(c, device);
          if (!known) return expect(failures, `${name}; requests this hub does not answer: ${hub.unknown.join(", ")}`).toEqual([]);
          expect(failures.length, `${name} passes now: take it off KNOWN (${known.issue})`).toBeGreaterThan(0);
          expect(failures.filter((f) => !known.because.test(f)), `${name} fails for another reason than ${known.issue}`).toEqual([]);
        }, 60_000);
      }
    }
  });
}
