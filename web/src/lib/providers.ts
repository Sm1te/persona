// Settings › Model providers (#2455, docs/integrations/launchers.md): the add / edit wizard's form for a compatible gateway typed by hand
// (cindy's AddProviderWizard: connect → models, the rest folded under "Advanced"; the hub's presets are lib/launcher-presets.ts), its checks, the POST / PUT body, and the import preview of a computer's aliases. Pure: the views
// in views/settings/Provider*.tsx only render it.
import { ApiError, errorText } from "./api";
import { hostOf as urlHost } from "./hubs";
import { displayNameProblem, launcherNameProblem, testableKey } from "./launcher-presets";
import type { ScopeView } from "./provider-scope";
import { TIER_SLOTS, type TierSource } from "./provider-tiers";
import { EFFORTS } from "./session-actions";
import { formatList, t } from "../i18n";

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
  /** The startup ID; left empty on a new provider, a hub with `launcher_auto_name` makes one (#5029). */
  name: string;
  /** What people see, any language; "" shows the startup ID. */
  display_name: string;
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
  /** Which tier models were filled in by themselves and which the user chose (lib/provider-tiers.ts); never sent. */
  tier_source: TierSource;
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
    display_name: "",
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
    tier_source: {},
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
    display_name: v.display_name ?? "",
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
    // a saved provider's tiers are its own: nothing fills them in (#4975)
    tier_source: Object.fromEntries(TIER_SLOTS.map((s) => [s, "user"])),
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

/** A model typed by hand, filled in from the hub's catalog (POST /api/v1/launchers/catalog, #4975): only what is still
 *  empty (its name, context window, thinking levels and default level), so what the user typed meanwhile stays. A Chat
 *  Completions provider takes no thinking levels. */
