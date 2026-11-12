// A machine's token usage on its page (docs/server/machines.md "Token usage charts", #4973), as in the apps: the range, model
// and launcher filters, the range's sums, throughput per minute (every token, or the four kinds stacked; a legend entry
// hides its kind: cache reads are often nine tenths of it), the cache hit rate, and each model's and launcher's share.
// Hovering either chart (or ← → on it) shows that moment in both; the legend then says that moment's values. The areas
// and lines are memoized layers, so a move redraws the crosshair and the read-outs only.
import { memo, useEffect, useMemo, useState } from "react";
import { api, ApiError, enc, errorText, isAbort } from "../../lib/api";
import { hm, scale, timeAt, yAxis } from "../../lib/machine-detail";
import {
  formatTokens,
  hitRate,
  hitRuns,
  nearestStep,
  offlineRuns,
  percentText,
  perMinute,
  stackLayers,
  totalTokens,
  UNKNOWN_LAUNCHER,
  USAGE_COLOR,
  USAGE_KINDS,
  USAGE_RANGES,
  type MachineUsage,
  type UsageKind,
  type UsagePoint,
  type UsageRange,
} from "../../lib/tokens";
import { Button, cx, Loading } from "../../ui/atoms";
import { t } from "../../i18n";
import { useWidth } from "../../lib/use-width";
import { namedLauncher, providerIdNote, providerLabel, providerTitle, useProviderNames } from "../../lib/provider-names";

const H = 150;
const HIT_H = 84;
const AXIS_W = 52;
const AXIS_H = 20;
const TOP = 6;

const kindName = (k: UsageKind) => t(`machines-usage.kind_${k}`);
const launcherName = (name: string) => (name === "" ? t("machines-usage.other_launcher") : name);

/** Round times for the x axis: hours, or days for the week. */
function ticksFor(t0: number, t1: number, range: UsageRange, tz: number, count: number): number[] {
  const span = t1 - t0;
  const unit = range === "7d" ? 86400 : [3600, 2 * 3600, 3 * 3600, 6 * 3600, 900, 1800].find((u) => span / u <= count) ?? 6 * 3600;
  const step = range === "1h" ? [600, 900, 1200, 1800].find((u) => span / u <= count) ?? 1800 : unit;
  const first = Math.ceil((t0 + tz) / step) * step - tz;
  const out: number[] = [];
  for (let x = first; x <= t1; x += step) out.push(x);
  return out;
}

function dayLabel(tSec: number, tz: number): string {
  return new Date((tSec + tz) * 1000).toLocaleDateString(undefined, { weekday: "short", timeZone: "UTC" });
}

export function UsageSection({ mid, heading }: { mid: string; heading: (right: React.ReactNode) => React.ReactNode }) {
  // A different computer starts with its own controls, catalogs and request lifetime.
  return <UsageReport key={mid} mid={mid} heading={heading} />;
}

