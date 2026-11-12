// Settings › Model providers › Add (#5193): the wizard's two other ways to start. "Paste a configuration" sends the text
// to POST /api/v1/launchers/import/parse, which reads a shell alias / export / .env, Claude Code's settings.json, a curl
// command or a cindy v1 provider JSON into drafts (a POST /launchers body each, the stored providers on the same address,
// and notes about what it could not use); a draft prefills the wizard, or opens a stored provider's edit wizard with its
// models or its key. "Found on your computers" lists the rc-file aliases that reach a gateway and are not in Persona yet;
// one opens the import preview (#5057) for that alias alone. Pure: views/settings/ProviderPaste.tsx and
// ProviderWizard.tsx render it.
import { managedProvider } from "./compatibility";
import { byReason, formFromView, formModel, names, newForm, type ProviderForm, type ProviderModel, type ProviderView, type SkipReason } from "./providers";
import type { ListedLauncher } from "./launcher-list";
import type { TierSource } from "./provider-tiers";
import type { Machine } from "./types";
import { t } from "../i18n";

/** The hub's hello feature for the parse route and the import's `alias`; without it neither new entry shows. */
export const PASTE_FEATURE = "launcher_import_parse";

/** What the parse route read the text as; "" when nothing in it was recognized. */
export type PasteFormat = "shell" | "settings" | "curl" | "cindy" | "";

/** Something the hub could not use, by code (the client says it in words): `line` is 1-based in the pasted text, `name`
 *  an alias, variable, header, option, protocol or kind, never a value. */
export interface PasteNote {
  code: string;
  line?: number;
  name?: string;
}

/** A stored provider on the draft's address; `same_key` when its key is the pasted one. */
export interface PasteMatch {
  name: string;
  display_name?: string;
  same_key: boolean;
}

/** A draft's provider: a POST /api/v1/launchers body, empty fields left out. */
export type PasteProvider = Omit<ProviderView, "name" | "has_key" | "has_headers" | "has_env" | "origin"> & {
  /** The suggested startup ID (shell aliases only). */
  name?: string;
  api_key?: string;
  /** "Name: value" a line. */
  headers?: string;
  /** NAME=value a line. */
  env?: string;
};

export interface PasteDraft {
  /** What it came from: the aliases' names, a cindy provider's name; absent otherwise. */
  label?: string;
  aliases?: string[];
  provider: PasteProvider;
  env_names?: string[];
  skipped_env?: { name: string; reason: SkipReason | string }[];
  skipped_headers?: { name: string; reason: "credential" | "protocol" | "unsupported" | string }[];
  /** Same key first. */
  matches?: PasteMatch[];
  notes?: PasteNote[];
}

export interface PasteResult {
  format: PasteFormat;
  drafts: PasteDraft[];
  /** About the text as a whole, in its order. */
  notes: PasteNote[];
}

/** What Continue does with the chosen draft: a new provider, or a stored one opened with its models or its key. */
export type PasteAction = { type: "new" } | { type: "merge" | "key"; provider: string };

/** What a stored provider's edit wizard opens with (Settings passes it on): the models to add, at "Choose models"; or
 *  the key to put in, at "Connect". Nothing is saved until Save. */
export type EditDraft = { step: "models"; models: ProviderModel[] } | { step: "connect"; api_key: string };

/** The paste view's state, kept by the wizard while it is open so coming back from "Connect" finds it as it was. */
export interface PasteState {
  text: string;
  /** The hub's answer and the text it was for. */
  result: { text: string; answer: PasteResult } | null;
  /** The chosen draft's index, -1 for none. */
  draft: number;
  /** What the user picked Continue to do; null: the chosen draft's default (defaultAction). */
  action: PasteAction | null;
}

export const newPaste = (): PasteState => ({ text: "", result: null, draft: -1, action: null });

/** The draft and action Continue takes: none until a draft is chosen for the text as it is now. */
export function pasteChoice(s: PasteState): { draft: PasteDraft; action: PasteAction } | null {
  const d = s.result?.text === s.text ? s.result.answer.drafts[s.draft] : undefined;
  return d ? { draft: d, action: s.action ?? defaultAction(d) } : null;
}

/** The state with the hub's answer for `text` (dropped when the text changed since): one draft is chosen by itself,
 *  several wait for the user. */
