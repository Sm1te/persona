// Settings › Model providers (#2455, docs/integrations/launchers.md): the add / edit wizard's form for a compatible gateway typed by hand
// (cindy's AddProviderWizard: connect → models, the rest folded under "Advanced"; the hub's presets are lib/launcher-presets.ts), its checks, the POST / PUT body, and the "Import from ~/.zshrc" preview. Pure: the views
// in views/settings/Provider*.tsx only render it.
import { ApiError, errorText } from "./api";
import { hostOf as urlHost } from "./hubs";
import { launcherNameProblem, testableKey } from "./launcher-presets";
import type { ScopeView } from "./provider-scope";
import { EFFORTS } from "./session-actions";
import { t } from "../i18n";

export type ProviderAPI = "messages" | "responses" | "chat";

/** Claude Code's thinking levels (proto.EffortLevels), in order. */
export const EFFORT_LEVELS = EFFORTS.map((e) => e.id);

/** proto.ProviderModel. */
export interface ProviderModel {
  id: string;
  name?: string;
  context_window?: number;
  efforts?: string[];
  default_effort?: string;
}

/** GET /api/v1/launchers' managed launcher (hub LauncherView): never the key, the headers or the environment, only
 *  has_key / has_headers / has_env; the device scope's fields (lib/provider-scope.ts) when the hub has them. */
export interface ProviderView extends ScopeView {
  name: string;
  /** The name it shows, in any language (one added from a preset gets the provider's). */
  display_name?: string;
  kind?: "launcher" | "api";
  agent?: "claude" | "codex" | "agy" | "omp" | string;
  api?: ProviderAPI;
  base_url?: string;
  model?: string;
  small_model?: string;
  subagent_model?: string;
  custom_model?: string;
  custom_model_name?: string;
  custom_model_description?: string;
  args?: string[];
  has_key?: boolean;
  auth?: "api_key" | "bearer";
  models?: ProviderModel[];
  opus_model?: string;
  sonnet_model?: string;
  haiku_model?: string;
  fable_model?: string;
  subagent_force?: boolean;
  has_headers?: boolean;
  has_env?: boolean;
  proxy?: string;
  no_proxy?: string;
  timeout_ms?: number;
  compact_window?: number;
  max_context?: number;
  effort?: string;
  /** ManagedLauncher.Source: "" made by hand, "zshrc:<alias>@<machine>", "preset:<id>". */
  origin?: string;
}

/** One model row of step 3: numbers as typed. */
export interface FormModel {
  id: string;
  name: string;
  context_window: string;
  efforts: string[];
  default_effort: string;
  checked: boolean;
}

export interface ProviderForm {
  kind: "launcher" | "api";
  agent: "claude" | "codex" | "agy" | "omp";
  api: ProviderAPI;
  name: string;
  base_url: string;
  auth: "bearer" | "api_key";
  api_key: string;
  models: FormModel[];
  /** The default model: one of the checked models. */
  model: string;
  small_model: string;
  subagent_model: string;
  subagent_force: boolean;
  opus_model: string;
  sonnet_model: string;
  haiku_model: string;
  fable_model: string;
  /** "Name: value" a line; only sent when touched (a stored one is never shown). */
  headers: string;
  headers_touched: boolean;
  /** The provider's own environment, NAME=value a line, set last (#5953); like the headers only sent when touched. */
  env: string;
  env_touched: boolean;
  proxy: string;
  no_proxy: string;
  timeout_ms: string;
  compact_window: string;
  max_context: string;
  effort: string;
  custom_model: string;
  custom_model_name: string;
  custom_model_description: string;
  args: string;
}