function UsageReport({ mid, heading }: { mid: string; heading: (right: React.ReactNode) => React.ReactNode }) {
  const [range, setRange] = useState<UsageRange>("24h");
  const [model, setModel] = useState<string | null>(null);
  const [launcher, setLauncher] = useState<string | null>(null);
  const [stacked, setStacked] = useState(true);
  const [hidden, setHidden] = useState<Set<UsageKind>>(new Set());
  const scope = JSON.stringify([mid, range, model, launcher]);
  const [accepted, setAccepted] = useState<{ scope: string; data: MachineUsage } | null>(null);
  // Menus are a catalog, not report totals: keep them usable while another scope is being read.
  const [catalog, setCatalog] = useState<Pick<MachineUsage, "models" | "launchers" | "launchers_known"> | null>(null);
  const [status, setStatus] = useState({ scope, pending: true, failure: "", unsupported: false });
  const [retry, setRetry] = useState(0);
  const current = status.scope === scope ? status : { pending: true, failure: "", unsupported: false };
  const data = accepted?.scope === scope && !current.unsupported ? accepted.data : null;
  const [hover, setHover] = useState<number | null>(null);
  const tz = -new Date().getTimezoneOffset() * 60;
  // a launcher by the provider's display name, its startup ID as secondary text (#5029); "" is the report's Other
  const names = useProviderNames();
  const launcherOf = (name: string) => (name === "" ? { name: launcherName(name) } : namedLauncher(name, names));
  const launcherLabel = (name: string) => providerLabel(launcherOf(name));

  useEffect(() => {
    let dead = false;
    let ctrl: AbortController | null = null;
    const load = async () => {
      if (document.visibilityState !== "visible") return;
      ctrl?.abort();
      const request = new AbortController();
      ctrl = request;
      const active = () => !dead && ctrl === request && !request.signal.aborted;
      setStatus((old) => ({ scope, pending: true, failure: old.scope === scope ? old.failure : "", unsupported: old.scope === scope && old.unsupported }));
      const q = new URLSearchParams({ range });
      if (model !== null) q.set("model", model);
      if (launcher !== null) q.set("launcher", launcher === "" ? UNKNOWN_LAUNCHER : launcher);
      try {
        const r = await api<{ usage: MachineUsage }>(`/machines/${enc(mid)}/usage?${q}`, { signal: request.signal });
        if (!active()) return;
        const report = { ...r.usage, points: r.usage.points ?? [], models: r.usage.models ?? [], launchers: r.usage.launchers ?? [] };
        setAccepted({ scope, data: report });
        setCatalog(report);
        setStatus({ scope, pending: false, failure: "", unsupported: false });
      } catch (e) {
        if (!active() || isAbort(e)) return;
        const unsupported = e instanceof ApiError && e.status === 404;
        setStatus({ scope, pending: false, unsupported, failure: unsupported ? "" : errorText(e) });
      } finally {
        if (active()) setStatus((old) => ({ ...old, pending: false }));
      }
    };
    void load();
    const timer = setInterval(load, range === "1h" ? 20_000 : 60_000);
    const vis = () => void load();
    document.addEventListener("visibilitychange", vis);
    return () => {
      dead = true;
      ctrl?.abort();
      clearInterval(timer);
      document.removeEventListener("visibilitychange", vis);
    };
  }, [mid, range, model, launcher, scope, retry]);

  const kinds = useMemo(() => (stacked ? USAGE_KINDS.filter((k) => !hidden.has(k)) : []), [stacked, hidden]);
  const picked = data && hover !== null ? nearestStep(data, hover) : undefined;
  const toggle = (k: UsageKind) =>
    setHidden((h) => {
      const next = new Set(h);
      if (next.has(k)) next.delete(k);
      else if (next.size < USAGE_KINDS.length - 1) next.add(k);
      return next;
    });

  const rangeControl = (
    <div className="flex items-center gap-0.5 rounded-control bg-inset p-0.5 shadow-hairline" role="radiogroup" aria-label={t("machines-usage.range")}>
      {USAGE_RANGES.map((k) => (
        <button
          key={k}
          type="button"
          role="radio"
          aria-checked={range === k}
          onClick={() => (setRange(k), setHover(null))}
          className={cx(
            "flex h-7 items-center whitespace-nowrap rounded-[7px] px-2.5 text-[12px] transition-[background-color,color] duration-150",
            range === k ? "bg-surface font-medium text-ink shadow-btn" : "text-ink-3 hover:text-ink-2",
          )}
        >
          {t(`machines-usage.range_${k}`)}
        </button>
      ))}
    </div>
  );

  return (
    <section data-testid="machine-usage" aria-busy={current.pending}>
      {heading(rangeControl)}
      <div className="grid gap-3 lg:grid-cols-[minmax(0,2fr)_minmax(0,1fr)]">
        <div className="min-w-0 rounded-card bg-surface px-4 pt-3 pb-3 shadow-card">
          <div className="flex flex-wrap items-center gap-2">
            <Filter
              label={t("machines-usage.model")}
              all={t("machines-usage.all_models")}
              value={model}
              options={(catalog?.models ?? []).map((m) => ({ value: m.name, label: m.name }))}
              onChange={setModel}
            />
            <Filter
              label={t("machines-usage.launcher")}
              all={t("machines-usage.all_launchers")}
              value={launcher}
              options={(catalog?.launchers ?? []).map((l) => ({ value: l.name, label: launcherLabel(l.name) }))}
              labelOf={launcherLabel}
              onChange={setLauncher}
              disabled={catalog?.launchers_known === false}
            />
            <span className="flex-1" />
            <div className="flex items-center gap-0.5 rounded-control bg-inset p-0.5 shadow-hairline" role="radiogroup" aria-label={t("machines-usage.chart")}>
              {[false, true].map((s) => (
                <button
                  key={String(s)}
                  type="button"
                  role="radio"
                  aria-checked={stacked === s}
                  onClick={() => setStacked(s)}
                  className={cx("flex h-6 items-center whitespace-nowrap rounded-[6px] px-2 text-[11.5px]", stacked === s ? "bg-surface font-medium text-ink shadow-btn" : "text-ink-3 hover:text-ink-2")}
                >
                  {s ? t("machines-usage.by_kind") : t("machines-usage.total")}
                </button>
              ))}
            </div>
          </div>
          {catalog?.launchers_known === false && <p className="mt-1.5 text-[11.5px] text-ink-3">{t("machines-usage.old_node")}</p>}
          {(current.failure || current.unsupported) && (
            <div className="mt-3 flex flex-wrap items-center gap-2">
              <div className="min-w-0 flex-1 break-words text-[12.5px]" role={current.failure ? "alert" : "status"}>
                <p className={current.failure ? "text-red" : "text-ink-3"}>{current.unsupported ? t("machines-usage.old_hub") : current.failure}</p>
                {data && <p className="mt-1 text-ink-3">{t("machines-usage.stale_report")}</p>}
              </div>
              <Button tone="secondary" icon="retry" busy={current.pending} onClick={() => setRetry((n) => n + 1)}>{t("machines-usage.retry")}</Button>
            </div>
          )}
          {!data && current.pending && <div className="flex h-40 items-center justify-center"><Loading /></div>}
          {data && (
            <>
              <Totals data={data} />
              <div className="mt-3 flex items-baseline gap-2">
                <h3 className="text-[12.5px] font-medium text-ink-2">{t("machines-usage.throughput")}</h3>
                <span className="flex-1" />
                <span className="font-mono text-[11px] text-ink-3 tabular-nums">{picked ? stamp(picked.t, data.range, tz) : t("machines-usage.per_minute")}</span>
                <span className="text-[14px] font-medium text-ink tabular-nums">
                  {t("machines-usage.per_minute_value", { n: formatTokens(shownNow(data, picked, stacked ? kinds : USAGE_KINDS)) })}
                </span>
              </div>
              <Throughput data={data} kinds={kinds} hover={hover} onHover={setHover} tz={tz} model={model} launcher={launcher} onClear={() => (setModel(null), setLauncher(null))} />
              {stacked && <Legend data={data} picked={picked} hidden={hidden} onToggle={toggle} />}
              {data.points.some((p) => p.gap) && <p className="mt-1.5 text-[11.5px] text-ink-3">{t("machines-usage.offline_note")}</p>}
              <div className="mt-3 flex items-baseline gap-2">
                <h3 className="text-[12.5px] font-medium text-ink-2">{t("machines-usage.hit_rate_title")}</h3>
                <span className="flex-1" />
                <span className="text-[14px] font-medium text-ink tabular-nums">{percentText(picked ? hitRate(picked) : hitRate(data.total))}</span>
              </div>
              <HitRate data={data} hover={hover} onHover={setHover} tz={tz} />
              <p className="mt-1 text-[11.5px] text-ink-3">{t("machines-usage.hit_rate_note")}</p>
            </>
          )}
        </div>
        {data && <div className="flex flex-col gap-3">
          <Shares title={t("machines-usage.by_model")} shares={data.models.map((m) => ({ key: m.name, label: m.name, tokens: m }))} selected={model} onPick={(k) => setModel(model === k ? null : k)} />
          {data.launchers_known && (
            <Shares title={t("machines-usage.by_launcher")} shares={data.launchers.map((l) => ({ key: l.name, label: providerTitle(launcherOf(l.name)), note: providerIdNote(launcherOf(l.name)), tokens: l }))} selected={launcher} onPick={(k) => setLauncher(launcher === k ? null : k)} />
          )}
        </div>}
      </div>
    </section>
  );
}

