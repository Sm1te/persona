// Token usage by machine (docs/server/machines.md "Token usage", #4973): the counts' format, the API's shapes, and what the
// charts draw from them. The apps have the same rules (apple/Shared/TokenUsage.swift).

/** Token counts: K / M / B with three significant digits and no trailing zeros (1,250 → 1.25K, 2,960,000 → 2.96M,
 *  178,000,000 → 178M); under 1,000 the number itself. Rounding that reaches 1,000 of a unit moves up a unit
 *  (999,500 → 1M, not 1000K). */
export function formatTokens(n: number): string {
  const sign = n < 0 ? "-" : "";
  const v = Math.abs(Math.round(n));
  if (v < 1000) return sign + String(v);
  const units: [number, string][] = [[1e3, "K"], [1e6, "M"], [1e9, "B"]];
  let i = v >= 1e9 ? 2 : v >= 1e6 ? 1 : 0;
  for (;;) {
    const x = v / units[i][0];
    const digits = x >= 100 ? 0 : x >= 10 ? 1 : 2;
    const scale = 10 ** digits;
    const rounded = Math.round(x * scale) / scale;
    if (rounded >= 1000 && i + 1 < units.length) {
      i++;
      continue;
    }
    return sign + String(Number(rounded.toFixed(digits))) + units[i][1];
  }
}

/** A count of tokens on one line (a subagent card's "23.4k tokens", #5030): under 1,000 the number itself, then one
 *  decimal with k, M or B, moving up a unit where rounding would reach 1,000 (999,960 → 1.0M, not 1000.0k). */
export function compactTokens(n: number): string {
  const v = Math.max(0, Math.round(n));
  if (v < 1000) return String(v);
  const units: [number, string][] = [[1e3, "k"], [1e6, "M"], [1e9, "B"]];
  for (const [div, unit] of units) if (v / div < 999.95 || unit === "B") return `${(v / div).toFixed(1)}${unit}`;
  return String(v);
}

export type UsageKind = "in" | "out" | "cache_write" | "cache_read";
/** The kinds in the order a stacked chart puts them from the bottom. */
export const USAGE_KINDS: UsageKind[] = ["in", "out", "cache_write", "cache_read"];
/** Each kind's chart color (a CSS token, styles.css). */
export const USAGE_COLOR: Record<UsageKind, string> = {
  in: "var(--usage-input)",
  out: "var(--usage-output)",
  cache_write: "var(--usage-cache-write)",
  cache_read: "var(--usage-cache-read)",
};

/** The four kinds of tokens and the replies, summed over some time. */
export interface UsageTokens {
  in?: number;
  cache_write?: number;
  cache_read?: number;
  out?: number;
  req?: number;
}

export interface UsagePoint extends UsageTokens {
  t: number;
  gap?: boolean;
}

export interface UsageShare extends UsageTokens {
  name: string;
}

/** GET /api/v1/machines/{id}/usage. */
export interface MachineUsage {
  machine_id: string;
  online: boolean;
  range: UsageRange;
  step: number;
  points: UsagePoint[];
  total: UsageTokens;
  models: UsageShare[];
  launchers: UsageShare[];
  launchers_known: boolean;
  stored?: boolean;
}

/** A machine's tokens on its card (GET /api/v1/metrics). */
export interface UsageCard {
  today: UsageTokens;
  per_minute: number;
  /** The last 60 minutes, oldest first. */
  recent: number[];
}

export type UsageRange = "1h" | "24h" | "7d";
export const USAGE_RANGES: UsageRange[] = ["1h", "24h", "7d"];
/** The unknown launcher's filter value (a session typed in a terminal, an older node). */
export const UNKNOWN_LAUNCHER = "-";

/** Every token, cache reads included. */
export function totalTokens(u: UsageTokens): number {
  return (u.in ?? 0) + (u.cache_write ?? 0) + (u.cache_read ?? 0) + (u.out ?? 0);
}

/** Cache read / (input + cache write + cache read); undefined without any input. */
export function hitRate(u: UsageTokens): number | undefined {
  const prompt = (u.in ?? 0) + (u.cache_write ?? 0) + (u.cache_read ?? 0);
  return prompt > 0 ? (u.cache_read ?? 0) / prompt : undefined;
}

export function addTokens(a: UsageTokens, b: UsageTokens): UsageTokens {
  return {
    in: (a.in ?? 0) + (b.in ?? 0),
    cache_write: (a.cache_write ?? 0) + (b.cache_write ?? 0),
    cache_read: (a.cache_read ?? 0) + (b.cache_read ?? 0),
    out: (a.out ?? 0) + (b.out ?? 0),
    req: (a.req ?? 0) + (b.req ?? 0),
  };
}

/** A step's sum per minute. */
export function perMinute(n: number, step: number): number {
  return Math.round(n / Math.max(step / 60, 1));
}

/** The stacked chart's layers: for each kind shown, every step's bottom and top (per minute), bottom up. */
export function stackLayers(u: MachineUsage, kinds: UsageKind[]): { kind: UsageKind; points: { t: number; y0: number; y1: number }[] }[] {
  const base = u.points.map(() => 0);
  return kinds.map((kind) => ({
    kind,
    points: u.points.map((p, i) => {
      const y0 = base[i];
      base[i] += perMinute(p[kind] ?? 0, u.step);
      return { t: p.t, y0, y1: base[i] };
    }),
  }));
}

/** The runs of offline steps, from the first's start to the last's end. */
export function offlineRuns(u: MachineUsage): [number, number][] {
  const out: [number, number][] = [];
  let start: number | null = null;
  u.points.forEach((p, i) => {
    if (p.gap && start === null) start = p.t;
    const ends = !p.gap || i === u.points.length - 1;
    if (start !== null && ends) {
      out.push([start, p.gap ? p.t + u.step : p.t]);
      start = null;
    }
  });
  return out;
}

/** The hit rate's runs: a step with at least `minReplies` replies is drawn; fewer (or offline) breaks the line. */
export function hitRuns(u: MachineUsage, minReplies = 3): { t: number; v: number }[][] {
  const out: { t: number; v: number }[][] = [];
  let run: { t: number; v: number }[] = [];
  for (const p of u.points) {
    const r = hitRate(p);
    if (p.gap || (p.req ?? 0) < minReplies || r === undefined) {
      if (run.length) out.push(run);
      run = [];
      continue;
    }
    run.push({ t: p.t, v: r });
  }
  if (run.length) out.push(run);
  return out;
}

/** The step nearest to t, within one step; undefined over an offline one. */
export function nearestStep(u: MachineUsage, t: number): UsagePoint | undefined {
  let best: UsagePoint | undefined;
  for (const p of u.points) if (!best || Math.abs(p.t - t) < Math.abs(best.t - t)) best = p;
  return best && Math.abs(best.t - t) <= u.step && !best.gap ? best : undefined;
}

export function percentText(v: number | undefined): string {
  return v === undefined ? "—" : `${Math.round(v * 100)}%`;
}
