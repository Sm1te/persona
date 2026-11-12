// The model panel's data for an Agent's chat (#5032, docs/web/web.md "Model pill and model panel"), from what
// lib/session-models.ts read: the computer's launchers are the providers, each with its list, and the setup the session
// runs now; and what a pick sends. The session's own launcher switches its model at once. On a hub that switches a
// running Agent to another launcher (`launcher_switch`, #5035), a Claude Code Agent in a terminal may also pick another
// launcher of its engine: the pick is recorded and the next message carries it out (the agent restarts on that launcher
// and keeps the conversation). Another engine needs a new conversation, and an agent that can't change its model while
// it runs (a Codex thread) keeps its effort and speed only. Loaded with the panel, not with the composer.
import { t } from "../i18n";
import { agentId, agentName } from "./agent-kind";
import { ALL_EFFORTS, defaultModel, launcherAgent, modelName, typedModelOffered, type AgentModel, type LauncherModels } from "./launcher-models";
import { validModelId } from "./model-id";
import { cleanSetup, type PanelConfig, type PanelModel, type PanelProvider, type PanelState } from "./model-panel";
import { canSwitchModel, isCurrentModel, modelBusy, sessionLauncher } from "./model-switch";
import { currentModelRow, modelLabel, sessionEngine, type ModelList } from "./session-models";
import type { Launcher, Session } from "./types";

const hasFast = (s: Session) => Boolean(s.service_tiers?.some((x) => x.id === "priority"));

/** The efforts a row's slider offers. Antigravity lists one model per thinking level, so its rows have none (a pick
 *  takes the variant's own); the model a thread runs takes the thread's own levels (a Codex thread says them); an ID
 *  typed by hand into Claude Code takes any, as nobody knows. */
function rowEfforts(engine: string, m: AgentModel, listed: boolean, current: boolean, s: Session): string[] {
  if (engine === "agy") return [];
  if (current && s.efforts?.length) return s.efforts;
  if (listed && !m.custom) return m.efforts ?? [];
  return engine === "claude" ? ALL_EFFORTS : [];
}

/** A row of the panel; `listed` false: the model the session runs, which its list doesn't name. */
function panelModel(m: AgentModel, engine: string, current: boolean, s: Session, listed = true): PanelModel {
  const out: PanelModel = { id: m.id, name: modelName(m), efforts: rowEfforts(engine, m, listed, current, s) };
  if (m.description) out.description = m.description;
  if (m.resolved) out.resolved = m.resolved;
  if (m.default_effort) out.defaultEffort = m.default_effort;
  if (m.custom) out.custom = true;
  if (m.price) out.price = m.price;
  if (current && engine === "codex" && hasFast(s)) out.fast = true;
  if (current && s.context_window) out.context = s.context_window;
  return out;
}

export interface SessionPanel {
  providers: PanelProvider[];
  state: PanelState;
}

/** The hub switches a running Agent to another launcher (#5035): the panel offers other launchers only on such a hub. */
export const LAUNCHER_SWITCH_FEATURE = "launcher_switch";

export interface PanelOptions {
  /** The hub has LAUNCHER_SWITCH_FEATURE. */
  switching?: boolean;
}

/** Whether `s` may move to another launcher of its engine: on a hub that does it, a Claude Code Agent whose model the
 *  app switches (one in a terminal; the node refuses the rest, `switch_unsupported`). */
export const switchesLauncher = (s: Session, o: PanelOptions): boolean => Boolean(o.switching) && sessionEngine(s) === "claude" && canSwitchModel(s);

/** The panel's providers and state for `s`: `launchers` the computer's (undefined before they are read), `listOf` what
 *  was read for each. The session's own launcher is always there, at its place in the list or first. */