/** A compatible gateway's form, typed by hand (the providers the hub knows are presets, lib/launcher-presets.ts). */
export function newForm(): ProviderForm {
  return {
    kind: "launcher",
    agent: "claude",
    api: "messages",
    name: "",
    base_url: "",
    // gateways mostly want Bearer (ANTHROPIC_AUTH_TOKEN)
    auth: "bearer",
    api_key: "",
    models: [],
    model: "",
    small_model: "",
    subagent_model: "",
    subagent_force: false,
    opus_model: "",
    sonnet_model: "",
    haiku_model: "",
    fable_model: "",
    headers: "",
    headers_touched: false,
    env: "",
    env_touched: false,
    proxy: "",
    no_proxy: "",
    timeout_ms: "",
    compact_window: "",
    max_context: "",
    effort: "",
    custom_model: "",
    custom_model_name: "",
    custom_model_description: "",
    args: "",
  };
}

const numText = (n?: number) => (n ? String(n) : "");

export function formModel(m: ProviderModel, checked = true): FormModel {
  return { id: m.id, name: m.name ?? "", context_window: numText(m.context_window), efforts: [...(m.efforts ?? [])], default_effort: m.default_effort ?? "", checked };
}

/** How a saved provider signs in: what it stores, else x-api-key (providers saved before the choice existed). */
export const storedAuth = (v: ProviderView): ProviderForm["auth"] => v.auth ?? "api_key";

/** The form an existing provider opens with: its models checked, the default among them even when not listed. */
export function formFromView(v: ProviderView): ProviderForm {
  const models = (v.models ?? []).map((m) => formModel(m));
  if (v.model && !models.some((m) => m.id === v.model)) models.unshift(formModel({ id: v.model }));
  return {
    ...newForm(),
    kind: v.kind ?? "launcher",
    agent: (v.agent as ProviderForm["agent"]) || "claude",
    api: v.api ?? "messages",
    name: v.name,
    base_url: v.base_url ?? "",
    auth: storedAuth(v),
    models,
    model: v.model ?? "",
    small_model: v.small_model ?? "",
    subagent_model: v.subagent_model ?? "",
    subagent_force: !!v.subagent_force,
    opus_model: v.opus_model ?? "",
    sonnet_model: v.sonnet_model ?? "",
    haiku_model: v.haiku_model ?? "",
    fable_model: v.fable_model ?? "",
    proxy: v.proxy ?? "",
    no_proxy: v.no_proxy ?? "",
    timeout_ms: numText(v.timeout_ms),
    compact_window: numText(v.compact_window),
    max_context: numText(v.max_context),
    effort: v.effort ?? "",
    custom_model: v.custom_model ?? "",
    custom_model_name: v.custom_model_name ?? "",
    custom_model_description: v.custom_model_description ?? "",
    args: (v.args ?? []).join(" "),
  };
}

/** Up to this many discovered models come checked when there were none yet; a longer list is picked by hand. */
export const AUTO_CHECK = 8;

/** Discovered models merged in: new ones added unchecked (all checked when nothing was there and the list is short),
 *  what was typed kept. */
export function mergeDiscovered(models: FormModel[], found: ProviderModel[], chat = false): FormModel[] {
  const out = [...models];
  const first = models.length === 0 && found.length <= AUTO_CHECK;
  for (const m of found) {
    const i = out.findIndex((x) => x.id === m.id);
    if (i < 0) {
      const mismatch = chat && m.default_effort && m.efforts?.length && !m.efforts.includes(m.default_effort);
      out.push(formModel(mismatch ? { ...m, efforts: [], default_effort: "" } : m, first));
    } else if (!out[i].context_window && m.context_window) {
      out[i] = { ...out[i], context_window: String(m.context_window), ...(!chat && !out[i].efforts.length ? { efforts: [...(m.efforts ?? [])] } : {}) };
    }
  }
  return out;
}

const nonNegInt = (s: string) => s.trim() === "" || /^\d+$/.test(s.trim());

export function gatewayProblem(raw: string): string {
  const s = raw.trim();
  if (!s) return t("lib-providers.url_required");
  let u: URL;
  try {
    u = new URL(s);
  } catch {
    return t("lib-providers.url_scheme");
  }
  if ((u.protocol !== "http:" && u.protocol !== "https:") || !u.host) return t("lib-providers.url_scheme");
  if (u.username || u.password || u.search || u.hash) return t("lib-providers.url_extras");
  return "";
}