function stamp(tSec: number, range: UsageRange, tz: number): string {
  return range === "7d" ? `${dayLabel(tSec, tz)} ${hm(tSec, tz)}` : hm(tSec, tz);
}

/** The per-minute number by the throughput title: the held moment's, or the latest step's, of the kinds shown. */
function shownNow(data: MachineUsage, picked: UsagePoint | undefined, kinds: UsageKind[]): number {
  const at = picked ?? [...data.points].reverse().find((p) => !p.gap);
  return perMinute(kinds.reduce((s, k) => s + (at?.[k] ?? 0), 0), data.step);
}

function Totals({ data }: { data: MachineUsage }) {
  const cells: [string, string][] = [
    [t("machines-usage.total"), formatTokens(totalTokens(data.total))],
    [t("machines-usage.kind_out"), formatTokens(data.total.out ?? 0)],
    [t("machines-usage.hit_rate"), percentText(hitRate(data.total))],
    [t("machines-usage.replies"), formatTokens(data.total.req ?? 0)],
  ];
  return (
    <dl className="mt-3 grid grid-cols-4 gap-3">
      {cells.map(([k, v]) => (
        <div key={k} className="min-w-0">
          <dt className="truncate text-[11.5px] text-ink-3">{k}</dt>
          <dd className="truncate text-[17px] font-medium text-ink tabular-nums">{v}</dd>
        </div>
      ))}
    </dl>
  );
}