export function answered(s: PasteState, text: string, r: PasteResult): PasteState {
  if (s.text !== text) return s;
  const answer = { format: r.format ?? "", drafts: r.drafts ?? [], notes: r.notes ?? [] };
  return { ...s, result: { text, answer }, draft: answer.drafts.length === 1 ? 0 : -1, action: null };
}

/** The draft's models with its default among them: what a new provider lists, and what merging adds. */
export function draftModels(d: PasteDraft): ProviderModel[] {
  const p = d.provider;
  const models = p.models ?? [];
  return p.model && !models.some((m) => m.id === p.model) ? [{ id: p.model }, ...models] : models;
}

const filled = (s: string) => s.trim() !== "";

/** The wizard's form for a new provider from a draft: its connection, models (checked), tiers and the rest. Tiers the
 *  draft sets are the user's, so the automatic ones (#4975) only fill in the others; the Haiku tier set counts for the
 *  light one, which would otherwise put the background requests on another model. The headers and environment it
 *  brings are sent. */
export function draftForm(d: PasteDraft): ProviderForm {
  const p = d.provider;
  const f = formFromView({ ...p, name: p.name ?? "" });
  const tier_source: TierSource = {};
  if (filled(f.opus_model)) tier_source.opus = "user";
  if (filled(f.sonnet_model)) tier_source.sonnet = "user";
  if (filled(f.small_model) || filled(f.haiku_model)) tier_source.light = "user";
  if (filled(f.fable_model)) tier_source.fable = "user";
  return {
    ...f,
    model: f.model || f.models[0]?.id || "",
    // a stored provider's sign-in defaults to x-api-key (formFromView); a draft without one takes a new form's
    auth: p.auth ?? newForm().auth,
    api_key: p.api_key ?? "",
    tier_source,
    headers: p.headers ?? "",
    headers_touched: filled(p.headers ?? ""),
    env: p.env ?? "",
    env_touched: filled(p.env ?? ""),
  };
}

/** Continue on a stored provider: merging opens it at "Choose models" with the draft's models, replacing the key at
 *  "Connect" with the pasted one. */
export function editDraft(d: PasteDraft, type: "merge" | "key"): EditDraft {
  return type === "merge" ? { step: "models", models: draftModels(d) } : { step: "connect", api_key: d.provider.api_key ?? "" };
}

/** A stored provider's form with what it was opened with: the models it does not have yet, checked, or the key. */
export function withEditDraft(f: ProviderForm, d: EditDraft): ProviderForm {
  if (d.step === "connect") return { ...f, api_key: d.api_key };
  const added = d.models.filter((m, i) => !f.models.some((x) => x.id === m.id) && d.models.findIndex((x) => x.id === m.id) === i);
  return added.length ? { ...f, models: [...f.models, ...added.map((m) => formModel(m))] } : f;
}

/** Merging into a stored provider when it has the pasted key, else a new one. */
export function defaultAction(d: PasteDraft): PasteAction {
  const same = d.matches?.find((m) => m.same_key);
  return same ? { type: "merge", provider: same.name } : { type: "new" };
}

/** The choices for a draft on an address that is stored already: per stored provider, adding the models (same key) or
 *  replacing its key (another one), then a new provider. A draft without a key can only add its models: there is no key
 *  to put in. */
export function pasteActions(d: PasteDraft): (PasteAction & { match?: PasteMatch })[] {
  const keyless = !d.provider.api_key;
  return [
    ...(d.matches ?? []).map((m) => ({ type: m.same_key || keyless ? ("merge" as const) : ("key" as const), provider: m.name, match: m })),
    { type: "new" as const },
  ];
}

export const sameAction = (a: PasteAction | null, b: PasteAction) => !!a && a.type === b.type && (a.type === "new" || (b.type !== "new" && a.provider === b.provider));

const HEADER_REASONS = ["credential", "protocol", "unsupported"];

/** The headers a draft sends and leaves out, a line each (as the import says its variables, lib/providers.ts). */
export function headerLines(d: PasteDraft): string[] {
  const kept = (d.provider.headers ?? "").split("\n").filter((l) => l.includes(":")).map((l) => l.slice(0, l.indexOf(":")).trim()).filter(Boolean);
  return [
    ...(kept.length ? [t("lib-provider-paste.headers_kept", { names: names(kept), count: kept.length })] : []),
    ...byReason(d.skipped_headers ?? [], HEADER_REASONS).map(([r, list]) => t(`lib-provider-paste.headers_left_${r}`, { names: names(list), count: list.length })),
  ];
}

