// A launcher's models (#794): what its agent lists in its own /model menu (GET /machines/{mid}/launchers/{name}/models,
// node/launcher_models.go), read by New Agent's model panel (#5104), the chat's and the model settings' pickers; which
// launcher a picker asks, searching the list, the thinking efforts a pick allows, and what a start sends. A New Agent
// pick is for one start only: nothing about it is saved on the hub.
import { MANAGER_HEADER } from "./manager-choice";
import { t } from "../i18n";
import { api, ApiError, enc, pastHub } from "./api";
import { cleanModelId, validModelId } from "./model-id";
import type { ModelBinding } from "./model-config";
import { EFFORTS } from "./session-actions";
import { launcherTitle, type Named } from "./provider-names";
import type { Launcher } from "./types";

export interface AgentModel {
  /** What a start sends as `model` (Claude Code's aliases too: opus, sonnet…). */
  id: string;
  name: string;
  description?: string;
  /** The concrete model an alias stands for with this launcher's settings. */
  resolved?: string;
  /** What the agent runs when no model is given: picking it is the same as picking none. */
  default?: boolean;
  /** Thinking efforts it takes, weakest first; none: no effort setting. */
  efforts?: string[];
  default_effort?: string;
  /** An ID the user typed before that ran here, remembered on the hub for this computer and launcher (#1644): listed
   *  after the agent's own, most recent first, with no efforts (not known). It can be forgotten. */
  custom?: boolean;
}

export interface LauncherModels {
  /** What the launcher runs: claude, codex, traex, a runtime's agent. */
  agent: string;
  /** Empty: this agent has no list (no picker). */
  models: AgentModel[];
  /** Why a pick would not reach the agent (the launcher fixes its own --model); the start sends no model then. */
  locked?: string;
}

/** What "Model configuration" runs when a launcher picker is left alone (GET /machines/{mid}/launchers's `default`, #2455):
 *  `provider` is the launcher's name. */
export interface LauncherDefault {
  provider?: string;
  model?: string;
  effort?: string;
  /** Main chat only: cli or agentsdk. */
  runtime?: string;
}

/** One of "New agent"'s presets (#2225, docs/assistant/model-profiles.md "Agent presets"): a tier of the model profile in use
 *  whose launcher this computer starts. Picking it fills launcher, model and effort as picking them by hand would. */
export interface PickerTier {
  name: string;
  launcher: string;
  /** "" or absent: the launcher's default (the model settings' when the launcher is theirs). */
  model?: string;
  effort?: string;
  /** When to use it. */
  description?: string;
}

export interface MachineLaunchers {
  launchers: Launcher[];
  /** Absent from an older hub; null when the purpose is not a launcher use. */
  default?: LauncherDefault | null;
  /** "New agent" only: absent from an older hub, with no profile in use, or none whose launcher this computer has. */
  tiers?: PickerTier[];
  /** The profile the presets come from (they follow its switches). */
  tiers_profile?: { id: string; name: string };
}

/** The launchers on `mid` with the default of `purpose` (the hub's own when absent: "New agent"). */
export function machineLaunchers(mid: string, opts: { purpose?: string; signal?: AbortSignal } = {}): Promise<MachineLaunchers> {
  const q = opts.purpose ? `?purpose=${enc(opts.purpose)}` : "";
  return api<MachineLaunchers>(`/machines/${enc(mid)}/launchers${q}`, { signal: opts.signal });
}

/** A picker's first entry: "Default: <provider> · <model>" from the hub's default, `fallback` naming the launcher when the hub
 *  says nothing (an older hub). The provider is a startup ID; `names` titles it (#5029). */
export function defaultLabel(d: LauncherDefault | null | undefined, fallback = "", names?: readonly Named[]): string {
  const provider = d?.provider || fallback;
  const parts = [provider && launcherTitle(provider, names), d?.model && modelName({ id: d.model.split("/").pop() || d.model })].filter(Boolean);
  return parts.length ? t("lib-launcher-models.default_named", { name: parts.join(" · ") }) : t("lib-launcher-models.default");
}

/** What a launcher picked over the default is marked with: it applies to this start / conversation only. */
export const oneOff = () => t("lib-launcher-models.one_off");

// The hub gives a model list 70 s (assistant/models_launchers.go): the node may have to start the CLI to ask its /model.
const MODELS_HUB_MS = 70_000;

/** The list for `launcher` on `mid` (seconds when the node has to start the CLI); null from an older hub (no picker). */
export async function launcherModels(mid: string, launcher: string, opts: { refresh?: boolean; signal?: AbortSignal } = {}): Promise<LauncherModels | null> {
  try {
    const r = await api<LauncherModels>(`/machines/${enc(mid)}/launchers/${enc(launcher)}/models${opts.refresh ? "?refresh=1" : ""}`, { signal: opts.signal, timeout: pastHub(MODELS_HUB_MS) });
    return { ...r, models: r.models ?? [] };
  } catch (e) {
    if (e instanceof ApiError && e.status === 404) return null;
    throw e;
  }
}

