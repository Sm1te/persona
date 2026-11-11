// Resume (#4754, docs/integrations/engines.md "Agents run in a terminal"): the one way a stopped Agent runs again is the
// user's Resume on its terminal page, `POST /sessions {machine_id, resume_session_id}` with no message. The chat's
// read-only line and the terminal's not-running state both start it, so how it went is kept here, per session, and
// both show the same progress and the same error. A pure store, tested without a DOM.
import { useSyncExternalStore } from "react";
import { api, errorText } from "./api";
import type { Session } from "./types";

export type ResumeState =
  | { phase: "idle" }
  | { phase: "resuming" }
  | { phase: "failed"; error: string }
  /** `at`: when it came back, so a terminal reconnects once for it. */
  | { phase: "done"; at: number };

const IDLE: ResumeState = { phase: "idle" };
const states = new Map<string, ResumeState>();
const listeners = new Set<() => void>();
const keyOf = (mid: string, sid: string) => `${mid}/${sid}`;

function put(key: string, state: ResumeState) {
  if (state.phase === "idle") states.delete(key);
  else states.set(key, state);
  listeners.forEach((l) => l());
}

export function resumeState(mid: string, sid: string): ResumeState {
  return states.get(keyOf(mid, sid)) ?? IDLE;
}

export function subscribeResume(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

/** Forgets how the last Resume went (the error once it was read, a test's leftovers). */
export function clearResume(mid: string, sid: string) {
  put(keyOf(mid, sid), IDLE);
}

type Post = (path: string, init: { method: "POST"; json: unknown }) => Promise<unknown>;

/** Resumes the stopped Agent `sid` on `mid`; true when the hub started it. A second press while one is out does nothing. */
export async function resumeAgent(mid: string, sid: string, post: Post = (path, init) => api<{ session: Session }>(path, init)): Promise<boolean> {
  const key = keyOf(mid, sid);
  if (states.get(key)?.phase === "resuming") return false;
  put(key, { phase: "resuming" });
  try {
    await post("/sessions", { method: "POST", json: { machine_id: mid, resume_session_id: sid } });
    put(key, { phase: "done", at: Date.now() });
    return true;
  } catch (e) {
    put(key, { phase: "failed", error: errorText(e) });
    return false;
  }
}

export function useResume(mid: string, sid: string): ResumeState {
  return useSyncExternalStore(subscribeResume, () => resumeState(mid, sid));
}