export function fillFromCatalog(models: FormModel[], found: ProviderModel[], chat = false): FormModel[] {
  let out = models;
  for (const m of found) {
    const i = out.findIndex((x) => x.id === m.id);
    if (i < 0) continue;
    const row = out[i];
    const patch: Partial<FormModel> = {};
    if (!row.name && m.name) patch.name = m.name;
    if (!row.context_window && m.context_window) patch.context_window = String(m.context_window);
    if (!chat && !row.efforts.length && m.efforts?.length) {
      patch.efforts = [...m.efforts];
      if (!row.default_effort && m.default_effort && m.efforts.includes(m.default_effort)) patch.default_effort = m.default_effort;
    }
    if (Object.keys(patch).length) out = out.map((x, j) => (j === i ? { ...x, ...patch } : x));
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

/** The hub keeps at most this many variables in a provider's environment (checkEnv); an import leaves the rest out as `limit`. */
const ENV_LIMIT = 50;

/** The hub's checkEnv (and the node's reading of it): NAME=value a line, "export " before it is fine, a line starting
 *  with # is a comment, at most ENV_LIMIT. */
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
    if (++count > ENV_LIMIT) return t("lib-providers.env_limit");
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
  /** The names, the address and the key: what keeps "Next" and "Save" from going on. */
  connect: string;
  /** Only the address and the key: what keeps "Test connection" and "Fetch models" from running. The names play no
   *  part in reaching the gateway, so they never block them (#4881). */
  reach: string;
  models: string;
  advanced: string;
}

/** `autoName`: the hub makes up a new provider's startup ID when none is typed (`launcher_auto_name`, #5029); an older
 *  hub needs one. */
export function formProblems(f: ProviderForm, isNew: boolean, hadKey = false, autoName = false): FormProblems {
  const requiresUrlAndKey = f.kind === "api" || !f.agent || f.agent === "claude";
  const reach =
    ((requiresUrlAndKey || f.base_url.trim() !== "") && gatewayProblem(f.base_url)) ||
    (requiresUrlAndKey && !f.api_key.trim() && (isNew || !hadKey) && t("lib-providers.key_required")) ||
    "";
  const connect =
    displayNameProblem(f.display_name) ||
    (!f.name.trim() && !autoName && t("lib-providers.name_required")) ||
    (isNew && launcherNameProblem(f.name)) ||
    reach;

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

/** What a provider is called when its display name is left empty, for the field's placeholder: its startup ID (typed,
 *  or a saved one's), else, when the hub makes the ID up, the address's host, which the hub names it after (#5029); ""
 *  while neither is known. */
export function fallbackTitle(f: ProviderForm, autoName: boolean): string {
  const id = f.name.trim();
  if (id || !autoName || gatewayProblem(f.base_url)) return id;
  // as the hub's gatewayHost reads it: no port, no IPv6 brackets, lower case
  return new URL(f.base_url.trim()).hostname.replace(/^\[|\]$/g, "").toLowerCase();
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

/** POST / PUT /api/v1/launchers' body. The base fields always go (the hub replaces them), the display name too ("" on a
 *  PUT clears it; on a POST with no startup ID the hub names it after the address, #5029); #2455's fields on a PUT only
 *  when they changed from `old` (left out keeps what is stored), the headers and the environment only when touched (""
 *  clears them), the key only when typed. */
export function providerBody(f: ProviderForm, old: ProviderView | null, clearLegacyChat = false): Record<string, unknown> {
  const api = f.kind === "api";
  const chat = api && f.api === "chat";
  const openAI = api && (f.api === "responses" || f.api === "chat");
  const body: Record<string, unknown> = {
    // empty: the hub makes the startup ID up (launcher_auto_name)
    name: f.name.trim(),
    display_name: f.display_name.trim(),
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

// ── Import from a computer's aliases ────────────────────────

/** Why an alias's variable is not carried into the new provider's environment (the hub's rules, docs/integrations/launchers.md). */
export type SkipReason = "credential" | "shell" | "computer" | "limit";
/** What hiding an alias after the import would do: `ok` hides it, `in_use` / `elsewhere` keep it (with why), `none` has nothing to hide. */
export type HideCheck = "ok" | "in_use" | "elsewhere" | "none";

/** One alias of an imported group: the file it came from and whether it can be hidden once imported. */
export interface ImportSource {
  alias: string;
  /** "~/.zshrc", "~/.bashrc", "~/.bash_aliases". */
  file: string;
  hide: HideCheck;
  /** in_use: plain names of what uses it (a purpose, a scheduled task, a profile). */
  uses?: string[];
  /** elsewhere: the computers where the new provider would not stand in for it. */
  elsewhere?: string[];
}

/** GET /api/v1/launchers/import's provider: the aliases on one URL and key (no key, no headers, no variable values). */
export interface ImportedProvider extends Omit<ProviderView, "origin"> {
  aliases: string[];
  existing?: string;
  /** One that will be created (no `existing`) under another name than `name`: an alias Persona already lists makes the hub
   *  save "<alias>-2". `name` stays what POST takes. */
  create_as?: string;
  source?: string;
  /** Names of the variables that go into the provider's environment, sorted. */
  env_names?: string[];
  /** The alias's variables that are left out, and why. */
  skipped_env?: { name: string; reason: SkipReason | string }[];
  /** One per alias, in `aliases` order (absent from older hubs). */
  sources?: ImportSource[];
  /** Settings other than the model its aliases set differently (tiers, effort, args, limits, proxy, headers, env, auth, custom_model). */
  differs?: string[];
  /** Two or more aliases: what each becomes as a provider of its own. */
  variants?: ImportedProvider[];
}

/** POST /api/v1/launchers/import's answer. */
export interface ImportResult {
  created?: string[];
  updated?: string[];
  hidden?: string[];
  kept?: Omit<ImportSource, "file">[];
}

export interface ImportRow {
  /** What the row is called: the name a group made one provider is saved under, else the group's own. */
  name: string;
  host: string;
  models: string;
  aliases: string;
  /** "Merge into xx", "New", or how many providers a split group makes. */
  action: string;
  merge: boolean;
}

export const hostOf = (u?: string) => (u ? urlHost(u) : "");
const names = (xs: string[]) => xs.join(t("lib-providers.alias_separator"));

/** The group asks "A provider for each alias" / "One provider": its aliases set more than the model differently. */
export const importCanSplit = (p: ImportedProvider) => !!p.differs?.length && (p.variants?.length ?? 0) > 1;

/** The providers a group becomes: its variants when split, else the group itself. */
export const importOutcome = (p: ImportedProvider, split: boolean) => (split && importCanSplit(p) ? p.variants! : [p]);

/** The name a provider of the import is saved under. */
export const importCreates = (p: ImportedProvider) => p.create_as || p.name;

export function importRow(p: ImportedProvider, split = false): ImportRow {
  const n = new Set([...(p.models ?? []).map((m) => m.id), ...(p.model ? [p.model] : [])]).size;
  const parts = split && importCanSplit(p);
  return {
    name: parts ? p.name : importCreates(p),
    host: hostOf(p.base_url) || t("lib-providers.official_host"),
    models: t("lib-providers.model_count", { count: n }),
    aliases: names(p.aliases ?? []),
    action: parts ? t("lib-providers.split_count", { count: p.variants!.length }) : p.existing ? t("lib-providers.merge_into", { name: p.existing }) : t("lib-providers.create"),
    merge: !parts && !!p.existing,
  };
}

/** Where the aliases came from, a line per file ("From a, b in ~/.zshrc"); an older hub names no file. */
export function importFrom(p: ImportedProvider): string[] {
  if (!p.sources?.length) return p.aliases?.length ? [t("lib-providers.from_aliases", { aliases: names(p.aliases) })] : [];
  const files = new Map<string, string[]>();
  for (const s of p.sources) files.set(s.file, [...(files.get(s.file) ?? []), s.alias]);
  return [...files].map(([file, aliases]) => t("lib-providers.from_file", { aliases: names(aliases), file }));
}

/** The variables the provider brings along, or "" when none. */
export const importBrings = (p: ImportedProvider) => (p.env_names?.length ? t("lib-providers.brings", { names: names(p.env_names) }) : "");

const SKIP_ORDER = ["credential", "shell", "computer", "limit"];

/** The variables left out, a line per reason in plain words; an unknown reason reads as the generic one. */
export function importLeftOut(p: ImportedProvider): string[] {
  const by = new Map<string, string[]>();
  for (const s of p.skipped_env ?? []) {
    const reason = SKIP_ORDER.includes(s.reason) ? s.reason : "other";
    by.set(reason, [...(by.get(reason) ?? []), s.name]);
  }
  return [...SKIP_ORDER, "other"].filter((r) => by.has(r)).map((r) => t(`lib-providers.left_out_${r}`, { names: names(by.get(r)!), max: ENV_LIMIT }));
}

const DIFFERS = ["tiers", "effort", "args", "limits", "proxy", "headers", "env", "auth", "custom_model"];

/** What the group's aliases set differently ("These aliases set the tier models and proxy differently."), or "". */
export function importDiffers(p: ImportedProvider): string {
  if (!p.differs?.length) return "";
  const what = p.differs.map((d) => (DIFFERS.includes(d) ? t(`lib-providers.differs_${d}`) : d));
  return t("lib-providers.differs", { what: formatList(what) });
}

/** The providers the picked groups become (importOutcome). */
const pickedOutcomes = (list: ImportedProvider[], picked: Set<string>, split: Set<string>) =>
  list.filter((p) => picked.has(p.name)).flatMap((p) => importOutcome(p, split.has(p.name)));

/** The sources the hide row speaks for: the picked groups', each as the provider it lands in (a split group's variants). */
export const importSources = (list: ImportedProvider[], picked: Set<string>, split: Set<string>): ImportSource[] =>
  pickedOutcomes(list, picked, split).flatMap((x) => x.sources ?? []);

/** Whether the import really merges into a provider that is there: a picked group that lands in one, or a variant of a
 *  split one that does. */
export const importMerges = (list: ImportedProvider[], picked: Set<string>, split: Set<string>) =>
  pickedOutcomes(list, picked, split).some((x) => !!x.existing);

/** How many providers the import makes or merges into: a split group counts each alias. */
export const importCount = (list: ImportedProvider[], picked: Set<string>, split: Set<string>) => pickedOutcomes(list, picked, split).length;

/** Why an alias stays when the imported ones are hidden. */
export function importStays(s: Omit<ImportSource, "file">): string {
  if (s.hide === "in_use" && s.uses?.length) return t("lib-providers.stays_in_use", { alias: s.alias, uses: formatList(s.uses) });
  if (s.hide === "elsewhere" && s.elsewhere?.length) return t("lib-providers.stays_elsewhere", { alias: s.alias, computers: formatList(s.elsewhere) });
  return t("lib-providers.stays", { alias: s.alias });
}

export const importSummary = (r: ImportResult) => {
  const counts = { created: r.created?.length ?? 0, updated: r.updated?.length ?? 0 };
  if (!r.hidden?.length && !r.kept?.length) return t("lib-providers.import_summary", counts);
  return t("lib-providers.import_summary_hide", { ...counts, hidden: r.hidden?.length ?? 0, kept: r.kept?.length ?? 0 });
};

/** What a failed import says: the two cases the hub names, otherwise its message. */
export function importErrorText(e: unknown): string {
  if (e instanceof ApiError && e.status === 409 && e.code === "node_outdated") return t("lib-providers.node_outdated");
  if (e instanceof ApiError && e.status === 503 && e.code === "offline") return t("lib-providers.offline");
  return errorText(e);
}
