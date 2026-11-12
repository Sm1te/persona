// Claude Code's tier models in the provider wizard (#4975, docs/integrations/launchers.md "Provider model and gateway
// parameters"): which checked model each tier runs, filled in by itself from the model IDs, what each tier ends up on
// (the node's fallbacks, providerEnv in server/internal/node/launcherenv.go), and what a saved provider is offered.
// Pure: views/settings/ProviderWizard.tsx renders it.
import type { ProviderForm } from "./providers";

/** The tiers the wizard fills in by itself. `light` is the Haiku tier and Claude Code's small model together: the node
 *  runs the Haiku tier on haiku_model, else small_model, so one value (small_model) covers both. */
export type TierSlot = "opus" | "sonnet" | "light" | "fable";
export const TIER_SLOTS: readonly TierSlot[] = ["opus", "sonnet", "light", "fable"];

/** Where a tier's value came from: filled in by itself, or chosen by the user (a saved provider's are the user's);
 *  missing: neither yet, so it may be filled in. */
export type TierSource = Partial<Record<TierSlot, "auto" | "user">>;

export type Tiers = Partial<Record<TierSlot, string>>;

type TierField = "opus_model" | "sonnet_model" | "small_model" | "fable_model";
const FIELD: Record<TierSlot, TierField> = { opus: "opus_model", sonnet: "sonnet_model", light: "small_model", fable: "fable_model" };

/** Only a Claude Code launcher has tiers (the node sets ANTHROPIC_DEFAULT_*_MODEL for claude). */
export const hasTiers = (f: Pick<ProviderForm, "kind" | "agent">) => f.kind === "launcher" && f.agent === "claude";

/** The checked models' IDs, trimmed: what a tier can be set to. */
export const checkedIds = (f: Pick<ProviderForm, "models">) => f.models.filter((m) => m.checked && m.id.trim()).map((m) => m.id.trim());

// ── Reading a model ID ──────────────────────────────────────

/** Claude's tier names, wherever they are in an ID (a gateway's `example-haiku` too). */
const NAMED: Record<string, TierSlot> = { opus: "opus", sonnet: "sonnet", haiku: "light", fable: "fable" };
/** Words that make a model of a family lighter, heavier or in between. */
const LIGHT = new Set(["flash", "air", "mini", "nano", "lite", "turbo", "small", "tiny"]);
const HEAVY: Record<string, number> = { ultra: 3, max: 2, pro: 1 };
const MID = new Set(["plus"]);

/** The catalog's key of an ID (models_catalog.go normalizeCatalogID): lower case, no vendor prefix, no "[1m]". */
const key = (id: string) => {
  const k = id.trim().toLowerCase();
  return k.slice(k.lastIndexOf("/") + 1).replace(/\[1m\]/g, "");
};
const words = (id: string) => key(id).split(/[-_.:\s]+/).filter(Boolean);

/** The leading name before the generation number, the catalog's family without its suffix ("glm" for glm-5.3-flash,
 *  "kimi-k" for kimi-k3.1-pro); an ID with no number is its name words. */
function series(id: string): string {
  const k = key(id);
  const m = /^([a-z][a-z_-]*?)[-_.]?(?=\d)/.exec(k);
  if (m) return m[1];
  return words(id).filter((w) => !LIGHT.has(w) && !(w in HEAVY) && !MID.has(w)).join("-");
}

/** The generation's numbers (glm-5.3 → 5, 3); a date and a parameter size are not part of it. */
function version(id: string): number[] {
  const out: number[] = [];
  for (const w of words(id)) {
    const m = /^[a-z]*(\d{1,3})$/.exec(w);
    if (m) out.push(Number(m[1]));
  }
  return out;
}

/** A parameter size in billions (27b, 0.5b, 8x22b), or 0. */
function size(id: string): number {
  const m = /(?:^|[-_:])(\d+(?:\.\d+)?)(?:x(\d+(?:\.\d+)?))?([bm])(?=$|[-_.:])/.exec(key(id));
  if (!m) return 0;
  return Number(m[1]) * (m[2] ? Number(m[2]) : 1) * (m[3] === "m" ? 0.001 : 1);
}

type Weight = "light" | "heavy" | "mid" | "base";
function weight(id: string): Weight {
  const w = words(id);
  if (w.some((x) => LIGHT.has(x))) return "light";
  if (w.some((x) => x in HEAVY)) return "heavy";
  if (w.some((x) => MID.has(x))) return "mid";
  return "base";
}
const heaviness = (id: string) => Math.max(0, ...words(id).map((w) => HEAVY[w] ?? 0));