function Filter({ label, all, value, options, labelOf = launcherName, onChange, disabled }: {
  label: string;
  all: string;
  value: string | null;
  options: { value: string; label: string }[];
  /** What a picked value no longer in `options` is called. */
  labelOf?: (value: string) => string;
  onChange: (v: string | null) => void;
  disabled?: boolean;
}) {
  return (
    <label className={cx("relative inline-flex h-7 items-center rounded-full px-3 text-[12px] shadow-hairline", value !== null ? "bg-accent/12 text-accent" : "bg-inset text-ink-2", disabled && "opacity-50")}>
      <span className="sr-only">{label}</span>
      <select
        className="cursor-pointer appearance-none bg-transparent pr-4 outline-none disabled:cursor-not-allowed"
        value={value ?? "\u0000all"}
        disabled={disabled}
        onChange={(e) => onChange(e.target.value === "\u0000all" ? null : e.target.value)}
      >
        <option value={"\u0000all"}>{all}</option>
        {value !== null && !options.some((o) => o.value === value) && <option value={value}>{labelOf(value)}</option>}
        {options.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
      <span aria-hidden className="pointer-events-none absolute right-2.5 text-[9px]">▾</span>
    </label>
  );
}

function Legend({ data, picked, hidden, onToggle }: { data: MachineUsage; picked?: UsagePoint; hidden: Set<UsageKind>; onToggle: (k: UsageKind) => void }) {
  return (
    <div className="mt-2 flex flex-wrap gap-x-5 gap-y-1">
      {USAGE_KINDS.map((k) => {
        const off = hidden.has(k);
        const v = picked ? formatTokens(perMinute(picked[k] ?? 0, data.step)) : formatTokens(data.total[k] ?? 0);
        return (
          <button
            key={k}
            type="button"
            aria-pressed={!off}
            onClick={() => onToggle(k)}
            title={t("machines-usage.legend_hint")}
            className={cx("flex items-center gap-1.5 whitespace-nowrap rounded-[6px] px-1 py-0.5 text-[12px] hover:bg-inset", off ? "text-ink-3 line-through decoration-ink-3/60" : "text-ink-2")}
          >
            <span aria-hidden className="inline-block size-2 rounded-full" style={{ background: off ? "var(--line)" : USAGE_COLOR[k] }} />
            {kindName(k)}
            <span className={cx("font-medium tabular-nums", off ? "text-ink-3" : "text-ink")}>{v}</span>
          </button>
        );
      })}
    </div>
  );
}

/** Tokens per minute: the kinds stacked from the bottom, or every token as one line when kinds is empty. */
function Throughput({ data, kinds, hover, onHover, tz, model, launcher, onClear }: {
  data: MachineUsage;
  kinds: UsageKind[];
  hover: number | null;
  onHover: (t: number | null) => void;
  tz: number;
  model: string | null;
  launcher: string | null;
  onClear: () => void;
}) {
  const [box, width] = useWidth<HTMLDivElement>();
  const W = Math.max(0, width - AXIS_W);
  const t0 = data.points[0]?.t ?? 0;
  const t1 = (data.points[data.points.length - 1]?.t ?? t0) + data.step;
  const layers = useMemo(() => stackLayers(data, kinds.length ? kinds : USAGE_KINDS), [data, kinds]);
  const tops = useMemo(() => (layers.length ? layers[layers.length - 1].points.map((p) => p.y1) : []), [layers]);
  const axis = useMemo(() => yAxis(Math.max(1, ...tops)), [tops]);
  const x = scale(t0, t1, 0, W);
  const y = scale(0, axis.top, H, TOP);
  const picked = hover !== null ? nearestStep(data, hover) : undefined;
  const ticks = ticksFor(t0, t1, data.range, tz, W < 360 ? 3 : 5);
  const empty = totalTokens(data.total) === 0;
  // Each step's point at its middle, so a bar of time reads as the step it sums.
  const mid = (tt: number) => x(tt + data.step / 2);
  const summary = t("machines-usage.chart_summary", { total: formatTokens(totalTokens(data.total)), peak: formatTokens(Math.max(0, ...tops)) });

  return (
    <div ref={box} className="relative mt-1.5">
      {width > 0 && (
        <svg width={width} height={H + AXIS_H} role="img" aria-label={summary} tabIndex={0} onKeyDown={(e) => keyHover(e, data, picked, onHover)} onBlur={() => onHover(null)} className="block rounded-[4px] outline-none focus-visible:shadow-[0_0_0_2px_var(--accent)]">
          <Grid axis={axis} y={y} W={W} format={formatTokens} />
          <XTicks ticks={ticks} x={x} W={W} range={data.range} tz={tz} hoverX={picked ? mid(picked.t) : null} />
          <ThroughputLayers data={data} layers={layers} tops={tops} stacked={kinds.length > 0} t0={t0} t1={t1} W={W} top={axis.top} />
          <Crosshair at={picked ? mid(picked.t) : null} H={H} label={picked ? stamp(picked.t, data.range, tz) : ""} W={W} />
          <rect x={0} y={0} width={W} height={H + AXIS_H} fill="transparent" onPointerMove={(e) => pointer(e, t0, t1, data, onHover)} onPointerDown={(e) => pointer(e, t0, t1, data, onHover)} onPointerLeave={() => onHover(null)} style={{ touchAction: "pan-y" }} />
        </svg>
      )}
      {width === 0 && <div style={{ height: H + AXIS_H }} />}
      {empty && width > 0 && (
        <div className="pointer-events-auto absolute inset-x-0 top-[42%] -translate-y-1/2 text-center text-[12px] text-ink-3" style={{ paddingRight: AXIS_W }}>
          {model !== null || launcher !== null ? (
            <>
              {t("machines-usage.empty_filtered")}{" "}
              <button type="button" className="text-accent hover:underline" onClick={onClear}>{t("machines-usage.clear_filters")}</button>
            </>
          ) : (
            t(data.range === "7d" ? "machines-usage.empty_week" : "machines-usage.empty")
          )}
        </div>
      )}
    </div>
  );
}

function HitRate({ data, hover, onHover, tz }: { data: MachineUsage; hover: number | null; onHover: (t: number | null) => void; tz: number }) {
  const [box, width] = useWidth<HTMLDivElement>();
  const W = Math.max(0, width - AXIS_W);
  const t0 = data.points[0]?.t ?? 0;
  const t1 = (data.points[data.points.length - 1]?.t ?? t0) + data.step;
  const runs = useMemo(() => hitRuns(data), [data]);
  const low = useMemo(() => Math.min(0.5, Math.floor(Math.min(1, ...runs.flat().map((p) => p.v)) * 10) / 10), [runs]);
  const x = scale(t0, t1, 0, W);
  const y = scale(low, 1, HIT_H, TOP);
  const mid = (tt: number) => x(tt + data.step / 2);
  const picked = hover !== null ? nearestStep(data, hover) : undefined;
  const ticks = [low, (low + 1) / 2, 1];
  return (
    <div ref={box} className="relative mt-1">
      {width > 0 && (
        <svg width={width} height={HIT_H + AXIS_H} role="img" aria-label={t("machines-usage.hit_rate_label", { rate: percentText(hitRate(data.total)) })} className="block">
          {ticks.map((v) => (
            <g key={v}>
              <line x1={0} x2={W} y1={y(v)} y2={y(v)} stroke="var(--line)" strokeWidth={1} shapeRendering="crispEdges" />
              <text x={W + 6} y={y(v)} dy="0.35em" fontSize={10.5} fill="var(--ink-3)" className="tabular-nums">{percentText(v)}</text>
            </g>
          ))}
          <XTicks ticks={ticksFor(t0, t1, data.range, tz, W < 360 ? 3 : 5)} x={x} W={W} range={data.range} tz={tz} H={HIT_H} />
          <HitRateLine runs={runs} step={data.step} t0={t0} t1={t1} W={W} low={low} />
          {picked && hitRate(picked) !== undefined && (picked.req ?? 0) >= 3 && (
            <g pointerEvents="none">
              <line x1={mid(picked.t)} x2={mid(picked.t)} y1={TOP} y2={HIT_H} stroke="var(--ink-3)" strokeWidth={1} shapeRendering="crispEdges" />
              <circle cx={mid(picked.t)} cy={y(Math.max(low, hitRate(picked) ?? low))} r={4} fill={USAGE_COLOR.cache_read} stroke="var(--surface)" strokeWidth={2} />
            </g>
          )}
          <rect x={0} y={0} width={W} height={HIT_H + AXIS_H} fill="transparent" onPointerMove={(e) => pointer(e, t0, t1, data, onHover)} onPointerDown={(e) => pointer(e, t0, t1, data, onHover)} onPointerLeave={() => onHover(null)} style={{ touchAction: "pan-y" }} />
        </svg>
      )}
      {width === 0 && <div style={{ height: HIT_H + AXIS_H }} />}
    </div>
  );
}

/** The offline steps' shade and the kinds stacked (or every token as one line): two toFixed a point and side, so they are
 * memoized and a hover does not build them again. */
const ThroughputLayers = memo(function ThroughputLayers({ data, layers, tops, stacked, t0, t1, W, top }: {
  data: MachineUsage;
  layers: ReturnType<typeof stackLayers>;
  tops: number[];
  stacked: boolean;
  t0: number;
  t1: number;
  W: number;
  top: number;
}) {
  const x = scale(t0, t1, 0, W);
  const y = scale(0, top, H, TOP);
  const mid = (tt: number) => x(tt + data.step / 2);
  const area = (pts: { t: number; y0: number; y1: number }[]) =>
    pts.map((p) => `${mid(p.t).toFixed(1)},${y(p.y1).toFixed(1)}`).join(" ") + " " +
    [...pts].reverse().map((p) => `${mid(p.t).toFixed(1)},${y(p.y0).toFixed(1)}`).join(" ");
  return (
    <>
      {offlineRuns(data).map(([a, b]) => (
        <rect key={a} x={x(a)} y={TOP} width={Math.max(1, x(b) - x(a))} height={H - TOP} fill="var(--ink)" fillOpacity={0.05} />
      ))}
      {stacked
        ? layers.map((l) => <polygon key={l.kind} points={area(l.points)} fill={USAGE_COLOR[l.kind]} fillOpacity={0.88} stroke="var(--surface)" strokeWidth={1} strokeLinejoin="round" />)
        : (
          <g>
            <polygon points={area(layers[layers.length - 1].points.map((p) => ({ ...p, y0: 0 })))} fill={USAGE_COLOR.in} fillOpacity={0.12} />
            <polyline points={tops.map((v, i) => `${mid(data.points[i].t).toFixed(1)},${y(v).toFixed(1)}`).join(" ")} fill="none" stroke={USAGE_COLOR.in} strokeWidth={2} strokeLinejoin="round" />
          </g>
        )}
    </>
  );
});

/** The hit rate's runs, memoized like the throughput's layers. */
const HitRateLine = memo(function HitRateLine({ runs, step, t0, t1, W, low }: { runs: { t: number; v: number }[][]; step: number; t0: number; t1: number; W: number; low: number }) {
  const x = scale(t0, t1, 0, W);
  const y = scale(low, 1, HIT_H, TOP);
  const mid = (tt: number) => x(tt + step / 2);
  return (
    <>
      {runs.map((r, i) =>
        r.length === 1 ? (
          <circle key={i} cx={mid(r[0].t)} cy={y(r[0].v)} r={1.5} fill={USAGE_COLOR.cache_read} />
        ) : (
          <polyline key={i} points={r.map((p) => `${mid(p.t).toFixed(1)},${y(p.v).toFixed(1)}`).join(" ")} fill="none" stroke={USAGE_COLOR.cache_read} strokeWidth={2} strokeLinejoin="round" strokeLinecap="round" />
        ),
      )}
    </>
  );
});

function pointer(e: React.PointerEvent<SVGRectElement>, t0: number, t1: number, data: MachineUsage, onHover: (t: number | null) => void) {
  const r = e.currentTarget.getBoundingClientRect();
  onHover(timeAt(e.clientX - r.left, r.width, t0, t1) - data.step / 2);
}

function keyHover(e: React.KeyboardEvent, data: MachineUsage, picked: UsagePoint | undefined, onHover: (t: number | null) => void) {
  const real = data.points.filter((p) => !p.gap);
  if (!real.length) return;
  if (e.key === "ArrowLeft" || e.key === "ArrowRight") {
    e.preventDefault();
    const at = picked ? real.indexOf(picked) : real.length - 1;
    onHover(real[Math.min(real.length - 1, Math.max(0, at + (e.key === "ArrowLeft" ? -1 : 1) * (e.shiftKey ? 10 : 1)))].t);
  } else if (e.key === "Escape") onHover(null);
}

function Grid({ axis, y, W, format }: { axis: { top: number; ticks: number[] }; y: (v: number) => number; W: number; format: (v: number) => string }) {
  return (
    <>
      {axis.ticks.map((v) => (
        <g key={v}>
          <line x1={0} x2={W} y1={y(v)} y2={y(v)} stroke="var(--line)" strokeWidth={1} shapeRendering="crispEdges" />
          <text x={W + 6} y={y(v)} dy="0.35em" fontSize={10.5} fill="var(--ink-3)" className="tabular-nums">{format(v)}</text>
        </g>
      ))}
    </>
  );
}

function XTicks({ ticks, x, W, range, tz, H: h = H, hoverX = null }: { ticks: number[]; x: (v: number) => number; W: number; range: UsageRange; tz: number; H?: number; hoverX?: number | null }) {
  return (
    <>
      {ticks.map((tt) => {
        const tx = x(tt);
        const anchor = tx < 16 ? "start" : tx > W - 16 ? "end" : "middle";
        // A label gives way to the held time's badge.
        const covered = hoverX !== null && Math.abs(tx - hoverX) < 52;
        return (
          <g key={tt}>
            <line x1={tx} x2={tx} y1={TOP} y2={h} stroke="var(--line)" strokeWidth={1} shapeRendering="crispEdges" />
            <text visibility={covered ? "hidden" : undefined} x={anchor === "start" ? 0 : anchor === "end" ? W : tx} y={h + 14} textAnchor={anchor} fontSize={10.5} fill="var(--ink-3)" className="tabular-nums">
              {range === "7d" ? dayLabel(tt, tz) : hm(tt, tz)}
            </text>
          </g>
        );
      })}
    </>
  );
}

function Crosshair({ at, H: h, label, W }: { at: number | null; H: number; label: string; W: number }) {
  if (at === null) return null;
  const w = Math.max(42, label.length * 6.4 + 10);
  return (
    <g pointerEvents="none">
      <line x1={at} x2={at} y1={TOP} y2={h} stroke="var(--ink-3)" strokeWidth={1} shapeRendering="crispEdges" />
      <rect x={Math.min(Math.max(0, at - w / 2), W - w)} y={h + 2} width={w} height={16} rx={4} fill="var(--tooltip-bg)" />
      <text x={Math.min(Math.max(w / 2, at), W - w / 2)} y={h + 14} textAnchor="middle" fontSize={10.5} fill="var(--tooltip-fg)" className="tabular-nums">{label}</text>
    </g>
  );
}

/** `note`: a launcher's startup ID beside its display name (#5029); a label without one is an ID or a model, in mono. */
function Shares({ title, shares, selected, onPick }: { title: string; shares: { key: string; label: string; note?: string; tokens: MachineUsage["total"] }[]; selected: string | null; onPick: (k: string) => void }) {
  const top = Math.max(1, ...shares.map((s) => totalTokens(s.tokens)));
  return (
    <div className="rounded-card bg-surface px-4 pt-3 pb-1.5 shadow-card">
      <h3 className="mb-1 text-[12.5px] font-medium text-ink-2">{title}</h3>
      {shares.length === 0 && <p className="py-2 text-[12px] text-ink-3">—</p>}
      {shares.map((s, i) => {
        const rate = hitRate(s.tokens);
        return (
          <button key={s.key} type="button" onClick={() => onPick(s.key)} aria-pressed={selected === s.key} className={cx("block w-full rounded-[6px] py-2 text-left hover:bg-inset/60", i > 0 && "border-t border-line")}>
            <div className="flex items-baseline gap-2">
              <span className={cx("min-w-0 flex-1 truncate text-[12px]", !s.note && "font-mono", selected === s.key ? "text-accent" : "text-ink")}>
                {s.label}
                {s.note && <> <span className="font-mono text-[11px] text-ink-3">{s.note}</span></>}
              </span>
              <span className="text-[12.5px] font-medium text-ink tabular-nums">{formatTokens(totalTokens(s.tokens))}</span>
            </div>
            <div className="mt-1.5 flex items-center gap-2">
              <div className="h-1 flex-1 rounded-full bg-inset">
                <div className="h-1 rounded-full bg-ink-3" style={{ width: `${(100 * totalTokens(s.tokens)) / top}%` }} />
              </div>
              {rate !== undefined && <span className="text-[11px] text-ink-3 tabular-nums">{t("machines-usage.hit", { rate: percentText(rate) })}</span>}
            </div>
          </button>
        );
      })}
    </div>
  );
}