export function headersProblem(raw: string): string {
  const lines = raw.split("\n");
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i].trim();
    if (!line) continue;
    const at = line.indexOf(":");
    const name = at < 0 ? "" : line.slice(0, at).trim();
    if (!name || /\s/.test(name)) return t("lib-providers.header_line", { line: i + 1 });
  }
  return "";
}

/** The hub's checkEnv (and the node's reading of it): NAME=value a line, "export " before it is fine, a line starting
 *  with # is a comment, at most 50. */
export function envProblem(raw: string): string {
  const lines = raw.split("\n");
  let count = 0;
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i].replace(/^[\s\uFEFF]+|[\s\uFEFF]+$/g, "");
    if (!line || line.startsWith("#")) continue;
    const rest = line.replace(/^export\s+/, "");
    const at = rest.indexOf("=");
    const name = at < 0 ? "" : rest.slice(0, at).trim();
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(name)) return t("lib-providers.env_line", { line: i + 1 });
    if (++count > 50) return t("lib-providers.env_limit");
  }
  return "";
}

export function proxyProblem(raw: string): string {
  const s = raw.trim();
  if (!s) return "";
  try {
    const u = new URL(s);
    if (["http:", "https:", "socks5:", "socks5h:"].includes(u.protocol) && u.host) return "";
  } catch {
    /* falls through */
  }
  return t("lib-providers.proxy_scheme");
}

/** What keeps each step from going on ("" when it can): the same rules the hub checks (launchers.go), said earlier. */
export interface FormProblems {
  /** The name, the address and the key: what keeps "Next" and "Save" from going on. */
  connect: string;
  /** Only the address and the key: what keeps "Test connection" and "Fetch models" from running. The name plays no part
   *  in reaching the gateway, so it never blocks them (#4881). */
  reach: string;
  models: string;
  advanced: string;
}

export function formProblems(f: ProviderForm, isNew: boolean, hadKey = false): FormProblems {
  const requiresUrlAndKey = f.kind === "api" || !f.agent || f.agent === "claude";
  const reach =
    ((requiresUrlAndKey || f.base_url.trim() !== "") && gatewayProblem(f.base_url)) ||
    (requiresUrlAndKey && !f.api_key.trim() && (isNew || !hadKey) && t("lib-providers.key_required")) ||
    "";
  const connect = (!f.name.trim() && t("lib-providers.name_required")) || (isNew && launcherNameProblem(f.name)) || reach;

  const chat = f.kind === "api" && f.api === "chat";
  let models = "";
  const checked = f.models.filter((m) => m.checked);
  const ids = new Set<string>();
  for (const m of checked) {
    const id = m.id.trim();
    if (!id) models = t("lib-providers.model_no_id");
    else if (ids.has(id)) models = t("lib-providers.model_duplicate", { id });
    else if (!nonNegInt(m.context_window)) models = t("lib-providers.model_context_int", { id });
    else if (m.default_effort && m.efforts.length > 0 && !m.efforts.includes(m.default_effort)) models = t("lib-providers.model_effort_unsupported", { id });
    if (models) break;
    ids.add(id);
  }
  if (!models && !f.model.trim()) models = checked.length ? t("lib-providers.pick_default") : t("lib-providers.add_model");
  else if (!models && !ids.has(f.model.trim())) models = t("lib-providers.default_unchecked");

  const advanced =
    (!chat && f.headers_touched && headersProblem(f.headers)) ||
    (!chat && f.env_touched && envProblem(f.env)) ||
    (!chat && proxyProblem(f.proxy)) ||
    (!chat && !nonNegInt(f.timeout_ms) && t("lib-providers.timeout_int")) ||
    (!chat && !nonNegInt(f.compact_window) && t("lib-providers.compact_int")) ||
    (!chat && !nonNegInt(f.max_context) && t("lib-providers.max_context_int")) ||
    (f.kind !== "api" && f.args.split(/\s+/).some((a) => a && !a.startsWith("-")) && t("lib-providers.args_dash")) ||
    "";
  return { connect, reach, models, advanced };
}