export function sessionPanel(s: Session, launchers: Launcher[] | undefined, listOf: (launcher: string) => ModelList, o: PanelOptions = {}): SessionPanel {
  const own = sessionLauncher(s);
  const engine = sessionEngine(s);
  const modelSwitch = canSwitchModel(s);
  const switching = switchesLauncher(s, o);
  const all = launchers ?? [];
  const names = all.some((l) => l.name === own) ? all.map((l) => l.name) : [own, ...all.map((l) => l.name)];
  const ownList = listOf(own).list;
  const row = currentModelRow(ownList?.models ?? [], s.model);

  const providers = names.map((name): PanelProvider => {
    const e = listOf(name);
    const mine = name === own;
    const kind = mine ? engine : agentId(launcherAgent(name, all, e.list));
    // another launcher of the engine, on a hub that switches to it with the next message
    const other = !mine && switching && kind === engine;
    const p: PanelProvider = { id: name, name, engine: kind, models: null };
    if (e.list === undefined) {
      if (e.error) p.error = e.error;
      else p.loading = true;
      // the model it runs stays there while its list is read or couldn't be: its flyout (effort, Fast, ultracode) works
      // as the old popover's tabs did with no list
      if (mine && s.model) p.models = [panelModel({ id: s.model, name: modelLabel(s.model) }, kind, true, s, false)];
    } else {
      let models = (e.list?.models ?? []).map((m) => panelModel(m, kind, mine && m === row, s));
      // a launcher that fixes its own --model takes no model with a switch: its default entry, only
      if (other && e.list?.locked) models = models.filter((m) => m.id === defaultModel(e.list?.models ?? [])?.id);
      // the model it runs, when the list doesn't name it (a list that couldn't be read, an older hub, a model it lacks)
      if (mine && !row && s.model) models.push(panelModel({ id: s.model, name: modelLabel(s.model) }, kind, true, s, false));
      p.models = models;
    }
    if (other) {
      if (typedModelOffered(kind, e.list)) p.typable = true;
    } else if (!mine) {
      p.unavailable = kind === engine ? t("chat-modelpanel.other_provider") : t("chat-modelpanel.other_engine", { from: agentName(engine), to: agentName(kind) });
    } else if (!modelSwitch) {
      p.unavailable = t("chat-modelpanel.model_fixed", { agent: agentName(engine) });
    } else {
      if (e.list?.locked) p.locked = t("composer.locked");
      if (typedModelOffered(engine, e.list)) p.typable = true;
    }
    return p;
  });

  const model = row?.id ?? s.model;
  const state: PanelState = {
    current: model ? cleanSetup({ provider: own, model, effort: s.effort, fast: s.service_tier === "priority" }) : null,
    // the models that switch at once: the session's own launcher's
    recommended: modelSwitch ? [own] : [],
  };
  const ps = s.pending_switch;
  if (ps?.launcher && ps.launcher !== own) {
    state.pending = cleanSetup({ provider: ps.launcher, model: ps.model || defaultModel(listOf(ps.launcher).list?.models ?? [])?.id || "", effort: ps.effort });
    state.note = t("chat-modelpanel.pending_note", { provider: ps.launcher });
  }
  const busy = modelSwitch ? modelBusy(s) : "";
  if (busy) state.busy = busy;
  return { providers, state };
}

/** One POST …/modes of a pick. `launcher`: a switch to another launcher with the user's next message (#5035), or the
 *  session's own to drop one that waits; `from` the launcher the session runs on as the panel saw it. */
export type ModeStep = { model: string } | { effort: string } | { service_tier: string } | { launcher: string; from: string; model?: string; effort?: string };

/** What applying `config` sends, in order. On the session's own launcher: a switch that waits is dropped first, then the
 *  model (the efforts a model takes depend on it), then the effort, then the speed; each only when it differs from the
 *  session's. On another launcher of the engine (a hub with the switch): one request recording the switch, with the model
 *  unless it is that launcher's default entry, and the effort when the setup has one. null: it can't be applied here
 *  (another launcher without the switch or of another engine, a model this agent can't switch to while it runs, an ID
 *  the hub would refuse). An Antigravity variant brings its own thinking level. `listOf`: each launcher's list as read. */
export function pickPlan(s: Session, config: PanelConfig, listOf: (launcher: string) => LauncherModels | null | undefined, o: PanelOptions = {}): ModeStep[] | null {
  const own = sessionLauncher(s);
  if (config.provider !== own) return switchPlan(s, config, listOf(config.provider), o);
  const models = listOf(own)?.models ?? [];
  const row = models.find((m) => m.id === config.model);
  const current = currentModelRow(models, s.model);
  const same = row ? row === current || isCurrentModel(row, s.model) : config.model === s.model;
  if (!same && (!canSwitchModel(s) || (!row && !validModelId(config.model)))) return null;
  const steps: ModeStep[] = [];
  if (s.pending_switch?.launcher && s.pending_switch.launcher !== own) steps.push({ launcher: own, from: own });
  if (!same) steps.push({ model: config.model });
  const effort = config.effort || (sessionEngine(s) === "agy" ? row?.default_effort : undefined);
  if (effort && effort !== s.effort) steps.push({ effort });
  // a saved setup without Fast is Standard (cleanSetup drops `fast: false`)
  const speed = speedStep(s, Boolean(config.fast));
  if (speed) steps.push(speed);
  return steps;
}

/** A pick on another launcher: the switch the next message carries out; nothing when that very switch waits already. */
function switchPlan(s: Session, config: PanelConfig, list: LauncherModels | null | undefined, o: PanelOptions): ModeStep[] | null {
  if (!switchesLauncher(s, o) || (list?.agent && agentId(list.agent) !== sessionEngine(s))) return null;
  const row = list?.models.find((m) => m.id === config.model);
  if (!row && !validModelId(config.model)) return null;
  const model = row?.default ? undefined : config.model;
  if (model && list?.locked) return null;
  const step: { launcher: string; from: string; model?: string; effort?: string } = { launcher: config.provider, from: sessionLauncher(s) };
  if (model) step.model = model;
  if (config.effort) step.effort = config.effort;
  const ps = s.pending_switch;
  if (ps && ps.launcher === step.launcher && (ps.model || undefined) === step.model && (ps.effort || undefined) === step.effort) return [];
  return [step];
}

/** The speed a setup asks for (Codex's service tier): only where the thread has a Fast speed, and (a pick) only when it
 *  changes. `always`: the flyout's switch, sent whatever the session says: the session learns a speed only from the push
 *  after the agent took it, so Fast on and straight back off must send the second tap too. */
export function speedStep(s: Session, fast: boolean | undefined, always = false): ModeStep | undefined {
  if (fast === undefined || !hasFast(s)) return undefined;
  const tier = fast ? "priority" : "default";
  return always || tier !== (s.service_tier ?? "default") ? { service_tier: tier } : undefined;
}