/** The most drafts the hub reads from one text (too_many). */
const MAX_DRAFTS = 50;

/** A note in plain words; a code from a newer hub reads as the generic one. */
export function noteText(n: PasteNote): string {
  const { line, name } = n;
  switch (n.code) {
    case "unrecognized":
      return t("lib-provider-paste.unrecognized");
    case "invalid_json":
      return t("lib-provider-paste.invalid_json");
    case "line_unused":
      if (line) return t("lib-provider-paste.line_unused", { line });
      break;
    case "not_claude":
      return name ? t("lib-provider-paste.not_claude_alias", { name }) : line ? t("lib-provider-paste.not_claude_line", { line }) : t("lib-provider-paste.not_claude");
    case "no_gateway":
      return name ? t("lib-provider-paste.no_gateway_alias", { name }) : line ? t("lib-provider-paste.no_gateway_line", { line }) : t("lib-provider-paste.no_gateway");
    case "url_shell":
      return name ? t("lib-provider-paste.url_shell_alias", { name }) : line ? t("lib-provider-paste.url_shell_line", { line }) : t("lib-provider-paste.url_shell");
    case "url_user":
      return name ? t("lib-provider-paste.url_user_alias", { name }) : line ? t("lib-provider-paste.url_user_line", { line }) : t("lib-provider-paste.url_user");
    case "key_shell":
      return t("lib-provider-paste.key_shell");
    case "no_key":
      return t("lib-provider-paste.no_key");
    case "key_helper":
      return t("lib-provider-paste.key_helper");
    case "curl_option":
      if (name) return t("lib-provider-paste.curl_option", { name });
      break;
    case "curl_path":
      if (name) return t("lib-provider-paste.curl_path", { name });
      break;
    case "cindy_kind":
      return t("lib-provider-paste.cindy_kind");
    case "cindy_auth":
      return name === "oauth" ? t("lib-provider-paste.cindy_auth_oauth") : name === "none" ? t("lib-provider-paste.cindy_auth_none") : t("lib-provider-paste.cindy_auth", { name: name ?? "" });
    case "cindy_endpoint":
      if (name) return t("lib-provider-paste.cindy_endpoint", { name });
      break;
    case "too_many":
      return t("lib-provider-paste.drafts_limit", { max: MAX_DRAFTS });
  }
  return line ? t("lib-provider-paste.other_line", { line }) : t("lib-provider-paste.other");
}

// ── Found on your computers ────────────────────────────────

/** The fields of GET /api/v1/launchers' entries that finding aliases reads. */
export type ListedAlias = Pick<ListedLauncher, "name" | "source" | "hidden" | "machines"> & { base_url?: string; origin?: string };

/** An rc-file alias that reaches a gateway and is not in Persona yet, with the computer to read it on (its first
 *  online one, else its first) and that computer's name. */
export interface FoundAlias {
  name: string;
  base_url: string;
  machine: string;
  computer: string;
  online: boolean;
}

/** The aliases "Found on your computers" lists: rc-file ones only (the import reads shell files, not PATH wrappers),
 *  shown, with an address, and not imported already (a Persona provider whose origin is this alias on one of its
 *  computers). */
export function foundAliases(list: readonly ListedAlias[], machines: readonly Pick<Machine, "id" | "name" | "online">[]): FoundAlias[] {
  const imported = new Set(list.filter((l) => managedProvider(l.source) && l.origin?.startsWith("zshrc:")).map((l) => l.origin!));
  const out: FoundAlias[] = [];
  for (const l of list) {
    const on = l.machines ?? [];
    if (l.source !== "zshrc" || l.hidden || !l.base_url || !on.length || on.some((m) => imported.has(`zshrc:${l.name}@${m}`))) continue;
    const machine = on.find((id) => machines.some((m) => m.id === id && m.online)) ?? on[0]!;
    const m = machines.find((x) => x.id === machine);
    out.push({ name: l.name, base_url: l.base_url, machine, computer: m?.name || machine, online: !!m?.online });
  }
  return out;
}