/** Whether the Connect step checks the connection by itself (fetches the models, then tests one): a valid address and a
 *  key that looks whole (typed, or the stored one when editing). A saved provider is checked only once its address, key
 *  or sign-in method differs from what is stored, so opening it sends nothing (#4881). */
export function autoCheckable(f: ProviderForm, old: ProviderView | null): boolean {
  if (!f.base_url.trim() || gatewayProblem(f.base_url)) return false;
  const key = f.api_key.trim();
  if (key && !testableKey(key)) return false;
  if (!old) return !!key;
  if (!key && !old.has_key) return false;
  return !!key || f.base_url.trim() !== (old.base_url ?? "").trim() || f.auth !== storedAuth(old);
}

const int = (s: string) => (s.trim() ? parseInt(s.trim(), 10) : 0);

function modelsOf(f: ProviderForm): ProviderModel[] {
  return f.models
    .filter((m) => m.checked && m.id.trim())
    .map((m) => {
      const out: ProviderModel = { id: m.id.trim() };
      if (m.name.trim()) out.name = m.name.trim();
      if (int(m.context_window)) out.context_window = int(m.context_window);
      if (m.efforts.length) out.efforts = [...m.efforts];
      if (m.default_effort) out.default_effort = m.default_effort;
      return out;
    });
}

const sameJSON = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b);

/** #2455's fields as the hub takes them (the pointer ones: left out keeps what is stored). */
function extraFields(f: ProviderForm): Record<string, unknown> {
  return {
    models: modelsOf(f),
    opus_model: f.opus_model.trim(),
    sonnet_model: f.sonnet_model.trim(),
    haiku_model: f.haiku_model.trim(),
    fable_model: f.fable_model.trim(),
    subagent_force: f.subagent_force,
    proxy: f.proxy.trim(),
    no_proxy: f.no_proxy.trim(),
    timeout_ms: int(f.timeout_ms),
    compact_window: int(f.compact_window),
    max_context: int(f.max_context),
    effort: f.effort,
  };
}

/** Explicit clears for settings an older Chat provider may have stored but cannot use. */
export function legacyChatClears(v: ProviderView | null): Record<string, string | number | boolean> {
  if (!v || v.kind !== "api" || v.api !== "chat") return {};
  const clears: Record<string, string | number | boolean> = {};
  if (v.has_headers) clears.headers = "";
  if (v.has_env) clears.env = "";
  for (const key of ["proxy", "no_proxy", "effort", "opus_model", "sonnet_model", "haiku_model", "fable_model"] as const) {
    if (v[key]) clears[key] = "";
  }
  for (const key of ["timeout_ms", "compact_window", "max_context"] as const) {
    if (v[key]) clears[key] = 0;
  }
  if (v.subagent_force) clears.subagent_force = false;
  return clears;
}

/** POST / PUT /api/v1/launchers' body. The base fields always go (the hub replaces them); #2455's fields on a PUT only
 *  when they changed from `old` (left out keeps what is stored), the headers and the environment only when touched (""
 *  clears them), the key only when typed. */
