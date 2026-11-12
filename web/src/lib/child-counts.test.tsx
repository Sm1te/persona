// One fixture, every place that counts (#5120, DESIGN_SYSTEM 4.8 "One count, per session"): a parent idle with a working
// child and a waiting one, an orphan child whose parent is not listed, and a plain session. The sidebar's sections and
// quick filters, the tab's badge, Home, the dock, the switcher, the pending list, the Inbox and the notifications all
// see the parent once, in its folded state, and never a folded child.
import { describe, expect, it, vi } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import type { Interaction, Machine, Session } from "./types";

vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
  const kept = new Map<string, string>();
  g.localStorage = { getItem: (k: string) => kept.get(k) ?? null, setItem: (k: string, v: string) => void kept.set(k, v), removeItem: (k: string) => void kept.delete(k) };
});

const fake = vi.hoisted(() => ({ state: {} as Record<string, unknown> }));
vi.mock("./store", async (importOriginal) => {
  const real = await importOriginal<typeof import("./store")>();
  return { ...real, useStore: <T,>(select: (s: ReturnType<typeof real.getState>) => T) => select({ ...real.getState(), ...fake.state } as ReturnType<typeof real.getState>) };
});

const { foldRows, homeKey } = await import("./folded-rows");
const { stateCounts, useAgentSessions, useCounts, useFullAgentSessions } = await import("./sessions");
const { buildAgentGroups, splitAgentSessions } = await import("./agent-groups");
const { filterCounts } = await import("./agent-filter");
const { tabTitle, detectNewCards, detectTransitions } = await import("./attention");
const { homeOverview } = await import("./herdr-tree");
const { assistantEntries } = await import("./assistant-entries");
const { dockAgents } = await import("./todos");
const { waitingAgents: managerWaiting } = await import("./manager-attention");
const { waitingAgents: inboxWaiting } = await import("./inbox");
const { requestRoute } = await import("./card-focus");
const { backgroundWork } = await import("./background-work");
const { runtimeNeedsYou } = await import("./runtime-list");
const { sidebarPending } = await import("../views/sidebar-pending");
const { answeredSessions } = await import("../views/assistant/answered-lookup");

const at = "2026-11-12T08:00:00Z";
const row = (sid: string, extra: Partial<Session>): Session => ({ machine_id: "m", session_id: sid, title: sid, cwd: "/w", status: "idle", updated_at: at, ...extra });
const parent = row("lead", { state: "idle", children: { total: 2, working: 1, needs_you: 1 } });
const working = row("child-a", { state: "working", is_subagent: true, parent_session_id: "lead", folded_under: "lead" });
// a waiting runtime child: only the manager names its parent
const waiting = row("child-b", { state: "needs_you", status: "blocked", folded_under: "lead" });
const orphan = row("orphan", { state: "working", is_subagent: true, parent_session_id: "archived" });
const plain = row("plain", { state: "unread" });
const raw = [parent, working, waiting, orphan, plain];
const card = { id: "ix-1", machine_id: "m", session_id: "child-b", folded_under: "lead", kind: "question", questions: [{ question: "Which test runner?" }], created_at: at } as Interaction;
const interactions = [card];
const machines = [{ id: "m", online: true } as Machine];
const mutes = { spaces: [], sessions: [] };
const keys = (rows: readonly Session[]) => rows.map((s) => s.session_id);

// What every place below agrees on: the parent needs you, the orphan works, the plain session finished unseen.
const NEEDS = 1, WORKING = 1, UNREAD = 1;

