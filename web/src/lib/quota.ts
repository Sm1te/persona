// A launcher's quota at its gateway (GET /api/v1/launchers/{name}/quota, docs/integrations/launchers.md "Quota"), as a quota extension
// read it, and how its numbers read (iOS LauncherQuotaSection / QuotaText).
import { currentLanguage, formatDate, formatNumber, t } from "../i18n";

export interface QuotaWindow {
  label: string;
  used_ratio?: number;
  used?: number;
  limit?: number;
  /** usd | tokens | requests | credits | percent … */
  unit?: string;
  resets_at?: string;
  exhausted?: boolean;
}

export interface QuotaPool {
  name: string;
  /** available | ok | unsupported | cooldown | exhausted | error … */
  state?: string;
  detail?: string;
  windows?: QuotaWindow[];
  models?: { model: string; windows: QuotaWindow[] }[];
}

export interface QuotaUsage {
  label: string;
  requests: number;
  tokens: number;
  cost_usd?: number;
}

export interface LauncherQuota {
  /** The gateway's id (the extension's contribution). */
  gateway: string;
  /** What to call it, from the hub (an older hub leaves it out: the id is shown). */
  gateway_name?: string;
  key_name?: string;
  usage?: QuotaUsage[];
  budget?: QuotaWindow[];
  pools?: QuotaPool[];
  note?: string;
}

/** `error` when there is nothing to read (`unsupported`: no extension knows the gateway) or the gateway said no. */
export interface LauncherQuotaAnswer {
  machine?: string;
  checked_at?: string;
  quota?: LauncherQuota;
  error?: string;
  unsupported?: boolean;
}

export function gatewayName(q: Pick<LauncherQuota, "gateway" | "gateway_name">): string {
  const n = q.gateway_name?.trim();
  if (n) return n;
  return q.gateway || "gateway";
}

/** Something wrong with a pool (cooling down, used up, unreadable); "no quota interface" is just how it is. */
export function poolTrouble(p: QuotaPool): boolean {
  return !(p.state === undefined || ["available", "ok", "unsupported"].includes(p.state));
}

export type QuotaTone = "accent" | "orange" | "red";

/** ≥ 70 % orange, ≥ 90 % or used up red. */
export function quotaTone(ratio: number, exhausted?: boolean): QuotaTone {
  if (exhausted || ratio >= 0.9) return "red";
  if (ratio >= 0.7) return "orange";
  return "accent";
}

/** 12K, 340M (Chinese counts in units of ten thousand and a hundred million). */
export function quotaCount(v: number): string {
  const short = (x: number) => x.toFixed(1).replace(/\.0$/, "");
  const a = Math.abs(v);
  // English has no units of ten thousand / a hundred million: it reads 12K, 210.6M
  if (a >= 1e4 && currentLanguage() === "en") return formatNumber(v, { notation: "compact", maximumFractionDigits: 1 });
  if (a >= 1e8) return t("lib-quota.count_1e8", { n: short(v / 1e8) });
  if (a >= 1e4) return t("lib-quota.count_1e4", { n: short(v / 1e4) });
  return v.toFixed(0);
}

export function quotaMoney(v: number): string {
  return v >= 100 ? v.toFixed(0) : v.toFixed(2);
}

/** "64% used", "6401 / 10K", "$496 / $800", "Used up". */
export function quotaAmount(w: QuotaWindow): string {
  if (w.exhausted && (w.used_ratio ?? 1) >= 1) return t("lib-quota.used_up");
  if (w.used !== undefined && w.limit !== undefined) {
    switch (w.unit) {
      case "usd":
        return `$${quotaMoney(w.used)} / $${quotaMoney(w.limit)}`;
      case "tokens":
        return t("lib-quota.amount_tokens", { used: quotaCount(w.used), limit: quotaCount(w.limit) });
      case "requests":
        return t("lib-quota.amount_requests", { used: quotaCount(w.used), limit: quotaCount(w.limit) });
      case "credits":
        return `${quotaCount(w.used)} / ${quotaCount(w.limit)}`;
    }
  }
  if (w.used_ratio !== undefined) return t("lib-quota.used_percent", { percent: Math.round(w.used_ratio * 100) });
  return "—";
}

export function quotaUsage(u: QuotaUsage): string {
  let s = t("lib-quota.usage", { requests: quotaCount(u.requests), tokens: quotaCount(u.tokens) });
  if (u.cost_usd !== undefined) s += ` · $${quotaMoney(u.cost_usd)}`;
  return s;
}

/** "Resets Nov 6 15:00", or "Reset" once it is past. */
export function quotaReset(iso: string, now = Date.now()): string {
  const at = Date.parse(iso);
  if (!Number.isFinite(at)) return "";
  if (at <= now) return t("lib-quota.reset_done");
  return t("lib-quota.resets_at", { time: formatDate(at, { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit", hour12: false }) });
}

/** The bar's width, 0…100. */
export function quotaPercent(ratio: number): number {
  return Math.round(Math.min(Math.max(ratio, 0), 1) * 100);
}

export interface WeeklyQuota {
  window: QuotaWindow;
  /** Whole percent left, clamped to 0…100. */
  remaining: number;
  tone: QuotaTone;
}

/** The rail's one weekly number: the most constrained weekly window in the launcher's existing cached answer. */
export function weeklyQuota(answer: LauncherQuotaAnswer | undefined): WeeklyQuota | undefined {
  const q = answer?.quota;
  if (!q) return undefined;
  const windows = [...(q.budget ?? []), ...(q.pools ?? []).flatMap((p) => p.windows ?? [])];
  let found: { window: QuotaWindow; used: number } | undefined;
  for (const w of windows) {
    if (!/week|周|週/i.test(w.label)) continue;
    let used = w.used_ratio !== undefined && Number.isFinite(w.used_ratio) ? w.used_ratio : undefined;
    if (used === undefined && w.used !== undefined && w.limit !== undefined && Number.isFinite(w.used) && w.used >= 0 && Number.isFinite(w.limit) && w.limit > 0) used = w.used / w.limit;
    if (used === undefined) {
      if (!w.exhausted) continue;
      used = 1;
    }
    if (!found || (w.exhausted && !found.window.exhausted) || (w.exhausted === found.window.exhausted && used > found.used)) found = { window: w, used };
  }
  if (!found) return undefined;
  return { window: found.window, remaining: found.window.exhausted ? 0 : 100 - quotaPercent(found.used), tone: quotaTone(found.used, found.window.exhausted) };
}

/** Nothing to show beyond the key's use: the key has no ceiling. */
export function quotaUnlimited(q: LauncherQuota): boolean {
  return !q.pools?.length && !q.budget?.length && !q.note;
}

/** The line under the section: the key's name at the gateway, which machine read it, when. */
export function quotaFooter(a: LauncherQuotaAnswer, machineName: (id: string) => string, when: (iso: string) => string): string {
  const parts: string[] = [];
  const k = a.quota?.key_name;
  if (k) parts.push(t("lib-quota.footer_key", { key: k }));
  if (a.machine) parts.push(t("lib-quota.footer_machine", { machine: machineName(a.machine) }));
  const w = a.checked_at ? when(a.checked_at) : "";
  if (w) parts.push(t("lib-quota.footer_when", { when: w }));
  return parts.length ? parts.join(t("lib-quota.footer_separator")) + t("lib-quota.footer_end") : "";
}
