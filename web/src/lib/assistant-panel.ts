// The model panel's data for a Persona chat (#5033, docs/web/web.md "Model pill and model panel"; docs/assistant/
// model-config.md "A conversation's own model"), and what a pick sends. The providers are what the assistant's computer
// lists for the main chat (`?purpose=assistant`): its rc-file aliases and PATH wrappers, plain claude and the Model
// providers that can run it (launchers and Messages-protocol direct APIs), each with its model list; then what can't run
// it there (Responses / Chat Completions direct APIs, providers out of the computer's scope or with their own sign-in…),
// listed off with the hub's reason rather than hidden. A chat that already runs keeps its engine (Claude Code can't
// resume a Codex thread, nor Codex a Claude transcript), so a provider of another engine is off there too, as in an
// Agent's panel; the new-conversation page runs nothing yet and offers every kind. Every pickable provider switches the
// same way, from the next message, so "Recommended" is the provider the chat runs on now and the others keep their own
// groups. Loaded with the panel, not with the composer.
import { t } from "../i18n";
import { agentId, agentName } from "./agent-kind";
import type { AssistantSetup } from "./assistant-models";
import { ALL_EFFORTS, defaultModel, modelName, typedModelOffered, type AgentModel, type LauncherModels } from "./launcher-models";
import { validModelId } from "./model-id";
import { cleanSetup, type PanelConfig, type PanelModel, type PanelProvider, type PanelState } from "./model-panel";
import { providerTitle } from "./provider-names";
import { currentModelRow, modelLabel, type AssistantLaunchers, type ModelList } from "./session-models";
import type { ModelPick } from "./types";

/** A model of a launcher's list as the panel shows it: an ID typed before takes any of Claude Code's efforts (nobody
 *  knows which it has). */
function panelModel(m: AgentModel, engine: string): PanelModel {
  const out: PanelModel = { id: m.id, name: modelName(m), efforts: m.efforts ?? (m.custom && engine === "claude" ? ALL_EFFORTS : []) };
  if (m.description) out.description = m.description;
  if (m.resolved) out.resolved = m.resolved;
  if (m.default_effort) out.defaultEffort = m.default_effort;
  if (m.custom) out.custom = true;
  return out;
}

export interface AssistantPanel {
  providers: PanelProvider[];
  state: PanelState;
}

export interface AssistantPanelOptions {
  /** The hub takes a model ID typed by hand for a Persona chat (`typed_models`). */
  typed?: boolean;
  /** The engine the chat runs, for a launcher the list doesn't have. */
  engine?: string;
  /** The chat already runs on `engine`: a provider of another engine can't take it over (the hub refuses it, 409
   *  `launcher_other_kind`), so it is listed off with why. Not set on the new-conversation page. */
  running?: boolean;
  /** The chat has Codex's Fast speed: the model it runs offers it ("as it runs": on when `fastOn`). */
  fast?: boolean;
  fastOn?: boolean;
}

/** The panel's providers and state for a Persona chat running `setup`: `info` the computer's launchers for the main chat
 *  (undefined before they are read), `listOf` each launcher's list as read. */
export function assistantPanel(setup: AssistantSetup, info: AssistantLaunchers | undefined, listOf: (launcher: string) => ModelList, o: AssistantPanelOptions = {}): AssistantPanel {
  const { typed = false, engine = "claude", running = false } = o;
  const runs = agentId(engine);
  const launchers = info?.launchers ?? [];
  const providers: PanelProvider[] = launchers.map((l) => {
    const kind = agentId(l.agent_type || l.agent || "claude");
    const e = listOf(l.name);
    const p: PanelProvider = { id: l.name, name: providerTitle(l), engine: kind, models: null };
    if (e.list === undefined) {
      if (e.error) p.error = e.error;
      else p.loading = true;
    } else {
      let models = (e.list?.models ?? []).map((m) => panelModel(m, kind));
      // a launcher that fixes its own --model takes no model with a pick: what it runs by default, only
      if (e.list?.locked) models = models.filter((m) => m.id === defaultModel(e.list?.models ?? [])?.id);
      // no list to read (a direct API the computer can't ask, an older hub): the model it is configured with
      if (!models.length && l.model) models = [{ id: l.model, name: modelLabel(l.model), efforts: kind === "claude" ? ALL_EFFORTS : [] }];
      p.models = models;
    }
    // the provider it runs on stays as it is, whatever its kind: it is what the panel says the chat runs
    if (running && kind !== runs && l.name !== setup.provider) p.unavailable = t("chat-modelpanel.other_engine", { from: agentName(runs), to: agentName(kind) });
    else if (typed && typedModelOffered(kind, e.list)) p.typable = true;
    return p;
  });
  // what the chat runs on, when the list doesn't have it (yet): plain claude is always there; another one is gone
  if (info && !providers.some((p) => p.id === setup.provider)) {
    const claude = setup.provider === "claude";
    const e = listOf(setup.provider);
    const models = e.list ? e.list.models.map((m) => panelModel(m, engine)) : setup.model ? [{ id: setup.model, name: modelLabel(setup.model), efforts: [] }] : [];
    providers.unshift({ id: setup.provider, name: setup.provider, engine, models: claude && !e.list ? null : models, ...(claude ? (e.list ? {} : { loading: true }) : { unavailable: t("chat-modelpill.provider_gone") }) });
  }
  for (const u of info?.unavailable ?? []) {
    if (providers.some((p) => p.id === u.name)) continue;
    providers.push({ id: u.name, name: providerTitle(u), engine: agentId(u.agent || "claude"), models: null, unavailable: u.reason });
  }

  const own = providers.find((p) => p.id === setup.provider);
  const row = own?.models ? currentModelRow(listOf(setup.provider).list?.models ?? [], setup.model) : undefined;
  const model = row?.id ?? setup.model ?? launchers.find((l) => l.name === setup.provider)?.model;
  if (o.fast && model && own?.models) own.models = own.models.map((m) => (m.id === model ? { ...m, fast: true } : m));
  const state: PanelState = {
    current: model ? cleanSetup({ provider: setup.provider, model, effort: setup.effort, fast: o.fast && o.fastOn }) : null,
    recommended: own && !own.unavailable ? [setup.provider] : [],
  };
  return { providers, state };
}

/** What a pick of `config` keeps for the chat: the launcher, the model unless it is the launcher's default entry, the
 *  effort when the setup has one. null: nothing to keep (an ID the hub would refuse, or the setup the chat runs now). */
export function assistantPickOf(config: PanelConfig, list: LauncherModels | null | undefined, current: PanelConfig | null): ModelPick | null {
  const row = list?.models.find((m) => m.id === config.model);
  if (!row && !validModelId(config.model)) return null;
  if (current && current.provider === config.provider && current.model === config.model && (current.effort || "") === (config.effort || "")) return null;
  const pick: ModelPick = { launcher: config.provider };
  if (!row?.default) pick.model = config.model;
  if (config.effort) pick.effort = config.effort;
  return pick;
}