export function providerBody(f: ProviderForm, old: ProviderView | null, clearLegacyChat = false): Record<string, unknown> {
  const api = f.kind === "api";
  const chat = api && f.api === "chat";
  const openAI = api && (f.api === "responses" || f.api === "chat");
  const body: Record<string, unknown> = {
    name: f.name.trim(),
    kind: f.kind,
    ...(f.kind !== "api" && f.agent && f.agent !== "claude" ? { agent: f.agent } : {}),
    api: api ? f.api : "messages",
    base_url: f.base_url.trim(),
    model: f.model.trim(),
    small_model: api ? "" : f.small_model.trim(),
    subagent_model: api ? "" : f.subagent_model.trim(),
    custom_model: api ? "" : f.custom_model.trim(),
    custom_model_name: api ? "" : f.custom_model_name.trim(),
    custom_model_description: api ? "" : f.custom_model_description.trim(),
    args: api ? [] : f.args.split(/\s+/).filter(Boolean),
    auth: openAI ? "api_key" : f.auth,
  };
  if (f.api_key.trim()) body.api_key = f.api_key.trim();
  const extra = extraFields(f);
  const before = old && extraFields(formFromView(old));
  for (const [k, v] of Object.entries(extra)) {
    if (chat && ["proxy", "no_proxy", "timeout_ms", "compact_window", "max_context", "effort", "opus_model", "sonnet_model", "haiku_model", "fable_model", "subagent_force"].includes(k)) continue;
    if (before ? !sameJSON(before[k], v) : v !== "" && v !== 0 && v !== false && !(Array.isArray(v) && v.length === 0)) body[k] = v;
  }
  if (!chat && f.headers_touched) body.headers = f.headers.trim();
  if (!chat && f.env_touched) body.env = f.env.replace(/\s+$/, ""); // the start kept: the hub's line numbers match the box
  if (chat && clearLegacyChat) Object.assign(body, legacyChatClears(old));
  return body;
}

/** POST /api/v1/launchers/test and /discover's body: by name when editing (the stored key and headers), what is typed over it. */
export function probeBody(f: ProviderForm, stored: string | null, model?: string): Record<string, unknown> {
  const b: Record<string, unknown> = { base_url: f.base_url.trim(), auth: f.auth, api: f.api };
  if (stored) b.name = stored;
  if (f.api_key.trim()) b.api_key = f.api_key.trim();
  if (!(f.kind === "api" && f.api === "chat") && f.headers_touched) b.headers = f.headers.trim();
  if (model !== undefined) b.model = model;
  return b;
}

export interface TestResult {
  ok: boolean;
  ms: number;
  model?: string;
  error?: string;
  /** What the hub recognised (`region_blocked`: the provider does not serve this region, #4482); the text is `error`. */
  code?: string;
}

export const testText = (r: TestResult) => (r.ok ? t("lib-providers.test_ok", { ms: r.ms }) : r.error || t("lib-providers.test_failed"));

/** The model "Test connection" asks with: the default, else the first checked, else the first listed. */
export const testModel = (f: ProviderForm) => f.model.trim() || f.models.find((m) => m.checked)?.id || f.models[0]?.id || "";

// ── Import from ~/.zshrc ────────────────────────────────────

/** GET /api/v1/launchers/import's provider (no key, no headers). */
export interface ImportedProvider extends Omit<ProviderView, "origin"> {
  aliases: string[];
  existing?: string;
  source?: string;
}

export interface ImportRow {
  name: string;
  host: string;
  models: string;
  aliases: string;
  /** "Merge into xx" or "New". */
  action: string;
  merge: boolean;
}

export const hostOf = (u?: string) => (u ? urlHost(u) : "");

export function importRow(p: ImportedProvider): ImportRow {
  const n = new Set([...(p.models ?? []).map((m) => m.id), ...(p.model ? [p.model] : [])]).size;
  return {
    name: p.name,
    host: hostOf(p.base_url) || t("lib-providers.official_host"),
    models: t("lib-providers.model_count", { count: n }),
    aliases: (p.aliases ?? []).join(t("lib-providers.alias_separator")),
    action: p.existing ? t("lib-providers.merge_into", { name: p.existing }) : t("lib-providers.create"),
    merge: !!p.existing,
  };
}

export const importSummary = (r: { created?: string[]; updated?: string[] }) =>
  t("lib-providers.import_summary", { created: r.created?.length ?? 0, updated: r.updated?.length ?? 0 });

/** What a failed import says: the two cases the hub names, otherwise its message. */
export function importErrorText(e: unknown): string {
  if (e instanceof ApiError && e.status === 409 && e.code === "node_outdated") return t("lib-providers.node_outdated");
  if (e instanceof ApiError && e.status === 503 && e.code === "offline") return t("lib-providers.offline");
  return errorText(e);
}
