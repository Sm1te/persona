// Settings › Model providers › Add provider (docs/assistant/onboarding.md 4.3 / 5.4, docs/extensions/extensions.md "Model providers", iOS LauncherPresetModel):
// the providers the hub offers (GET /api/v1/launchers/presets): its built-in ones (plugin "builtin", in the request
// language's order) and the extensions'. A launcher added from one sends only the key, the preset and maybe a name or
// another of its models; the hub fills in the name, the address, the kind and the rest.
import { formatList, t } from "../i18n";
import { api, ApiError } from "./api";
import type { ProviderAPI, TestResult } from "./providers";

export interface PresetModel {
  id: string;
  name?: string;
}

export interface LauncherPreset {
  id: string; // <plugin>/<provider>
  plugin: string;
  name: string;
  description?: string;
  kind: "launcher" | "api";
  api?: ProviderAPI;
  base_url: string;
  model?: string;
  small_model?: string;
  models?: PresetModel[];
  key_hint?: string;
  models_error?: string;
  /** How the gateway takes the key (built-in presets). */
  auth?: "api_key" | "bearer";
  /** Where to open an account or get a key (built-in presets). */
  signup_url?: string;
  /** The name in the request's language ("Zhipu GLM"); `name` stays the plain one. */
  display_name?: string;
}

/** The hub's built-in presets' plugin. */
export const BUILTIN_PLUGIN = "builtin";

/** What a preset is called in the list. */
export const presetTitle = (p: LauncherPreset) => p.display_name?.trim() || p.name;

/** "Where do I get a key?"'s link: the preset's signup page when it is a web address, else "". */
export function signupHref(p: LauncherPreset): string {
  const raw = p.signup_url?.trim();
  if (!raw) return "";
  try {
    const u = new URL(raw);
    return u.protocol === "https:" || u.protocol === "http:" ? u.href : "";
  } catch {
    return "";
  }
}

/** The extensions' providers; none from a hub without the extension base (404). */
export async function loadPresets(): Promise<LauncherPreset[]> {
  try {
    return (await api<{ presets?: LauncherPreset[] }>("/launchers/presets")).presets ?? [];
  } catch (e) {
    if (e instanceof ApiError && e.status === 404) return [];
    throw e;
  }
}

/** The model a launcher gets when none is picked: the declared default, else the first listed. */
export const presetDefaultModel = (p: LauncherPreset) => p.model || p.models?.[0]?.id || "";

/** What can be picked: the listed models, with the default first when it is not among them. */
export function presetChoices(p: LauncherPreset): PresetModel[] {
  const listed = p.models ?? [];
  const d = presetDefaultModel(p);
  return !d || listed.some((m) => m.id === d) ? listed : [{ id: d }, ...listed];
}

export const presetModelLabel = (m: PresetModel) => m.name || m.id;

/** The host a preset talks to, "" when its address does not parse. */
export function presetHost(p: LauncherPreset): string {
  try {
    return new URL(p.base_url).host;
  } catch {
    return "";
  }
}

/** "Direct API · api.example.com", "Claude Code launcher · gateway.example.com" (in Chinese). */
export function presetSubtitle(p: LauncherPreset): string {
  const kind = p.kind === "api" ? t("lib-launcher-presets.kind_api") : t("lib-launcher-presets.kind_launcher");
  try {
    const host = new URL(p.base_url).host;
    return host ? `${kind} · ${host}` : kind;
  } catch {
    return kind;
  }
}

/** The startup IDs the hub keeps for itself (server `reservedLauncherNames`). */
const RESERVED_NAMES = ["claude", "presets", "test", "discover", "import", "health"];

/** What is wrong with a launcher's startup ID, the way the hub would put it; "" while empty or fine. */
export function launcherNameProblem(name: string): string {
  const n = name.trim();
  if (!n) return "";
  if (!/^[A-Za-z_][A-Za-z0-9_-]*$/.test(n)) return t("lib-launcher-presets.name_invalid");
  if (RESERVED_NAMES.includes(n)) return t("lib-launcher-presets.name_reserved", { name: n });
  return "";
}

/** What is wrong with a display name, as the hub checks it (`err_display_name`): at most 64 characters (code points),
 *  no line breaks or other control characters; "" when fine, empty included. */
