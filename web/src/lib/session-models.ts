// What an Agent's model pill and panel read (#5032, docs/web/web.md "Model pill and model panel"): the launchers of the
// computer it runs on (the panel's providers), each with the models its agent lists in its own /model menu
// (lib/launcher-models.ts), and what the pill says. The panel's own data and what a pick sends are built from these in
// lib/session-panel.ts, which loads with the panel.
//
// A list read from the agent takes seconds the first time, so what was read is kept per computer and launcher for the
// page's life: the pill and a later opening show the last list at once, and an opening reads every list again behind it
// (the session's own first, at most three at a time).
import { useSyncExternalStore } from "react";
import { t } from "../i18n";
import { ApiError, errorText } from "./api";
import { agentId, sessionAgent } from "./agent-kind";
import { modelShort } from "./context-usage";
import { stateOf } from "./format";
import { defaultModel, forgetCustomModel, launcherModels, machineLaunchers, modelName, withoutCustom, type AgentModel, type LauncherModels } from "./launcher-models";
import { canSwitchModel, isCurrentModel, sessionLauncher } from "./model-switch";
import { hasEffortOnly, hasModes } from "./session-actions";
import type { Launcher, Session } from "./types";

/** One launcher's list as read: `list` undefined until a read answered, null from an older hub (it has no list). */
export interface ModelList {
  list?: LauncherModels | null;
  /** A read is on its way. */
  loading?: boolean;
  /** The last read failed and there is no list to show. */
  error?: string;
}

// ── what was read, kept for the page's life ──────────────────────────────────

const lists = new Map<string, ModelList>();
const machines = new Map<string, { launchers?: Launcher[]; loading?: boolean }>();
const reads = new Map<string, Promise<void>>();
const machineReads = new Map<string, Promise<void>>();
// the typed IDs whose Forget is still on its way, per list: a list read meanwhile doesn't bring them back
const forgetting = new Map<string, Set<string>>();
let version = 0;
const subscribers = new Set<() => void>();
const changed = () => {
  version++;
  for (const f of subscribers) f();
};
const subscribe = (f: () => void) => {
  subscribers.add(f);
  return () => void subscribers.delete(f);
};
const listKey = (mid: string, launcher: string) => `${mid}/${launcher}`;

/** Draws again whenever a list or a computer's launchers change. */
export function useModelLists(): number {
  return useSyncExternalStore(subscribe, () => version, () => version);
}

export const modelList = (mid: string, launcher: string): ModelList => lists.get(listKey(mid, launcher)) ?? {};

/** The launchers of `mid` as last read; undefined before the first answer. */
export const machineLauncherList = (mid: string): Launcher[] | undefined => machines.get(mid)?.launchers;

// at most three lists are read at a time (each may start the agent's CLI on the computer); `first` jumps the queue
const MAX_READS = 3;
const queue: (() => void)[] = [];
let running = 0;
function slot(first: boolean): Promise<() => void> {
  return new Promise((go) => {
    const start = () => {
      running++;
      go(() => {
        running--;
        queue.shift()?.();
      });
    };
    if (running < MAX_READS) start();
    else if (first) queue.unshift(start);
    else queue.push(start);
  });
}

/** Reads a launcher's list: once (the pill), or `again` behind the one shown (an opening, Retry). */
export function readModels(mid: string, launcher: string, o: { again?: boolean; first?: boolean } = {}): Promise<void> {
  const key = listKey(mid, launcher);
  const reading = reads.get(key);
  if (reading) return reading;
  const had = lists.get(key);
  if (had?.list !== undefined && !o.again) return Promise.resolve();
  lists.set(key, { list: had?.list, loading: true });
  changed();
  const read = slot(Boolean(o.first)).then(async (done) => {
    try {
      const r = await launcherModels(mid, launcher);
      const gone = forgetting.get(key);
      lists.set(key, { list: r && gone?.size ? { ...r, models: r.models.filter((m) => !(m.custom && gone.has(m.id))) } : r });
    } catch (e) {
      // a list already shown stays (and says nothing): a newer one just didn't come
      const now = lists.get(key);
      lists.set(key, now?.list !== undefined ? { list: now.list } : { error: errorText(e) });
    } finally {
      done();
      reads.delete(key);
      changed();
    }
  });
  reads.set(key, read);
  return read;
}

/** Reads the computer's launchers (the providers), the last ones shown meanwhile. */
function readLaunchers(mid: string): Promise<void> {
  const reading = machineReads.get(mid);
  if (reading) return reading;
  const had = machines.get(mid);
  machines.set(mid, { launchers: had?.launchers, loading: true });
  changed();
  const read = machineLaunchers(mid, { purpose: "agent.new" }).then(
    (r) => void machines.set(mid, { launchers: r.launchers ?? [] }),
    () => void machines.set(mid, { launchers: had?.launchers }),
  ).finally(() => {
    machineReads.delete(mid);
    changed();
  });
  machineReads.set(mid, read);
  return read;
}

/** An opening of the panel on computer `mid` for a session of launcher `own`: every list read again behind the one shown,
 *  the session's own first. */
export function readForPanel(mid: string, own: string): void {
  void readModels(mid, own, { again: true, first: true });
  void readLaunchers(mid).then(() => {
    for (const l of machines.get(mid)?.launchers ?? []) if (l.name !== own) void readModels(mid, l.name, { again: true });
  });
}