const named = (id: string): TierSlot | undefined => {
  for (const w of words(id)) if (w in NAMED) return NAMED[w];
  return undefined;
};

function compareVersion(a: number[], b: number[]): number {
  for (let i = 0; i < Math.max(a.length, b.length); i++) {
    const d = (a[i] ?? 0) - (b[i] ?? 0);
    if (d) return d;
  }
  return 0;
}

/** One of `ids` for a tier: the default model when it is one of them (the tier follows what the user chose), else the
 *  first by `better`, else the newest generation, else the first listed. */
function pick(ids: string[], def: string, better?: (a: string, b: string) => number): string | undefined {
  if (ids.includes(def)) return def;
  return [...ids].sort((a, b) => (better?.(a, b) || 0) || compareVersion(version(b), version(a)))[0];
}

// ── Filling in ──────────────────────────────────────────────

/** Each tier's model read from the checked IDs; a tier none fits is left out.
 *  - An ID naming a Claude tier (opus, sonnet, haiku, fable) goes to it.
 *  - Otherwise, within the default model's family: flash / air / mini / nano / lite / turbo / small (or the smallest
 *    parameter size) are Haiku / small model; ultra / max / pro (or the largest size, or the plain model beside lighter
 *    ones) are Opus; plus (or the plain model beside a heavier one) is Sonnet.
 *  A tier whose pick is the default model is left out too: left empty it runs the default model already, the way the
 *  session starts it (with its 1M marker). */
export function suggestTiers(ids: string[], def: string): Tiers {
  const out: Tiers = {};
  const byName: Partial<Record<TierSlot, string[]>> = {};
  const rest: string[] = [];
  for (const id of ids) {
    const slot = named(id);
    if (slot) (byName[slot] ??= []).push(id);
    else rest.push(id);
  }
  for (const slot of TIER_SLOTS) {
    const list = byName[slot];
    if (list) out[slot] = pick(list, def);
  }
  // other families: only the default model's own (a model of another family may not speak the same way)
  const base = def && !named(def) ? def : "";
  if (base) {
    const family = rest.filter((id) => series(id) === series(base));
    const of = (w: Weight) => family.filter((id) => weight(id) === w);
    const [light, heavy, mid, plain] = [of("light"), of("heavy"), of("mid"), of("base")];
    const sized = family.filter((id) => weight(id) !== "light" && size(id) > 0);
    const sizes = new Set(sized.map(size));
    const bySize = (desc: boolean) => (a: string, b: string) => (desc ? size(b) - size(a) : size(a) - size(b));
    out.opus ??=
      (heavy.length ? pick(heavy, def, (a, b) => heaviness(b) - heaviness(a)) : undefined) ??
      (sizes.size > 1 ? pick(sized, "", bySize(true)) : undefined) ??
      (light.length || mid.length ? pick(plain, def) : undefined);
    out.light ??= light.length ? pick(light, def) : sizes.size > 1 ? pick(sized, "", bySize(false)) : undefined;
    out.sonnet ??= mid.length ? pick(mid, def) : heavy.length ? pick(plain, def) : undefined;
  }
  for (const slot of TIER_SLOTS) if (!out[slot] || out[slot] === def) delete out[slot];
  return out;
}

/** The form with its tiers filled in from the checked models: a tier the user chose is never changed; one filled in
 *  before follows the checked models and the default (or empties when none fits any more); a provider that is not a
 *  Claude Code launcher has none filled in. The same object when nothing changes. */
export function withAutoTiers(f: ProviderForm): ProviderForm {
  const s = hasTiers(f) ? suggestTiers(checkedIds(f), f.model.trim()) : {};
  let next = f;
  for (const slot of TIER_SLOTS) {
    const source = f.tier_source[slot];
    const field = FIELD[slot];
    // the user's, or typed where it is not tracked (another kind's Advanced fields)
    if (source === "user" || (!source && f[field].trim())) continue;
    const want = s[slot] ?? "";
    if (f[field] === want && source === (want ? "auto" : undefined)) continue;
    const tier_source = { ...next.tier_source };
    if (want) tier_source[slot] = "auto";
    else delete tier_source[slot];
    next = { ...next, [field]: want, tier_source };
  }
  return next;
}