export function displayNameProblem(name: string): string {
  const d = name.trim();
  return [...d].length > 64 || /\p{Cc}/u.test(d) ? t("lib-launcher-presets.display_name_invalid") : "";
}

/** The categories a provider added in the wizard takes over while they are still at their default (docs/assistant/onboarding.md 5.4). */
export const ASSIGN_PURPOSES = ["assistant", "agent.new"] as const;

/** Whether the main conversation and Agents can run on a provider added from p (docs/assistant/model-config.md): Claude Code
 *  needs a Messages endpoint, so a direct API on Responses or Chat Completions serves only the hub's own calls. An
 *  empty `api` is Messages. */
export const presetRunsClaude = (p: LauncherPreset) => p.kind !== "api" || !p.api || p.api === "messages";

/** The hub says the provider blocks this region: no key will work here, only another provider (#4482, #4526). */
export const regionBlocked = (r: TestResult | null | undefined) => !!r && !r.ok && r.code === "region_blocked";

/** Whether "the main conversation and new agents use it" starts on: not while Claude Code already answers the main
 *  conversation (4.3 B.3), and not for a key the provider's region block makes useless here (#4526). The user can
 *  still turn it on. */
export function assignByDefault(claudeAnswers: boolean, test: TestResult | null | undefined): boolean {
  return !claudeAnswers && !regionBlocked(test);
}

/** What the wizard asks for a preset: the key, and under "More options" the startup ID, the display name and the model. */
export interface PresetForm {
  name: string;
  display_name: string;
  api_key: string;
  model: string;
}

/** POST /api/v1/launchers' body: the startup ID and the display name only when typed (the hub names it after the
 *  provider), the model only when another one than the preset's default was picked, `assign` when asked. */
export function presetBody(p: LauncherPreset, f: PresetForm, assign = false) {
  const m = f.model.trim();
  const n = f.name.trim();
  const d = f.display_name.trim();
  return {
    ...(n ? { name: n } : {}),
    ...(d ? { display_name: d } : {}),
    api_key: f.api_key.trim(),
    preset: p.id,
    ...(m && m !== presetDefaultModel(p) ? { model: m } : {}),
    ...(assign ? { assign: [...ASSIGN_PURPOSES] } : {}),
  };
}

/** Whether the preset uses a protocol supported by the connection test. */
export function presetTestable(p: LauncherPreset): boolean {
  return !p.api || p.api === "messages" || p.api === "responses" || p.api === "chat";
}

/** POST /api/v1/launchers/test's body for a preset: the hub fills in its address and, without one here, its default model. */
export function presetTestBody(p: LauncherPreset, key: string, model: string) {
  const m = model.trim();
  return { preset: p.id, api_key: key.trim(), auth: p.auth ?? "api_key", ...(m ? { model: m } : {}) };
}

/** A key long enough to be worth testing on its own: a test costs a request, so not on every keystroke of a typed one. */
export const testableKey = (key: string) => key.trim().length >= 8;

/** POST /api/v1/launchers' answer with `assign` (older hubs leave assigned / kept out). */
export interface AddResult {
  launcher?: { name: string; display_name?: string };
  assigned?: string[];
  kept?: { purpose: string; reason: string }[];
}

const purposeLabel = (id: string) => (id === "assistant" ? t("lib-launcher-presets.purpose_assistant") : id === "agent.new" ? t("lib-launcher-presets.purpose_agent_new") : id);

function keptReason(reason: string): string {
  if (reason === "customized") return t("lib-launcher-presets.kept_customized");
  if (reason === "incompatible") return t("lib-launcher-presets.kept_incompatible");
  return t("lib-launcher-presets.kept_failed");
}

/** The wizard's last screen: what now runs on the new provider and what was left as it was, and why. */
export function assignSummary(r: AddResult, asked: boolean): { assigned: string; kept: string[] } {
  if (!asked || !r.assigned) return { assigned: "", kept: [] };
  return {
    assigned: r.assigned.length ? t("lib-launcher-presets.assigned", { purposes: formatList(r.assigned.map(purposeLabel)) }) : "",
    kept: (r.kept ?? []).map((k) => t("lib-launcher-presets.kept", { purpose: purposeLabel(k.purpose), reason: keptReason(k.reason) })),
  };
}
