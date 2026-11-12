// The chat's newest plan for the pill above the composer (#5156, after cindy's PinnedPlanPanel): which plan it is, whether
// its card is in view, and Close Plan. Read on every render of the chat, so it stays on the first screen; the card and
// the pill themselves (PlanCard.tsx) load on demand.
import { useCallback, useEffect, useLayoutEffect, useMemo, useReducer, useState, type RefObject } from "react";
import type { Segment } from "../../lib/turns";
import { isPlanList, planTodos, type Todo } from "../../lib/work-groups";

/** The chat's newest plan: the latest plan list (work-groups.ts `isPlanList`, the same rule as the card) in its newest
 *  exchange that has one, also when the hub folded it away with the work around it (the fold keeps the list, #5602). `superseded`: a message of the user's
 *  came after the exchange it was made in (cindy's `isPlanUserBoundary`), so it is an earlier turn's plan. */
export function latestPlan(segs: Segment[]): { id: string; todos: Todo[]; superseded: boolean } | undefined {
  let later = false;
  for (let i = segs.length - 1; i >= 0; i--) {
    for (let k = segs[i].steps.length - 1; k >= 0; k--) {
      const st = segs[i].steps[k];
      if (isPlanList(st)) return { id: st.id, todos: planTodos(st), superseded: later };
      if (st.kind === "fold") {
        const snap = st.item.fold?.tasks?.findLast((t) => t.replace === "todo" && t.entries.length >= 2);
        if (snap) return { id: snap.source_item_id, todos: snap.entries.map((e) => ({ content: e.subject ?? "", status: todoStatus(e.status) })).filter((t) => t.content), superseded: later };
      }
    }
    if (startsTurn(segs[i])) later = true;
  }
  return undefined;
}

/** A message the user wrote starts a turn of their own; what Persona typed in, another session sent, a supervision round
 *  or a message still queued does not (cindy's synthetic rows: an automation, a resume, a subagent). */
function startsTurn(seg: Segment): boolean {
  const u = seg.user;
  return !!u && !u.injected && !u.from && !u.source && !u.queued && !seg.supervision;
}

/** Whether the plan card `id` is out of the scroll view (scrolled past, not reached, or not drawn at all). */
export function usePlanOutOfView(scroller: RefObject<HTMLDivElement | null>, id?: string): boolean {
  const [out, setOut] = useState(false);
  // the chat's scroller can be drawn again in a new element (the page wraps it differently once it knows the agent has a
  // terminal to switch to): watch the one on the page, not the first one seen
  const [sc, setSc] = useState<HTMLDivElement | null>(null);
  useLayoutEffect(() => {
    if (scroller.current !== sc) setSc(scroller.current);
  });
  useEffect(() => {
    // a scroller with nothing in it yet is watched too: its content box comes later (watchBody)
    if (!sc || !id) return setOut(false);
    const find = () => sc.querySelector(`[data-plan-id="${CSS.escape(id)}"]`);
    return typeof IntersectionObserver === "undefined" ? watchByScroll(sc, find, setOut) : watchByIntersection(sc, id, find, setOut);
  }, [sc, id]);
  return out;
}

/** Calls `changed` when the chat's content box (the scroller's first child) changes size, or is replaced by a new one,
 *  which is watched from then on. */
function watchBody(sc: Element, changed: () => void): () => void {
  let body = sc.firstElementChild;
  const ro = new ResizeObserver(changed);
  if (body) ro.observe(body);
  const mo = typeof MutationObserver === "undefined" ? null : new MutationObserver(() => {
    if (sc.firstElementChild === body) return;
    if (body) ro.unobserve(body);
    body = sc.firstElementChild;
    if (body) ro.observe(body);
    changed();
  });
  mo?.observe(sc, { childList: true });
  return () => {
    ro.disconnect();
    mo?.disconnect();
  };
}

/** The browser says when the card enters or leaves the view, so a scroll or a growing chat queries nothing and reads no
 *  layout (reading the rect of a card in an exchange that content-visibility skips lays that exchange out once the chat's
 *  width changed, #2485). Edge-adjacent counts as intersecting, the same rule as the measuring fallback. The card is
 *  looked for again only once the one watched left the DOM, or while none is drawn: the body's ResizeObserver sees a card
 *  that came or went; a card redrawn at the same size in view sends its last report as it leaves; one redrawn at the same
 *  size out of view sends none (it was not intersecting and still is not), so a scroll checks that the watched card is
 *  still in the DOM, which reads no layout, and looks for the new one at most once. */
function watchByIntersection(sc: Element, id: string, find: () => Element | null, setOut: (out: boolean) => void): () => void {
  let el: Element | null = null;
  const current = () => el?.isConnected && el.getAttribute("data-plan-id") === id;
  const refind = () => {
    if (current()) return;
    if (el) io.unobserve(el);
    el = find();
    // not drawn (inside a folded summary, or folded away by the hub with the work around it): the pill is where it shows
    if (!el) return setOut(true);
    io.observe(el);
  };
  // entries of a card no longer watched may still be queued: only the watched card's latest one counts
  const io = new IntersectionObserver((entries) => {
    const last = entries.findLast((e) => e.target === el);
    if (!last) return;
    if (current()) setOut(!last.isIntersecting);
    else refind();
  }, { root: sc });
  // while none is drawn, a scroll looks for nothing: a card that comes back changes the body's size
  const onScroll = () => {
    if (el && !current()) refind();
  };
  refind();
  sc.addEventListener("scroll", onScroll, { passive: true });
  const unwatch = watchBody(sc, refind);
  return () => {
    sc.removeEventListener("scroll", onScroll);
    unwatch();
    io.disconnect();
  };
}

/** Without IntersectionObserver (jsdom): measure the card against the view on every scroll and resize. */
function watchByScroll(sc: Element, find: () => Element | null, setOut: (out: boolean) => void): () => void {
  const check = () => {
    const el = find();
    if (!el) return setOut(true);
    const r = el.getBoundingClientRect();
    const v = sc.getBoundingClientRect();
    setOut(r.bottom < v.top || r.top > v.bottom);
  };
  check();
  sc.addEventListener("scroll", check, { passive: true });
  const unwatch = watchBody(sc, check);
  return () => {
    sc.removeEventListener("scroll", check);
    unwatch();
  };
}

const todoStatus = (s?: string): Todo["status"] => (s === "completed" || s === "in_progress" ? s : "pending");

/** What Close Plan closed, per chat, for this page's life (cindy keeps it in the view's state, not on disk): the plan as
 *  it was then, so the pill comes back once the plan changes (a step done, a new plan), as cindy's snapshot does. */
const closedPlans = new Map<string, string>();

const planSnapshot = (plan: { id: string; todos: Todo[] }) => JSON.stringify([plan.id, plan.todos.map((todo) => [todo.status, todo.content])]);

/** Whether the pill of chat `chat`'s plan was closed with Close Plan, and the close itself. */
export function usePlanClosed(chat: string, plan: { id: string; todos: Todo[] } | undefined): [boolean, () => void] {
  const snapshot = useMemo(() => (plan ? planSnapshot(plan) : ""), [plan]);
  const [, redraw] = useReducer((n: number) => n + 1, 0);
  const close = useCallback(() => {
    if (!snapshot) return;
    closedPlans.set(chat, snapshot);
    redraw();
  }, [chat, snapshot]);
  return [!!snapshot && closedPlans.get(chat) === snapshot, close];
}