/** The Haiku tier and the small model have different models (a saved provider's, set apart): shown as two rows. */
export const tiersSplit = (f: ProviderForm) => !!f.haiku_model.trim() && f.haiku_model.trim() !== f.small_model.trim();

/** A tier set by the user. `light` sets the small model and lets the Haiku tier follow it, unless they are apart. */
export function setTier(f: ProviderForm, slot: TierSlot, value: string): ProviderForm {
  const extra = slot === "light" && !tiersSplit(f) ? { haiku_model: "" } : {};
  return { ...f, [FIELD[slot]]: value, ...extra, tier_source: { ...f.tier_source, [slot]: "user" } };
}

/** Several tiers set at once (a suggestion taken), each as if the user chose it. A lighter model for Haiku / small
 *  model goes to the small model, and to the Haiku tier too when that one was on the Opus tier's model; a Haiku tier the
 *  user set to anything else stays. */
export const applyTiers = (f: ProviderForm, tiers: Tiers) =>
  TIER_SLOTS.reduce((x, slot) => {
    const v = tiers[slot];
    if (!v) return x;
    const next = setTier(x, slot, v);
    const onOpus = !!x.haiku_model.trim() && x.haiku_model.trim() === (x.opus_model.trim() || x.model.trim());
    return slot === "light" && onOpus ? { ...next, haiku_model: "" } : next;
  }, f);

// ── What each tier ends up on ───────────────────────────────

export type TierRow = "opus" | "sonnet" | "light" | "haiku" | "small" | "subagent";

/** One line of the summary: the model it runs in a session on the default model; what an empty one follows (the
 *  session's model, or for the Haiku tier the small model); whether it was filled in by itself. */
export interface TierLine {
  row: TierRow;
  model: string;
  follows: "session" | "small" | null;
  auto: boolean;
}

/** The summary's lines as the node runs them (an empty tier is the session's model, the provider's default unless the
 *  session picks another): Opus, Sonnet, Haiku / small model (Haiku and Small model apart when `split`), Subagents.
 *  Fable stays in Advanced. */
export function tierLines(f: ProviderForm, split = tiersSplit(f)): TierLine[] {
  const def = f.model.trim();
  const line = (row: TierRow, value: string, auto = false): TierLine => ({ row, model: value.trim() || def, follows: value.trim() ? null : "session", auto: auto && !!value.trim() });
  const light = f.tier_source.light === "auto";
  const small = f.small_model.trim();
  const haiku: TierLine = f.haiku_model.trim() || !small ? line("haiku", f.haiku_model) : { row: "haiku", model: small, follows: "small", auto: false };
  return [
    line("opus", f.opus_model, f.tier_source.opus === "auto"),
    line("sonnet", f.sonnet_model, f.tier_source.sonnet === "auto"),
    ...(split ? [haiku, line("small", f.small_model, light)] : [line("light", f.small_model, light)]),
    line("subagent", f.subagent_model),
  ];
}

/** What the tiers step offers under the summary, or null.
 *  - `sameAsOpus`: the Haiku tier or the small model runs on the Opus tier's model while a lighter checked model fits
 *    them (the model they share); `haiku` / `small` say which of the two does.
 *  - `tiers`: what taking the offer sets. A new provider's tiers fill themselves in, so it offers only that lighter
 *    model; a saved one's never change by themselves, so it offers every empty tier a checked model fits, and the
 *    lighter model in place of the Opus one. */
export interface TierAdvice {
  sameAsOpus: string;
  haiku: boolean;
  small: boolean;
  tiers: Tiers;
}

export function tierAdvice(f: ProviderForm, saved: boolean): TierAdvice | null {
  if (!hasTiers(f)) return null;
  const def = f.model.trim();
  const s = suggestTiers(checkedIds(f), def);
  const opus = f.opus_model.trim() || def;
  const haiku = f.haiku_model.trim() || f.small_model.trim() || def;
  const small = f.small_model.trim() || def;
  const lighter = !!opus && !!s.light && s.light !== opus;
  const on = { haiku: lighter && haiku === opus, small: lighter && small === opus };
  const sameAsOpus = on.haiku || on.small ? opus : "";
  const tiers: Tiers = {};
  if (saved) {
    for (const slot of TIER_SLOTS) if (s[slot] && !f[FIELD[slot]].trim()) tiers[slot] = s[slot];
  }
  if (sameAsOpus) tiers.light = s.light;
  return Object.keys(tiers).length ? { sameAsOpus, ...on, tiers } : null;
}