/** "Model configuration"'s list for a launcher provider (`launcher.<name>`, GET /model-config/providers/{id}/models, #943): asked on a
 *  machine that has the launcher, which `machine` names. null: an older hub or a provider without a list (no picker). */
export async function providerModels(provider: string, opts: { manager?: string; refresh?: boolean; machine?: string; signal?: AbortSignal } = {}): Promise<(LauncherModels & { machine?: string }) | null> {
  // machine: the purpose's own (a task, Lark /agent run there), so the list is that machine's launcher.
  const query = new URLSearchParams();
  if (opts.refresh) query.set("refresh", "1");
  if (opts.machine) query.set("machine", opts.machine);
  try {
    const r = await api<LauncherModels & { machine?: string }>(`/model-config/providers/${enc(provider)}/models${query.size ? `?${query}` : ""}`, { signal: opts.signal, timeout: pastHub(MODELS_HUB_MS), headers: opts.manager ? { [MANAGER_HEADER]: opts.manager } : undefined });
    return { ...r, models: r.models ?? [] };
  } catch (e) {
    if (e instanceof ApiError && e.status === 404) return null;
    throw e;
  }
}

/** What is typed in the search, as an id to use when no model has exactly that id and the hub would take it (#1644,
 *  lib/model-id.ts): "Model configuration" and "New agent" take ids the list lacks. */
export function customModel(models: AgentModel[], query: string): string {
  const id = cleanModelId(query);
  return validModelId(id) && !models.some((m) => m.id === id) ? id : "";
}

/** The agent takes a model ID typed by hand (#1644): Claude Code's `--model` / `/model` and Codex's `--model` take any
 *  string; the hub remembers typed IDs for these two only. Other agents' lists are empty (no model row). */
export const takesTypedModel = (agent: string) => agent === "claude" || agent === "codex";

/** A typed ID can reach the agent: Claude Code or Codex, and the launcher doesn't pass its own --model. */
export const typedModelOffered = (agent: string, list: LauncherModels | null | undefined) => takesTypedModel(agent) && !list?.locked;

/** Forgets one model ID typed for `launcher` on `mid` (#1644, the list's "Forget"): the hub stops listing it. */
export function forgetCustomModel(mid: string, launcher: string, model: string): Promise<unknown> {
  return api(`/machines/${enc(mid)}/launchers/${enc(launcher)}/models/custom?model=${enc(model)}`, { method: "DELETE" });
}

/** `list` without the typed ID `id` (the optimistic Forget), and how to put it back where it was when the hub says no. */
export function withoutCustom(list: LauncherModels, id: string): { list: LauncherModels; restore: (now: LauncherModels) => LauncherModels } {
  const at = list.models.findIndex((m) => m.id === id && m.custom);
  if (at < 0) return { list, restore: (now) => now };
  const row = list.models[at];
  return {
    list: { ...list, models: list.models.filter((_, i) => i !== at) },
    restore: (now) => (now.models.some((m) => m.id === id) ? now : { ...now, models: [...now.models.slice(0, at), row, ...now.models.slice(at)] }),
  };
}

/** The "Model configuration" binding of "New agent" that still applies with the chosen launcher: the hub keeps its model and effort
 *  unless another launcher is picked (hub applyModel). Not following "Model configuration": nothing. */
export function followedBinding(chosen: string, effective: ModelBinding | null | undefined): ModelBinding {
  if (!effective?.provider || (chosen && `launcher.${chosen}` !== effective.provider)) return {};
  return effective;
}

/** The launcher whose list the picker shows: the chosen one, else the one "Model configuration" runs (`launcher.<name>`); "": none. */
export function pickerLauncher(chosen: string, followed: ModelBinding): string {
  if (chosen) return chosen;
  const p = followed.provider ?? "";
  return p.startsWith("launcher.") ? p.slice("launcher.".length) : "";
}

/** What the launcher runs: the list's answer, else what the launcher list says (Claude Code when it says nothing). */
export function launcherAgent(name: string, launchers: Launcher[], list?: LauncherModels | null): string {
  if (list?.agent) return list.agent;
  const l = launchers.find((x) => x.name === name);
  return l?.agent_type || l?.agent || "claude";
}

/** Claude Code and Antigravity start with a thinking effort and permission mode; Codex and the rest keep their own. */
export const takesEffort = (agent: string) => agent === "claude" || agent === "agy";

export function defaultModel(models: AgentModel[]): AgentModel | undefined {
  return models.find((m) => m.default);
}

/** Display only: explicit aliases win; raw GPT ids lose the prefix and separate their variant words. */
export function modelName(m: { id: string; name?: string }): string {
  const name = m.name?.trim() ? m.name : m.id;
  if (name.toLowerCase() !== m.id.toLowerCase()) return name;
  const gpt = /^gpt-([a-z0-9]+(?:\.[a-z0-9]+)*(?:-[a-z0-9]+(?:\.[a-z0-9]+)*)*)$/i.exec(name);
  if (!gpt) return name;
  return gpt[1].split("-").map((part, i) => i ? part[0].toUpperCase() + part.slice(1) : part).join(" ");
}

