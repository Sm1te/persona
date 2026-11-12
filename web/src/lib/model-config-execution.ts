// Settings-only presentation stays out of the shared API module and first-screen catalog.
import { t } from "../i18n";
import { SUPERVISE_PURPOSE } from "./background-supervisor";
import { modelSummary, providerAgent, providerName, runtimeLabel, type ModelProvider, type ModelPurpose } from "./model-config";
import type { Machine } from "./types";

/** Older hubs omit launch; keep their known launcher purposes readable without using inheritance-group names. */
export function purposeLaunch(p: ModelPurpose): ModelPurpose["launch"] {
  if (p.kind !== "launcher") return undefined;
  if (p.launch) return p.launch;
  if (p.id === "inbox.agent" || p.id === "lark.agent") return "headless";
  return [p.id, p.parent].some((id) => id === "assistant" || id === "schedule" || id?.startsWith("task.")) ? "headless" : "engine";
}

function executionLabel(form: string): string {
  return form === "api" ? t("settings.direct_api") : form === "plugin" ? t("settings-nav.tab_extensions") : t(`settings-modelconfiguration.${form}`);
}

export function executionForm(p: ModelPurpose, providers: ModelProvider[] = [], machines: Machine[] = []): { label: string; backend: string } {
  const provider = providers.find((v) => v.id === p.effective?.provider);
  const launch = purposeLaunch(p);
  const agent = providerAgent(provider, p.effective?.provider);
  let backend: string;
  if (p.kind === "plugin") backend = providerName(p.effective?.provider, providers) ?? "";
  else if (p.kind === "api") backend = { messages: "Messages", responses: "Responses", chat: "Chat" }[provider?.api || "messages"];
  else if (launch === "headless") {
    backend = agent === "codex" ? "codex app-server"
      : agent === "claude" ? (p.effective?.runtime === "agentsdk" ? "Agent SDK" : "claude -p")
      : runtimeLabel("cli", agent);
  } else {
    const machine = p.machine_id ? machines.find((m) => m.id === p.machine_id) : undefined;
    backend = `${machine ? machine.engine_default || machine.engine || "herdr" : t("lib-agent-groups.by_machine")} · ${agent}`;
  }
  return { label: executionLabel(launch || p.kind), backend };
}

/** What a purpose is for, in a sentence under its row; only the ones this page has words for (the background
 *  supervisor's, #5082). */
export function purposeDescription(p: Pick<ModelPurpose, "id">): string {
  return p.id === SUPERVISE_PURPOSE ? t("settings-modelconfiguration.supervise_description") : "";
}

export function purposeGroups(purposes: ModelPurpose[], query: string, providers: ModelProvider[] = []): [string, ModelPurpose[]][] {
  const q = query.trim().toLocaleLowerCase();
  return [[t("sidebar.persona_chats"), "headless"], [t("shellswitch.agents"), "engine"], [t("settings-modelconfiguration.processing"), "api"], ["", "plugin"]].map(([name, key]): [string, ModelPurpose[]] => {
    const group = [name, executionLabel(key)].filter(Boolean).join(" · ");
    return [group, purposes.filter((p) =>
      (p.id.startsWith("extension.") || p.kind === "plugin" ? "plugin" : purposeLaunch(p) || "api") === key
      && (!q || [p.name, p.group, p.id, group, modelSummary(p, providers)].some((s) => s.toLocaleLowerCase().includes(q))))];
  }).filter(([, rows]) => rows.length);
}
