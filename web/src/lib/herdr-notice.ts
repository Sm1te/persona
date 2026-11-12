// The Agents list's "herdr isn't running on <computer>" rows with "Start" (#1916, docs/integrations/engines.md "When herdr is not running"), as on iOS
// (HerdrNotice.swift) and Android (#2066): told before a message to an agent there fails, not after. Only classic herdr
// (engines herdr and herdr-both): Herdr App's herdr-core has no server for the node to start.

import { ApiError, api, enc } from "./api";
import { currentEngine } from "./engines";
import type { Machine } from "./types";
import { t } from "../i18n";

/** GET /api/v1/machines/{mid}/herdr. `known` false: the node could not say (offline, older, Herdr App), which is not
 *  "it does not run". */
export interface HerdrState {
  autostart: boolean;
  running: boolean;
  known: boolean;
}

/** Why a "Start" did not work: what the row says, in words, and what "Copy details" copies (the hub's own text, its status and
 *  request id), kept out of the row. */
export interface HerdrFailure {
  text: string;
  detail: string;
}

/** How often the list asks while it is shown: once a minute, as on Apple (#4685). Each read is a node round trip on the hub,
 *  and the list asks again at once when a watched machine comes online or changes engine, or the tab is shown (#5060). */
export const HERDR_POLL_MS = 60_000;

/** A machine the row can be about: online, on classic herdr. */
export function herdrWatched(m: Pick<Machine, "online" | "engine">): boolean {
  const e = currentEngine(m);
  return m.online && (e === "herdr" || e === "herdr-both");
}

/** The node's and the hub's known failures of herdr.server.start (server/internal/node/herdrstart.go) in words;
 *  anything else as it came. */
export function herdrFailureText(message: string): string {
  const m = message.trim();
  const after = m.indexOf("herdr server stopped right after starting");
  if (after >= 0) {
    const why = m.slice(after);
    const code = /exit status (-?\d+)/.exec(why)?.[1];
    if (code) return t("herdr-notice.exited_code", { code });
    const sig = /signal: ([\w ]+)/.exec(why)?.[1];
    if (sig) return t("herdr-notice.killed", { signal: sig.trim() });
    return t("herdr-notice.exited");
  }
  if (m.includes("herdr is not installed")) return t("herdr-notice.not_installed");
  if (m.includes("herdr did not answer on")) {
    const s = /within (\d+)s/.exec(m)?.[1];
    return s ? t("herdr-notice.no_answer_within", { seconds: s }) : t("herdr-notice.no_answer");
  }
  if (m.includes("start herdr server:")) return t("herdr-notice.cannot_run");
  if (m.includes("unknown method herdr.server.start")) return t("herdr-notice.node_too_old");
  if (m.includes("machine is offline")) return t("herdr-notice.offline");
  const low = m.toLowerCase();
  if (low.includes("deadline exceeded") || low.includes("timed out") || low.includes("timeout")) return t("herdr-notice.timeout");
  return m;
}

export function herdrFailure(error: unknown): HerdrFailure {
  if (error instanceof ApiError) {
    const lines = [error.message, `HTTP ${error.status}`];
    if (error.requestId) lines.push(`Request ${error.requestId}`);
    return { text: herdrFailureText(error.message), detail: lines.join("\n") };
  }
  const text = error instanceof Error ? error.message : String(error);
  return { text, detail: text };
}

export interface HerdrApi {
  read: (mid: string) => Promise<HerdrState>;
  start: (mid: string) => Promise<HerdrState>;
}

export const herdrApi: HerdrApi = {
  read: (mid) => api<HerdrState>(`/machines/${enc(mid)}/herdr`),
  start: (mid) => api<HerdrState>(`/machines/${enc(mid)}/herdr/start`, { method: "POST" }),
};

export interface HerdrSnapshot {
  /** The machines whose herdr is known not to run, as last read. */
  stopped: ReadonlySet<string>;
  /** "Start" pressed and not answered yet. */
  starting: ReadonlySet<string>;
  /** Why the last "Start" did not work (shown under the row until it is pressed again). */
  failures: ReadonlyMap<string, HerdrFailure>;
}

/** Which machines' herdr does not run, and the starts made from the rows. A plain store (subscribe / snapshot) so a
 *  read that changes nothing redraws nothing. */
export class HerdrWatch {
  private snap: HerdrSnapshot = { stopped: new Set(), starting: new Set(), failures: new Map() };
  private listeners = new Set<() => void>();
  /** Their hub has no such row (older than it, 404): not asked again. */
  private unsupported = new Set<string>();
  /** Started from here and the node said it answers: that stands until the hub says running too (a hub that only
   *  knew what node.info said went on saying "not running"). */
  private started = new Set<string>();

  constructor(private readonly hub: HerdrApi = herdrApi) {}

  subscribe = (fn: () => void) => {
    this.listeners.add(fn);
    return () => void this.listeners.delete(fn);
  };
  snapshot = () => this.snap;

  private set(next: Partial<HerdrSnapshot>) {
    this.snap = { ...this.snap, ...next };
    this.listeners.forEach((fn) => fn());
  }

  /** Asks about each watched machine. A failed read keeps what the row showed (one lost poll must not flash it); a
   *  machine no longer watched (offline, another engine) loses its row. */
  async refresh(machines: Pick<Machine, "id" | "online" | "engine">[]): Promise<void> {
    const ids = new Set(machines.filter(herdrWatched).map((m) => m.id));
    for (const id of [...this.started]) if (!ids.has(id)) this.started.delete(id);
    const next = new Set([...this.snap.stopped].filter((id) => ids.has(id)));
    await Promise.all([...ids].filter((id) => !this.unsupported.has(id)).map(async (id) => {
      let state: HerdrState;
      try {
        state = await this.hub.read(id);
      } catch (e) {
        if (e instanceof ApiError && e.status === 404) {
          this.unsupported.add(id);
          next.delete(id);
        }
        return;
      }
      if (this.snap.starting.has(id)) return; // a start made meanwhile answers for itself
      if (state.running) this.started.delete(id); // the hub knows now: a later stop shows again
      if (state.known && !state.running && !this.started.has(id)) next.add(id);
      else next.delete(id);
    }));
    if (!sameSet(next, this.snap.stopped)) this.set({ stopped: next });
  }

  /** The row's "Start": the node starts herdr's server whatever "start automatically when needed" says. */
  async start(id: string): Promise<void> {
    if (this.snap.starting.has(id)) return;
    const failures = new Map(this.snap.failures);
    failures.delete(id);
    this.set({ starting: new Set([...this.snap.starting, id]), failures });
    let failure: HerdrFailure | undefined;
    let ran = false;
    try {
      ran = (await this.hub.start(id)).running;
      if (!ran) failure = { text: t("herdr-notice.no_answer_after_start"), detail: "herdr.server.start: running false" };
    } catch (e) {
      failure = herdrFailure(e);
    }
    const starting = new Set(this.snap.starting);
    starting.delete(id);
    const next: Partial<HerdrSnapshot> = { starting };
    if (ran) {
      this.started.add(id);
      const stopped = new Set(this.snap.stopped);
      stopped.delete(id);
      next.stopped = stopped;
    }
    if (failure) next.failures = new Map(this.snap.failures).set(id, failure);
    this.set(next);
  }
}

function sameSet(a: ReadonlySet<string>, b: ReadonlySet<string>): boolean {
  return a.size === b.size && [...a].every((x) => b.has(x));
}