/** "Default"'s second line: what "Model configuration" sets, else the concrete model the default entry stands for. */
export function defaultDetail(models: AgentModel[], followed: ModelBinding): string {
  if (followed.model) return followed.model;
  const d = defaultModel(models);
  if (!d) return "";
  return d.resolved || (d.id === "default" ? "" : d.id);
}

/** A row's second line: the concrete model, else the id when it says more than the name. */
export function modelDetail(m: AgentModel): string {
  if (m.resolved && m.resolved !== m.name) return m.resolved;
  return m.id.toLowerCase() !== (m.name || m.id).toLowerCase() ? m.id : "";
}

/** Whether every word of `query` (any case) is in one of `fields`; an empty query matches. */
export function matches(fields: (string | undefined)[], query: string): boolean {
  const text = fields.filter(Boolean).join("\n").toLocaleLowerCase();
  return query.toLocaleLowerCase().split(/\s+/).filter(Boolean).every((word) => text.includes(word));
}

/** What the open list shows for `query` (name, id, resolved model, description; every word): "Default" first when it
 *  matches, then the rest in the agent's order. */
export function modelRows(models: AgentModel[], query: string, followed: ModelBinding = {}): { showDefault: boolean; rows: AgentModel[] } {
  const d = defaultModel(models);
  const showDefault = !query.trim() || matches([t("lib-launcher-models.default"), "default", defaultDetail(models, followed), d && modelName(d), d?.name, d?.id, d?.resolved, d?.description], query);
  const rows = models.filter((m) => m !== d && matches([modelName(m), m.name, m.id, m.resolved, m.description], query));
  return { showDefault, rows };
}

/** Every thinking effort Claude Code takes, weakest first: what a model nobody lists the efforts of is offered. */
export const ALL_EFFORTS = EFFORTS.map((e) => e.id);

/** The efforts the control offers: the picked model's, with none picked the one that runs ("Model configuration"'s, else the
 *  default entry's). No list to go by (loading, failed, older hub), or "Model configuration"'s model not in it under that id: all
 *  of them. */
export function effortChoices(list: LauncherModels | null | undefined, model: string, followed: ModelBinding = {}): string[] {
  if (!list?.models.length) return ALL_EFFORTS;
  const id = model || followed.model;
  const m = list.models.find((x) => x.id === id) ?? (id ? undefined : defaultModel(list.models));
  // a typed ID (listed as typed before, or not at all): what it takes is not known
  return m && !m.custom ? (m.efforts ?? []) : ALL_EFFORTS;
}

/** The effort kept when the choices change: one they no longer offer goes back to "Launcher default". */
export function fitEffort(effort: string, choices: string[]): string {
  return choices.includes(effort) ? effort : "";
}

/** What a start sends: the model only when it is not the default and the launcher lets it through, the effort only to
 *  Claude Code and only one the model takes. An ID the list lacks (typed, #1644) goes as it is to Claude Code and Codex,
 *  also when the list couldn't be read (`undefined`); an older hub's `null` (no picker) sends none. */
export function startPicks(o: { agent: string; list: LauncherModels | null | undefined; model: string; effort: string; followed?: ModelBinding }): { model?: string; effort?: string } {
  const out: { model?: string; effort?: string } = {};
  const m = o.list?.models.find((x) => x.id === o.model);
  if (m) {
    // the default entry goes too when the model settings' own model would run instead (a preset picked it, #2225; the
    // picker's "Default" row picks "")
    if ((!m.default || (o.followed?.model && o.followed.model !== m.id)) && !o.list?.locked) out.model = m.id;
  } else if (o.model && o.list !== null && typedModelOffered(o.agent, o.list) && validModelId(o.model)) {
    out.model = o.model;
  }
  if (o.effort && takesEffort(o.agent) && effortChoices(o.list, out.model ?? "", o.followed).includes(o.effort)) out.effort = o.effort;
  return out;
}

/** What of a preset a start with this launcher's list would not pass on: its model when the launcher fixes its own --model
 *  or takes no typed ID, its effort when the model doesn't take it. Empty: all of it (or the list is not in yet). */
export function tierLeftOut(tier: PickerTier, o: { agent: string; list: LauncherModels | null | undefined; followed?: ModelBinding }): string[] {
  const picks = startPicks({ agent: o.agent, list: o.list, model: tier.model ?? "", effort: tier.effort ?? "", followed: o.followed });
  const out: string[] = [];
  // the list's default is what runs with no model sent (startPicks sends it when the model settings would run another)
  if (tier.model && picks.model !== tier.model && !(o.list?.models.some((m) => m.id === tier.model && m.default) && !o.list?.locked)) out.push(tier.model);
  if (tier.effort && picks.effort !== tier.effort) out.push(tier.effort);
  return out;
}