/** Forgets an ID typed before (#1644): off the list at once, back where it was when the hub says no. "": done; else why not. */
export async function forgetModel(mid: string, launcher: string, id: string): Promise<string> {
  const key = listKey(mid, launcher);
  const before = lists.get(key)?.list;
  if (!before) return "";
  lists.set(key, { list: withoutCustom(before, id).list });
  changed();
  const gone = forgetting.get(key) ?? new Set<string>();
  forgetting.set(key, gone.add(id));
  try {
    await forgetCustomModel(mid, launcher, id);
    return "";
  } catch (e) {
    const now = lists.get(key)?.list;
    lists.set(key, { list: now ? withoutCustom(before, id).restore(now) : before });
    changed();
    return t("composer.forget_failed", { id, error: errorText(e) });
  } finally {
    gone.delete(id);
  }
}

// ── the session's model, from what was read ──────────────────────────────────

/** The engine a session runs (AgentMark ids: claude, codex, agy…). */
export const sessionEngine = (s: Session): string => agentId(sessionAgent(s).agent);

/** What the pill and the panel can change on the session now: its model (Claude Code in a terminal, a runtime that says
 *  so), its effort (the composer's chips used to; a Codex thread on its app-server too). Neither: the pill is a label. */
export function sessionSwitches(s: Session): { model: boolean; effort: boolean } {
  const live = stateOf(s) !== "offline";
  return { model: canSwitchModel(s), effort: live && (hasModes(s) || hasEffortOnly(s)) };
}

/** The row of `models` the session runs: the one its model names (an alias or what an alias stands for; the default entry
 *  only when no other row names it); with no model reported yet, the default entry, which is what runs then. */
export function currentModelRow(models: AgentModel[], model: string | undefined): AgentModel | undefined {
  if (!model) return defaultModel(models);
  const hits = models.filter((m) => isCurrentModel(m, model));
  return hits.find((m) => !m.default) ?? hits[0];
}

/** A model ID said briefly where no list names it: Claude's as "opus 5.5", GPT's as their list would, a gateway's path
 *  without its vendor. */
export function modelLabel(id: string): string {
  const short = id.split("/").pop() || id;
  const claude = modelShort(short);
  return claude !== short ? claude : modelName({ id: short });
}

/** One setup as the pill draws it. */
export interface PillSetup {
  /** The engine (AgentMark ids: claude, codex…). */
  engine: string;
  /** The provider (launcher) name: its tile, and the tooltip. */
  provider: string;
  /** The model's name; absent while not known (no reply yet and no list to say what the launcher runs by default). */
  model?: string;
  effort?: string;
  fast?: boolean;
}

/** What the pill says: the engine, the launcher and the model the session runs (its list's name for it once read), the
 *  effort and speed; `loading` while the list is read the first time, `failed` when that failed. */
export interface PillView extends PillSetup {
  loading: boolean;
  failed: boolean;
}

/** A message refused because the switch to another launcher it was to carry out first could not be (#5035,
 *  docs/protocol/protocol.md "Switching a running Agent to another provider": `switch_launcher_missing`,
 *  `switch_failed`…): nothing was typed and the switch was dropped. */
export const switchRefused = (e: unknown): boolean => e instanceof ApiError && e.status === 409 && Boolean(e.code?.startsWith("switch_"));

/** The switch to another launcher waiting for the next message (#5035), as the pill draws it: that launcher, the model
 *  picked (its list's name for it once read; none picked: the launcher's default), the effort picked or the one it runs
 *  with now. null: none waits. */
export function pendingView(s: Session, listOf: (launcher: string) => ModelList): PillSetup | null {
  const ps = s.pending_switch;
  if (!ps?.launcher || ps.launcher === sessionLauncher(s)) return null;
  const models = listOf(ps.launcher).list?.models ?? [];
  const row = ps.model ? models.find((m) => m.id === ps.model) : defaultModel(models);
  const out: PillSetup = { engine: sessionEngine(s), provider: ps.launcher, model: row ? modelName(row) : ps.model ? modelLabel(ps.model) : t("chat-modelpill.default_model") };
  const effort = ps.effort || s.effort;
  if (effort) out.effort = effort;
  return out;
}

export function pillView(s: Session, e: ModelList): PillView {
  const row = currentModelRow(e.list?.models ?? [], s.model);
  const model = row ? modelName(row) : s.model ? modelLabel(s.model) : undefined;
  const out: PillView = { engine: sessionEngine(s), provider: sessionLauncher(s), loading: e.list === undefined && Boolean(e.loading), failed: e.list === undefined && Boolean(e.error) };
  if (model) out.model = model;
  if (s.effort) out.effort = s.effort;
  if (s.service_tier === "priority") out.fast = true;
  return out;
}

/** Forgets every list and read kept (tests: each starts with nothing read). */
export function resetModelListsForTests(): void {
  lists.clear();
  machines.clear();
  reads.clear();
  machineReads.clear();
  forgetting.clear();
  queue.length = 0;
  running = 0;
  changed();
}

/** Subscribes to every change of what was read (views/NewAgentModel.tsx useModelList follows one list with it). */
export const subscribeModelLists = subscribe;