describe("child sessions counted once, under their parent (#5120)", () => {
  const shown = foldRows(raw);

  it("lists the parent in its folded state, the orphan, and the plain session", () => {
    expect(keys(shown)).toEqual(["lead", "orphan", "plain"]);
    expect(shown[0].state).toBe("needs_you");
    expect(foldRows(raw)[0]).toBe(shown[0]); // the same copy each time: rows keep their memo
    expect(foldRows(shown)).toBe(shown);
  });

  it("the sidebar's state sections and quick filters", () => {
    const groups = Object.fromEntries(buildAgentGroups(shown, machines, "state", mutes).map((g) => [g.key, keys(g.sessions)]));
    expect(groups["state:needs"]).toEqual(["lead"]);
    expect(groups["state:working"]).toEqual(["orphan"]);
    expect(groups["state:unread"]).toEqual(["plain"]);
    const filters = filterCounts(splitAgentSessions(shown, machines).live, mutes);
    expect([filters.needs, filters.working, filters.unread]).toEqual([NEEDS, WORKING, UNREAD]);
  });

  it("the tab's badge and the sidebar's counts (hooks over the store)", () => {
    fake.state = { sessions: { m: raw }, sessionSummaries: [], features: [], interactions, loaded: true };
    let out: unknown;
    function Probe() {
      out = { list: keys(useAgentSessions()), full: keys(useFullAgentSessions().sessions), counts: useCounts() };
      return null;
    }
    renderToStaticMarkup(<Probe />);
    expect(out).toMatchObject({ list: ["lead", "orphan", "plain"], full: ["lead", "orphan", "plain"], counts: { needs: NEEDS, working: WORKING, unread: UNREAD, badge: NEEDS } });
    expect(tabTitle("Agents", stateCounts(shown).badge)).toBe(`(${NEEDS}) Agents · Persona`);
  });

  it("a child folded under a Persona conversation is no Agent of its own either", () => {
    const chat = row("persona-chat", { state: "idle", assistant: true, children: { total: 1, working: 1 } });
    const kid = row("chat-kid", { state: "working", is_subagent: true, folded_under: "persona-chat" });
    fake.state = { sessions: { m: [chat, kid, plain] }, sessionSummaries: [], features: [], interactions: [], loaded: true };
    let out: string[] = [];
    function Probe() {
      out = keys(useAgentSessions());
      return null;
    }
    renderToStaticMarkup(<Probe />);
    expect(out).toEqual(["plain"]);
  });

  it("the paged list on a folding hub: its rows are the parents, the Earlier fold counts the parent as needing you", () => {
    // roots=1 leaves the folded children out; the parent comes with its own state and its rollup
    const page = [parent, orphan, plain];
    expect(page.filter(runtimeNeedsYou).length).toBe(NEEDS);
    expect(keys(foldRows(page))).toEqual(["lead", "orphan", "plain"]);
    expect(stateCounts(foldRows(page))).toMatchObject({ needs: NEEDS, working: WORKING, unread: UNREAD });
  });

  it("Home: the card is the parent's, which is not listed a second time, and the hint stays off", () => {
    const home = homeOverview(shown, interactions);
    expect(home.groups.needs).toEqual([]); // the parent waits in the cards group, by its child's card
    expect(home.groups.working.map((g) => g.lead.session_id)).toEqual(["orphan"]);
    expect(home.groups.unread.map((g) => g.lead.session_id)).toEqual(["plain"]);
    expect(new Set(interactions.map(homeKey)).size).toBe(interactions.length);
  });

  it("the main chat's dock and its Needs you / In progress entries", () => {
    expect(dockAgents(raw, [], "main", "main").map((a) => [a.session.session_id, a.state])).toEqual([["lead", "needs_you"], ["orphan", "working"]]);
    const entries = assistantEntries({ conversation: "main", main: "main", todos: [], sessions: raw, interactions, plans: {}, feed: [] });
    expect(entries.needsYou.map((item) => item.key)).toEqual(["agent:m/lead"]);
    expect(entries.needsYou[0].kind === "agent" && entries.needsYou[0].asks.map((ix) => ix.id)).toEqual(["ix-1"]);
    expect(entries.running.map((item) => item.key)).toEqual(["agent:m/orphan"]);
  });

  it("the switcher's count, the pending list and the Inbox: one waiting agent, the parent", () => {
    const list = { managers: [{ id: "a", name: "A" }], default: "a" } as unknown as Parameters<typeof managerWaiting>[0];
    expect(managerWaiting(list, shown, interactions)).toEqual({ a: NEEDS });
    const pending = sidebarPending(shown, interactions, {});
    expect(pending.map((p) => [p.session?.session_id, p.cards.length, p.to])).toEqual([["lead", 1, { name: "session", mid: "m", sid: "lead", view: "chat", work: true }]]);
    expect(inboxWaiting(interactions, { m: raw }, mutes, { source: "", kind: "", query: "" }).map((ix) => ix.id)).toEqual(["ix-1"]);
    expect(requestRoute(card)).toEqual({ name: "session", mid: "m", sid: "lead" });
  });

  it("the parent's row says how many subagents run", () => {
    expect(backgroundWork(parent)?.subagents).toBe(1);
    expect(backgroundWork(plain)).toBeUndefined();
  });

  it("notifies the parent's transition and the child's card for the parent, never the child", () => {
    const before = foldRows([row("lead", { state: "idle" }), row("child-b", { state: "working", folded_under: "lead" }), orphan, plain]);
    const prev = detectTransitions(new Map(), before, mutes).next;
    const { next, events } = detectTransitions(prev, shown, mutes, interactions);
    expect(events).toEqual([{ kind: "needs", key: "m/lead" }]);
    expect(next.has("m/child-b")).toBe(false);
    // a second child's card while the parent already waits: announced for the parent
    const second = { ...card, id: "ix-2", session_id: "child-a" };
    const fresh = detectNewCards(new Map([["ix-1", 0]]), [card, second], next, next, shown, new Set(), 1);
    expect(fresh.events.map((e) => [e.key, e.card.id])).toEqual([["m/lead", "ix-2"]]);
  });

  it("a child's card answered here takes the parent out of Needs you at once", () => {
    const answered = { "ix-1": { ix: card, resolution: { kind: "answer" }, at: Date.parse(at) + 1000 } } as unknown as Parameters<typeof answeredSessions>[2];
    const after = foldRows(answeredSessions(raw, [], answered));
    expect(after.find((s) => s.session_id === "lead")?.state).toBe("working");
    expect(stateCounts(after)).toMatchObject({ needs: 0, working: WORKING + 1 });
  });
});
