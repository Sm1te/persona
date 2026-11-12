// A child session (a Codex subagent, an Agent a Paseo Agent created; #5120, after cindy) as a card in its parent's chat:
// never a conversation of its own. Its parent's transcript names no call for it, so the card is built from the child's
// own row and placed by when the child started: in the exchange that was going on then, after the last step that started
// before it, inside that exchange's folded work (super rule 2), the way `withNestedSubagents` places a nested subagent.
// What a card says is views/chat/ChildSessionCard.tsx's (loaded with the card); this is what the chat needs to place it.
import type { ChatLine, Step } from "./turns";
import type { Item, Session } from "./types";

/** The id a child session's card stands under among an exchange's steps, and the call it is known by (`data-tool-use`,
 *  the chat's jump to it). */
const CHILD = "child:";

/** Where a child's card goes: the session, and the time it is placed by. */
export interface ChildPlace {
  sid: string;
  at: string;
}

/** A child session's card among the steps (`withChildSessions`). */
export const isChildSessionStep = (s: Step): boolean => s.kind === "tool" && s.use.id.startsWith(CHILD);

/** The session a child card stands for. */
export const childSessionOf = (s: Step & { kind: "tool" }): string => s.use.id.slice(CHILD.length);

/** The call a child's card is known by: what a link to the child brings into view. */
export const childCall = (sid: string): string => `${CHILD}${sid}`;

/** The child session a call is a child's card's (`childCall`); undefined for any other call. */
export const childOfCall = (call: string): string | undefined => (call.startsWith(CHILD) ? call.slice(CHILD.length) : undefined);

/** The key a child card's open / closed state is kept under (expanded.ts): a link to the child opens it. */
export const childCardKey = (scope: string, sid: string): string => `${scope}${CHILD}${sid}`;

/** The time each child is placed by: when it started, else when it was first seen updated. A child without its start
 *  keeps that first time (`seen`, the chat's own), so a working one does not move to the latest exchange with every push. */
export function childPlaces(rows: readonly Session[], seen: Map<string, string>): ChildPlace[] {
  return rows.map((s) => {
    if (s.started_at) return { sid: s.session_id, at: s.started_at };
    let at = seen.get(s.session_id);
    if (at === undefined) {
      at = s.updated_at ?? "";
      seen.set(s.session_id, at);
    }
    return { sid: s.session_id, at };
  });
}

/** An exchange a chat draws, and when it started (its message, else its first step). */
export interface ExchangeStart {
  id: string;
  start?: string;
}

/** The exchanges a chat draws, in order. */
export function exchangeStarts(lines: readonly ChatLine[]): ExchangeStart[] {
  const out: ExchangeStart[] = [];
  const walk = (ls: readonly ChatLine[]) => {
    for (const l of ls) {
      if (l.kind === "segment") out.push({ id: l.seg.id, start: l.seg.user?.ts ?? l.seg.start });
      else if (l.kind === "supervision") walk(l.lines);
    }
  };
  walk(lines);
  return out;
}

const ms = (ts: string | undefined) => {
  const v = Date.parse(ts ?? "");
  return Number.isFinite(v) ? v : undefined;
};

/** Each child's exchange: the last one that started before it. One that started after the last exchange started is in
 *  that one; one that started before the first exchange loaded is drawn only once the history is `complete` (then in the
 *  first exchange), since its own is still to be loaded. A child without a time goes to the latest exchange. */
export function placeChildren(exchanges: readonly ExchangeStart[], children: readonly ChildPlace[], complete: boolean): Map<string, ChildPlace[]> {
  const by = new Map<string, ChildPlace[]>();
  if (!exchanges.length) return by;
  for (const c of children) {
    const at = ms(c.at);
    let home: string | undefined;
    if (at === undefined) home = exchanges[exchanges.length - 1].id;
    else {
      for (const e of exchanges) {
        const start = ms(e.start);
        if (start !== undefined && start <= at) home = e.id;
      }
      if (home === undefined && complete) home = exchanges[0].id;
    }
    if (home === undefined) continue;
    by.set(home, [...(by.get(home) ?? []), c]);
  }
  // in the chat's order, each exchange's oldest first: the same children give the same map in any order (the chat keeps
  // the last one while it holds the same, stable-lines.ts stableValue)
  const out = new Map<string, ChildPlace[]>();
  for (const e of exchanges) {
    const list = by.get(e.id);
    if (list) out.set(e.id, list.sort((a, b) => (ms(a.at) ?? Infinity) - (ms(b.at) ?? Infinity) || (a.sid < b.sid ? -1 : a.sid > b.sid ? 1 : 0)));
  }
  return out;
}

const stepTs = (s: Step) => (s.kind === "tool" ? s.use.ts : s.item.ts);
const stepSeq = (s: Step) => (s.kind === "tool" ? s.use.seq : s.item.seq);

/** An exchange's steps with a card for each child placed in it: after the last step that started before the child (the
 *  exchange's start when none did), in the order the children started. A card takes the time of the step it follows (the
 *  next one's at the start): it moves no clock of the exchange's work, and a child that started long after the
 *  exchange's last step does not split its work in two (work-groups.ts HISTORY_GAP_MS). */
export function withChildSessions(steps: Step[], places: readonly ChildPlace[] | undefined): Step[] {
  if (!places?.length) return steps;
  const out = [...steps];
  for (const c of places) {
    const at = ms(c.at) ?? Infinity;
    let i = 0;
    for (let j = 0; j < out.length; j++) {
      const t = ms(stepTs(out[j]));
      if (t !== undefined && t <= at) i = j + 1;
    }
    const near = out[i - 1] ?? out[i];
    const id = childCall(c.sid);
    const use: Item = { id, seq: near ? stepSeq(near) : 0, ts: near ? stepTs(near) : c.at || undefined, role: "assistant", kind: "tool_use", tool_name: "Agent", tool_use_id: id, tool_input: {} };
    out.splice(i, 0, { kind: "tool", id, use, images: [] });
  }
  return out;
}
