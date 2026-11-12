// Machine details (/machines/<id>, docs/server/machines.md "App", iOS MachineDetailView): one page, the most used on top — how the
// computer is (online, system, which hub when there are several), its agents by herdr space (a row opens the chat),
// its values now, CPU / memory / network / load over 1, 6 or 24 hours (hover a chart to read a moment, all follow) and
// each core now (Linux), the system, the launchers. Its engine (which one the agents come from) follows the agents. A Mac the hub's extension cua is on for gets "Computer control" after that, and "Browser" (ego lite) while ego is on.
// A Mac whose live screen can be shown (showsScreen) gets "View screen" in the top bar, the page's one way to it: it stays in view
// while the page scrolls, and does not wait for Cua Driver, which the screen does not use.
import { MoveToManager } from "../ManagerParts";
import { offlineManagerOf, useRoster } from "../../lib/managers";
import { useEffect, useMemo, useRef, useState } from "react";
import { api, ApiError, enc, errorText, isAbort } from "../../lib/api";
import { ago } from "../../lib/format";
import {
  agentsBySpace,
  bytesAxis,
  bytesBin,
  connectionText,
  coresNow,
  countsText,
  isEmpty,
  level,
  load as loadText,
  loadAxis,
  localOffset,
  memPercent,
  percent,
  RANGE_KEYS,
  RANGES,
  rate,
  systemFacts,
  systemLine,
  usedOfTotal,
  type DetailMachine,
  type DetailMetrics,
  type Level,
  type MetricRange,
} from "../../lib/machine-detail";
import { hubLabel, routeText } from "../../lib/mesh";
import { machineDeployment } from "../../lib/machine-deploy";
import { classifyConnection } from "../../lib/node-health";
import { screenPath, showsScreen } from "../../lib/computer";
import { href, onLinkClick } from "../../lib/router";
import { getState, refresh, useStore } from "../../lib/store";
import { foldRows } from "../../lib/folded-rows";
import type { Launcher, Session } from "../../lib/types";
import { Button, cx, EASE, IconButton, Loading, toast } from "../../ui/atoms";
import { AgentRow } from "./AgentRow";
import { Icon } from "../../ui/icons";
import { ProviderName } from "../../ui/ProviderName";
import { MetricChart, type ChartSeries } from "./MetricChart";
import { MachineDeploy } from "./MachineDeploy";
import { BrowserSection } from "./BrowserSection";
import { ComputerSection } from "./ComputerSection";
import { EngineSection } from "./EngineSection";
import { openEngineSection } from "./engine-anchor";
import { BotmuxSwitch } from "./BotmuxSwitch";
import { currentEngine, currentHosts, engineName, hostsName, supportsHostSets } from "../../lib/engines";
import { NodeHealth } from "./NodeHealth";
import { UsageSection } from "./UsageSection";
import { MachineExtensionSections } from "../extensions/ExtensionSlots";
import { t } from "../../i18n";

// MACHINE_RENAME_FEATURE: the hub renames computers (PUT /api/v1/machines/{id}/name, docs/server/machines.md
// "Renaming a computer"); an older one keeps the name its hub.json has.
const MACHINE_RENAME_FEATURE = "machine_rename";

// RenameMachine edits what the hub calls the computer, in place of its title row: the hub writes hub.json and every
// app and the computer's Persona Link follow (a machines event), with no restart or reconnect. Empty is its id again.
function RenameMachine({ m, onDone }: { m: DetailMachine; onDone: () => void }) {
  const [value, setValue] = useState(m.name && m.name !== m.id ? m.name : "");
  const [busy, setBusy] = useState(false);
  const save = async (e?: React.FormEvent) => {
    e?.preventDefault();
    setBusy(true);
    try {
      const r = await api<{ name: string }>(`/machines/${enc(m.id)}/name`, { method: "PUT", json: { name: value.trim() } });
      await refresh().catch(() => {});
      toast(t("machines-machinedetail.renamed", { name: r.name }), "ok");
      onDone();
    } catch (err) {
      toast(errorText(err), "error");
      setBusy(false);
    }
  };
  return (
    <form onSubmit={save} className="flex flex-wrap items-center gap-2" onKeyDown={(e) => e.key === "Escape" && (e.stopPropagation(), onDone())}>
      <input
        autoFocus
        value={value}
        onChange={(e) => setValue(e.target.value)}
        placeholder={m.id}
        aria-label={t("machines-machinedetail.rename_label")}
        maxLength={40}
        className="h-8 w-[240px] max-w-full rounded-control bg-inset px-2.5 text-[13px] text-ink shadow-hairline outline-none placeholder:text-ink-3 focus:shadow-[0_0_0_1px_var(--line-strong),0_0_0_3px_var(--accent-tint)]"
      />
      <Button tone="quiet" className="h-8" onClick={onDone}>
        {t("common.cancel")}
      </Button>
      <Button tone="primary" type="submit" className="h-8" busy={busy}>
        {t("common.save")}
      </Button>
      <p className="w-full text-[11.5px] text-ink-3">{t("machines-machinedetail.rename_hint")}</p>
    </form>
  );
}

type MachineLauncher = Pick<Launcher, "name" | "display_name" | "model" | "agent" | "agent_type">;

const NO_SESSIONS: Session[] = [];

function initialRange(): MetricRange {
  const r = new URLSearchParams(location.search).get("range");
  return RANGE_KEYS.includes(r as MetricRange) ? (r as MetricRange) : "1h";
}

export function MachineDetail({ mid, onToggleSidebar }: { mid: string; onToggleSidebar: () => void }) {
  const machines = useStore((s) => s.machines) as DetailMachine[];
  const loaded = useStore((s) => s.loaded);
  // a child session folds into its parent (#5120): never an agent of the computer of its own
  const sessions = useStore((s) => foldRows(s.sessions[mid] ?? NO_SESSIONS));
  const m = machines.find((x) => x.id === mid);
  const online = m?.online ?? false;
  const expectedDisconnect = m ? machineDeployment(m).expectedDisconnect : false;

  const [range, setRangeState] = useState<MetricRange>(initialRange);
  const [data, setData] = useState<{ metrics: DetailMetrics; range: MetricRange; at: number } | null>(null);
  const [failure, setFailure] = useState("");
  const [launchers, setLaunchers] = useState<MachineLauncher[] | null>(null);
  const [launcherError, setLauncherError] = useState("");
  const [reconnecting, setReconnecting] = useState(false);
  const alive = useRef(true);
  useEffect(() => () => void (alive.current = false), []);

  const setRange = (r: MetricRange) => {
    setRangeState(r);
    history.replaceState(history.state, "", r === "1h" ? location.pathname : `${location.pathname}?range=${r}`);
  };

  // Metrics: now, then every 10 s (the hour) or every minute, and again when the page comes back into view; nothing
  // while the tab is hidden.
  useEffect(() => {
    let dead = false;
    let ctrl: AbortController | null = null;
    const load = async () => {
      if (document.visibilityState !== "visible") return;
      ctrl?.abort();
      ctrl = new AbortController();
      try {
        const r = await api<{ metrics: DetailMetrics }>(`/machines/${enc(mid)}/metrics?range=${range}`, { signal: ctrl.signal });
        if (dead) return;
        setData({ metrics: { ...r.metrics, points: r.metrics.points ?? [] }, range, at: Date.now() / 1000 });
        setFailure("");
      } catch (e) {
        if (dead || isAbort(e)) return;
        setFailure(e instanceof ApiError && e.status === 404 ? t("machines-machinedetail.no_metrics_hub") : errorText(e));
      }
    };
    void load();
    const timer = setInterval(load, RANGES[range].poll);
    const vis = () => void load();
    document.addEventListener("visibilitychange", vis);
    return () => {
      dead = true;
      ctrl?.abort();
      clearInterval(timer);
      document.removeEventListener("visibilitychange", vis);
    };
  }, [mid, range]);

  // Launchers: an offline computer has none to list; again once it comes online.
  useEffect(() => {
    if (!online) {
      setLaunchers(null);
      return;
    }
    let dead = false;
    api<{ launchers: MachineLauncher[] }>(`/machines/${enc(mid)}/launchers`)
      .then((r) => !dead && (setLaunchers(r.launchers ?? []), setLauncherError("")))
      .catch((e) => !dead && setLauncherError(errorText(e)));
    return () => {
      dead = true;
    };
  }, [mid, online]);

  // Asks the hub to reconnect, then watches the machine list (up to 15 s) for it to come online.
  const reconnect = async () => {
    setReconnecting(true);
    try {
      await api(`/machines/${enc(mid)}/reconnect`, { method: "POST" });
    } catch (e) {
      toast(t("machines.reconnect_failed", { error: errorText(e) }), "error");
      setReconnecting(false);
      return;
    }
    for (let i = 0; i < 10 && alive.current; i++) {
      await new Promise((r) => setTimeout(r, 1500));
      await refresh().catch(() => {});
      if (getState().machines.find((x) => x.id === mid)?.online) {
        if (alive.current) setReconnecting(false);
        toast(t("machines-machinedetail.connected"), "ok");
        return;
      }
    }
    if (!alive.current) return;
    setReconnecting(false);
    const err = getState().machines.find((x) => x.id === mid)?.error;
    toast(err ? t("machines-machinedetail.not_connected_error", { error: err }) : t("machines-machinedetail.not_connected"), "error");
  };

  // its manager offline (several managers): that is why, and Reconnect would go through it
  const roster = useRoster();
  const goneWith = m ? offlineManagerOf(roster, m) : null;
  // a linked hub's computer is that hub's to give (`not_movable`): in a mesh every computer names its hub
  const selfHub = useStore((s) => s.mesh.find((x) => x.self)?.hub_id);
  const connFailure = m && !expectedDisconnect && !goneWith ? classifyConnection(m) : null;
  const groups = useMemo(() => (online ? agentsBySpace(sessions) : []), [online, sessions]);
  const agentCount = groups.reduce((n, g) => n + g.sessions.length, 0);
  const hub = hubLabel(m, machines);
  const route = routeText(m, machines);
  const system = data?.metrics.system ?? m?.system;
  const tz = useMemo(() => localOffset(), []);
  // a hub that renames computers, and one of its own (a linked hub's is renamed there)
  const canRename = useStore((s) => s.features.includes(MACHINE_RENAME_FEATURE)) && m?.kind !== "hub" && (!m?.hub_id || !selfHub || m.hub_id === selfHub);
  const [renaming, setRenaming] = useState(false);

  if (!m) {
    return (
      <Page mid={mid} title={mid} onToggleSidebar={onToggleSidebar}>
        {!loaded ? (
          <Loading />
        ) : (
          <div className="flex flex-col items-start gap-3 rounded-card bg-surface p-4 shadow-card">
            <p className="text-[13.5px] font-medium text-ink">{t("machines-machinedetail.not_found")}</p>
            <p className="text-[12.5px] text-ink-2">{t("machines-machinedetail.not_found_detail", { id: mid })}</p>
            <a href={href({ name: "machines" })} onClick={(e) => onLinkClick(e, { name: "machines" })} className="text-[12.5px] font-medium text-accent-ink hover:underline">
              {t("machines-machinedetail.back_to_list")}
            </a>
          </div>
        )}
      </Page>
    );
  }

  return (
    <Page
      mid={mid}
      title={m.name}
      onToggleSidebar={onToggleSidebar}
      right={
        <>
          <span className="hidden whitespace-nowrap text-[11.5px] text-ink-3 sm:inline">{range === "1h" ? t("machines-machinedetail.refresh_10s") : t("machines-machinedetail.refresh_1m")}</span>
          {showsScreen(m) && (
            <Button icon="monitor" href={screenPath(mid)} title={t("machines-machinedetail.screen_hint")}>
              {t("machines-machinedetail.screen")}
            </Button>
          )}
        </>
      }
    >
      {/* the computer */}
      <section className="overflow-hidden rounded-card bg-surface shadow-card" style={{ animation: `fade-up 420ms ${EASE} both` }}>
        <div className="flex items-start gap-3 px-4 py-3.5">
          <span className={cx("flex size-10 shrink-0 items-center justify-center rounded-[10px]", online ? "bg-field text-ink-2" : "bg-inset text-ink-3")}>
            <Icon name="machine" size={18} />
          </span>
          <div className="min-w-0 flex-1">
            {renaming && <RenameMachine key={m.id} m={m} onDone={() => setRenaming(false)} />}
            <div className={cx("flex flex-wrap items-center gap-x-2 gap-y-1", renaming && "hidden")}>
              <h2 className="truncate text-[15px] font-semibold text-ink">{m.name}</h2>
              {canRename && <IconButton icon="write" label={t("machines-machinedetail.rename")} size={24} iconSize={12} onClick={() => setRenaming(true)} />}
              <span className={cx("inline-flex h-5 items-center gap-1 whitespace-nowrap rounded-full px-1.5 text-[11px] font-medium", online ? "bg-green-tint text-green" : "bg-field text-ink-3")}>
                <span className={cx("size-1.5 rounded-full", online ? "bg-green" : "bg-ink-3")} />
                {online ? t("machines-machinedetail.online") : t("machines-machinedetail.offline")}
              </span>
              {hub && (
                <span className="inline-flex h-5 items-center gap-1 whitespace-nowrap rounded-full bg-inset px-1.5 text-[11px] font-medium text-ink-2 shadow-hairline" title={t("machines-machinedetail.belongs_to_hub", { hub })}>
                  <Icon name="hub" size={11} />
                  {hub}
                </span>
              )}
              {/* its engine and botmux, one click away (#2842): the engine opens Engine below, botmux flips */}
              <button
                type="button"
                data-testid="engine-pill"
                title={t("machines-machinedetail.engine_pill_hint")}
                onClick={() => openEngineSection()}
                className="touch-hit inline-flex h-5 items-center gap-1 whitespace-nowrap rounded-full bg-inset px-1.5 text-[11px] font-medium text-ink-2 shadow-hairline transition-colors hover:bg-hover"
              >
                <Icon name="terminal" size={11} />
                {supportsHostSets(m) ? hostsName(currentHosts(m), m) : engineName(currentEngine(m), m)}
              </button>
              <BotmuxSwitch key={m.id} machineID={m.id} />
            </div>
            <p className="mt-0.5 truncate text-[12px] text-ink-3" title={systemLine(system, m)}>
              {systemLine(system, m)}
            </p>
            {route && <p className="mt-0.5 truncate text-[11.5px] text-ink-3" title={t("machines-machinedetail.route_hint")}>{t("machines-machinedetail.route", { route })}</p>}
          </div>
          {/* with several managers: give it to another one (a linked hub's computer is its own hub's) */}
          <MoveToManager machineId={m.id} manager={m.manager} movable={m.kind !== "hub" && (!m.hub_id || !selfHub || m.hub_id === selfHub)} />
          {!online && !expectedDisconnect && !goneWith && (
            <Button tone="secondary" icon="retry" busy={reconnecting} onClick={() => void reconnect()}>
              {reconnecting ? t("machines-machinedetail.connecting") : t("machines.reconnect")}
            </Button>
          )}
        </div>
        {!online && !expectedDisconnect && (
          <div className="mx-4 mb-3.5 rounded-[8px] bg-red-tint px-2.5 py-1.5 text-[12px] text-red" data-offline-box>
            {/* The plain reason and next step lead when the error text says why; the raw error then goes last, small and
                grey, for whoever debugs it. Without a known reason the raw text (or the generic line) stays first. */}
            {goneWith ? (
              <p className="break-words font-medium" data-error-code="manager_offline" title={m.error}>{t("settings-managers.machine_offline", { name: goneWith.name })}</p>
            ) : connFailure ? (
              <p className="break-words font-medium" data-failure={connFailure.kind}>{t("node-health.failure_line", { title: connFailure.title, next: connFailure.next })}</p>
            ) : (
              <p className="break-words">{m.error || t("machines-machinedetail.unreachable")}</p>
            )}
            <p className="mt-0.5 text-red/80">{[m.last_seen && ago(m.last_seen) && t("machines-machinedetail.last_seen", { when: ago(m.last_seen) }), connectionText(m)].filter(Boolean).join(" · ")}</p>
            {connFailure && m.error && <p className="mt-1 break-words font-mono text-[11px] text-ink-3" data-raw-error>{m.error}</p>}
          </div>
        )}
        {online && m.error && <p className="mx-4 mb-3.5 rounded-[8px] bg-red-tint px-2.5 py-1.5 text-[12px] text-red">{m.error}</p>}
        <MachineDeploy machine={m} className="mb-3.5" />
      </section>

      {/* agents */}
      <section>
        <Heading title="Agent" count={online ? agentCount : undefined} />
        {groups.length === 0 ? (
          <p className="rounded-card bg-surface px-4 py-3 text-[12.5px] text-ink-3 shadow-card">{online ? t("machines-machinedetail.no_agents") : t("machines-machinedetail.agents_offline")}</p>
        ) : (
          <div className="flex flex-col gap-2.5">
            {groups.map((g, gi) => (
              <div key={g.key} className="overflow-hidden rounded-card bg-surface shadow-card" style={{ animation: `fade-up 420ms ${EASE} ${Math.min(gi, 6) * 50}ms both` }}>
                <div className="flex items-center gap-2 border-b border-line px-4 py-2">
                  <Icon name={g.key ? "layers" : "agent"} size={13} className="shrink-0 text-ink-3" />
                  {g.key ? (
                    // a space's name opens its page (/space/…); agents outside any space have none
                    <a
                      href={href({ name: "space", mid, space: g.name })}
                      onClick={(e) => onLinkClick(e, { name: "space", mid, space: g.name })}
                      className="group/space flex min-w-0 items-center gap-0.5 truncate rounded-[5px] text-[12.5px] font-medium text-ink hover:text-accent-ink"
                      title={t("machines-machinedetail.open_space", { name: g.name })}
                    >
                      <span className="truncate">{g.name}</span>
                      <Icon name="chevronRight" size={11} stroke={2.4} className="shrink-0 text-ink-3 transition-transform duration-150 group-hover/space:translate-x-0.5" />
                    </a>
                  ) : (
                    <span className="truncate text-[12.5px] font-medium text-ink">{g.name}</span>
                  )}
                  <span className="min-w-0 flex-1 truncate text-[11.5px] text-ink-3">{countsText(g.counts)}</span>
                  <span className="shrink-0 text-[11.5px] text-ink-3 tabular-nums">{g.sessions.length}</span>
                </div>
                <div className="divide-y divide-line">
                  {g.sessions.map((s) => (
                    <AgentRow key={s.session_id} s={s} />
                  ))}
                </div>
              </div>
            ))}
          </div>
        )}
      </section>

      {/* its token usage, then how busy it is, then the engine and what it can do (#4973, as in the apps) */}
      <UsageSection mid={mid} heading={(right) => <Heading title={t("machines-usage.title")} right={right} />} />

      {/* monitoring */}
      <section>
        <Heading
          title={t("machines-machinedetail.monitoring")}
          right={
            <div className="flex items-center gap-0.5 rounded-control bg-inset p-0.5 shadow-hairline" role="radiogroup" aria-label={t("machines-machinedetail.range")}>
              {RANGE_KEYS.map((k) => (
                <button
                  key={k}
                  type="button"
                  role="radio"
                  aria-checked={range === k}
                  onClick={() => setRange(k)}
                  className={cx(
                    "flex h-7 items-center whitespace-nowrap rounded-[7px] px-2.5 text-[12px] transition-[background-color,color] duration-150",
                    range === k ? "bg-surface font-medium text-ink shadow-btn" : "text-ink-3 hover:text-ink-2",
                  )}
                >
                  {RANGES[k].label}
                </button>
              ))}
            </div>
          }
        />
        {!data ? (
          failure ? (
            <div className="rounded-card bg-surface px-4 py-6 text-center shadow-card">
              <p className="text-[13px] font-medium text-ink">{t("machines-machinedetail.no_metrics")}</p>
              <p className="mt-1 text-[12px] text-ink-3">{failure}</p>
            </div>
          ) : (
            <div className="flex h-40 items-center justify-center rounded-card bg-surface shadow-card">
              <Loading />
            </div>
          )
        ) : (
          <Monitor data={data} range={range} online={online} system={system} tz={tz} onRange={setRange} />
        )}
        {data && failure && <p className="mt-2 px-1 text-[11.5px] text-red">{t("machines-machinedetail.refresh_failed", { error: failure })}</p>}
        <p className="mt-2 px-1 text-[11.5px] leading-relaxed text-ink-3">{t("machines-machinedetail.metrics_note")}</p>
      </section>

      {/* which engine the agents above come from (iOS engineRow) */}
      <EngineSection machine={m} heading={<Heading title={t("machines-machinedetail.engine")} />} />

      {/* what this computer can do for Persona, from the node's self-check (docs/server/machines.md "Doctor: what this computer can do") */}
      <NodeHealth machine={m} heading={<Heading title={t("node-health.title")} />} />

      {m.computer && <ComputerSection mid={mid} computer={m.computer} online={online} manager={m.manager} />}
      {m.browser && <BrowserSection mid={mid} browser={m.browser} online={online} />}

      {/* system */}
      <section>
        <Heading title={t("machines-machinedetail.system")} />
        <div className="overflow-hidden rounded-card bg-surface shadow-card">
          <dl className="-mb-px grid grid-cols-1 md:grid-cols-2">
            {systemFacts(system, m, Date.now() / 1000, tz).map(([k, v, mono]) => (
              <div key={k} className="flex items-baseline gap-3 border-b border-line px-4 py-2 md:odd:border-r">
                <dt className="w-[84px] shrink-0 whitespace-nowrap text-[12px] text-ink-3">{k}</dt>
                <dd className={cx("min-w-0 flex-1 text-[12.5px] break-words text-ink-2 select-text", mono && "font-mono text-[12px]")}>{v}</dd>
              </div>
            ))}
          </dl>
        </div>
      </section>

      {/* launchers */}
      <section>
        <Heading title={t("machines-machinedetail.launchers")} count={online && launchers?.length ? launchers.length : undefined} />
        <div className="overflow-hidden rounded-card bg-surface shadow-card">
          {!online ? (
            <p className="px-4 py-3 text-[12.5px] text-ink-3">{t("machines-machinedetail.launchers_offline")}</p>
          ) : launcherError && !launchers ? (
            <p className="px-4 py-3 text-[12.5px] text-red">{t("machines-machinedetail.launchers_failed", { error: launcherError })}</p>
          ) : !launchers ? (
            <div className="px-4 py-3">
              <Loading />
            </div>
          ) : launchers.length === 0 ? (
            <p className="px-4 py-3 text-[12.5px] text-ink-3">{t("machines-machinedetail.no_launchers")}</p>
          ) : (
            <div className="divide-y divide-line">
              {launchers.map((l) => {
                const kind = l.agent_type || l.agent || "claude";
                return (
                  <div key={l.name} className="flex items-center gap-3 px-4 py-2">
                    {/* a provider's display name, its startup ID beside it (#5029); an rc alias's ID in mono, as before */}
                    <ProviderName of={l} className="min-w-0 truncate text-[12.5px] text-ink" idClassName="min-w-0 shrink-[2] truncate text-[11.5px] text-ink-3" />
                    {kind !== "claude" && <span className="inline-flex h-5 shrink-0 items-center whitespace-nowrap rounded-full bg-inset px-1.5 text-[11px] font-medium text-ink-2 shadow-hairline">{kind === "codex" ? "Codex" : kind === "traex" ? "TRAEX" : kind}</span>}
                    <span className="flex-1" />
                    {l.model && <span className="shrink-0 truncate font-mono text-[11.5px] text-ink-3">{l.model}</span>}
                  </div>
                );
              })}
            </div>
          )}
        </div>
      </section>

      {/* the extensions' sections (docs/extensions/extensions.md "UI slots" machine_section), after the machine's own */}
      <MachineExtensionSections mid={mid} heading={(title, right) => <Heading title={title} right={right} />} />
    </Page>
  );
}

function Page({ mid, title, right, onToggleSidebar, children }: { mid: string; title: string; right?: React.ReactNode; onToggleSidebar: () => void; children: React.ReactNode }) {
  return (
    <div className="flex h-full flex-col">
      <header className="flex h-14 shrink-0 items-center gap-2 border-b border-line px-3 sm:px-4">
        <IconButton icon="sidebar" label={t("machines-machinedetail.sidebar")} onClick={onToggleSidebar} className="lg:hidden" />
        <a
          href={href({ name: "machines" })}
          onClick={(e) => onLinkClick(e, { name: "machines" })}
          className="flex h-7 shrink-0 items-center gap-0.5 whitespace-nowrap rounded-[7px] pr-2 pl-1 text-[12.5px] text-ink-3 transition-colors hover:bg-hover hover:text-ink"
          title={t("machines-machinedetail.back_to_list")}
        >
          <Icon name="chevronLeft" size={14} stroke={2.2} />
          {t("machines-machinedetail.machines")}
        </a>
        <h1 className="min-w-0 flex-1 truncate text-[14px] font-semibold text-ink" title={mid}>
          {title}
        </h1>
        {right}
      </header>
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto flex max-w-[880px] flex-col gap-7 px-4 py-6 sm:px-8">{children}</div>
      </div>
    </div>
  );
}

function Heading({ title, count, right }: { title: string; count?: number; right?: React.ReactNode }) {
  return (
    <div className="mb-2.5 flex min-h-7 items-center gap-2 px-1">
      <h2 className="text-[13px] font-medium text-ink">{title}</h2>
      {count !== undefined && <span className="text-[12px] text-ink-3 tabular-nums">{count}</span>}
      <span className="flex-1" />
      {right}
    </div>
  );
}

// ── monitoring: the values now, then the charts ──

function Monitor({
  data,
  range,
  online,
  system,
  tz,
  onRange,
}: {
  data: { metrics: DetailMetrics; range: MetricRange; at: number };
  range: MetricRange;
  online: boolean;
  system: DetailMachine["system"];
  tz: number;
  onRange: (r: MetricRange) => void;
}) {
  const { metrics } = data;
  const live = online ? (metrics.latest ?? undefined) : undefined;
  const perCore = coresNow(metrics.latest, online);

  // Nothing in the whole range (offline all along, or just started): one line instead of three empty charts.
  if (isEmpty(metrics.points))
    return (
      <>
        {live && <NowTiles s={live} cores={system?.cores} />}
        {coresNow(metrics.latest, online) && <CoreBars cores={coresNow(metrics.latest, online)!} />}
        <div className={cx("flex flex-col items-center gap-2.5 rounded-card bg-surface px-4 py-7 text-center shadow-card transition-opacity duration-200", data.range !== range && "opacity-60")}>
          <p className="text-[12.5px] text-ink-2">
            {online ? t("machines-machinedetail.just_started") : t("machines-machinedetail.offline_range", { range: RANGES[data.range].label })}
          </p>
          {!online && data.range !== "24h" && (
            <Button tone="secondary" onClick={() => onRange("24h")}>
              {t("machines-machinedetail.show_24h")}
            </Button>
          )}
        </div>
      </>
    );

  return (
    <>
      {live && <NowTiles s={live} cores={system?.cores} />}
      <Charts data={data} range={range} live={live} system={system} tz={tz} />
      {perCore && <CoreBars cores={perCore} />}
    </>
  );
}

const percentAxis = (v: number) => `${v}%`;

/** The four charts and the moment they share. The moment lives here, not in the page: a pointer move re-renders these
 * charts (each only its crosshair and read-out), not the tiles, the cores, the agents or the launchers. */
function Charts({
  data,
  range,
  live,
  system,
  tz,
}: {
  data: { metrics: DetailMetrics; range: MetricRange; at: number };
  range: MetricRange;
  live: NonNullable<DetailMetrics["latest"]> | undefined;
  system: DetailMachine["system"];
  tz: number;
}) {
  const { metrics } = data;
  const r = RANGES[data.range];
  const t1 = data.at,
    t0 = t1 - r.secs;
  const memTotal = live?.mem_total ?? system?.memory ?? 0;
  const memPct = memPercent(live?.mem_used, live?.mem_total);

  const cpu = useMemo<ChartSeries[]>(() => [{ name: "CPU", color: "var(--accent)", read: (p) => p.cpu, now: live?.cpu }], [live?.cpu]);
  const mem = useMemo<ChartSeries[]>(() => [{ name: t("machines-machinedetail.memory"), color: "var(--green)", read: (p) => (p.mem !== undefined && memTotal ? (100 * p.mem) / memTotal : undefined), now: memPct }], [memTotal, memPct]);
  const net = useMemo<ChartSeries[]>(
    () => [
      { name: t("machines-machinedetail.download"), color: "var(--orange)", read: (p) => p.rx, now: live?.net_rx },
      { name: t("machines-machinedetail.upload"), color: "var(--accent)", read: (p) => p.tx, now: live?.net_tx },
    ],
    [live?.net_rx, live?.net_tx],
  );
  const load = useMemo<ChartSeries[]>(() => [{ name: t("machines-machinedetail.load"), color: "var(--chart-load)", read: (p) => p.load, now: live?.load }], [live?.load]);
  const cores = system?.cores ?? 0;

  // Picking another range lets go of the moment (React's "adjusting some state when a prop changes").
  const [hover, setHover] = useState<number | null>(null);
  const [hoverRange, setHoverRange] = useState(range);
  if (hoverRange !== range) {
    setHoverRange(range);
    setHover(null);
  }
  const common = { points: metrics.points, t0, t1, step: r.step, hover, onHover: setHover, tz };

  return (
    <div className={cx("grid grid-cols-1 gap-3 transition-opacity duration-200 md:grid-cols-2", data.range !== range && "opacity-60")}>
      <MetricChart title="CPU" series={cpu} percent format={percent} axisFormat={percentAxis} {...common} />
      <MetricChart title={memTotal ? t("machines-machinedetail.memory_total", { total: bytesBin(memTotal).replace(/\.00 /, " ") }) : t("machines-machinedetail.memory")} series={mem} percent format={percent} axisFormat={percentAxis} {...common} />
      <MetricChart title={t("machines-machinedetail.network")} series={net} binary format={rate} axisFormat={bytesAxis} {...common} />
      <MetricChart title={t("machines-machinedetail.load_1m")} series={load} format={loadText} axisFormat={loadAxis} reference={cores > 0 ? { value: cores, label: t("machines-machinedetail.cores", { count: cores }) } : undefined} {...common} />
    </div>
  );
}

function NowTiles({ s, cores }: { s: NonNullable<DetailMetrics["latest"]>; cores?: number }) {
  const memPct = memPercent(s.mem_used, s.mem_total);
  const diskPct = memPercent(s.disk_used, s.disk_total);
  const swapPct = s.swap_total ? memPercent(s.swap_used, s.swap_total) : undefined;
  const perCore = s.load !== undefined && cores ? s.load / cores : undefined;
  const tiles: { label: string; value: string; sub?: string; frac?: number; lv: Level }[] = [
    { label: "CPU", value: percent(s.cpu), sub: cores ? t("machines-machinedetail.cores", { count: cores }) : undefined, frac: s.cpu, lv: level(s.cpu, 75, 90) },
    { label: t("machines-machinedetail.memory"), value: percent(memPct), sub: s.mem_used !== undefined ? usedOfTotal(s.mem_used, s.mem_total) : undefined, frac: memPct, lv: level(memPct, 80, 90) },
    { label: t("machines-machinedetail.network"), value: `↓ ${rate(s.net_rx)}`, sub: `↑ ${rate(s.net_tx)}`, lv: "normal" },
    { label: t("machines-machinedetail.load"), value: loadText(s.load), sub: cores ? t("machines-machinedetail.cores", { count: cores }) : undefined, lv: level(perCore, 1, 2) },
    { label: t("machines-machinedetail.disk"), value: percent(diskPct), sub: s.disk_used !== undefined ? usedOfTotal(s.disk_used, s.disk_total) : undefined, frac: diskPct, lv: level(diskPct, 80, 90) },
    { label: t("machines-machinedetail.swap"), value: s.swap_total ? bytesBin(s.swap_used) : t("machines-machinedetail.swap_off"), sub: s.swap_total ? t("machines-machinedetail.swap_total", { total: bytesBin(s.swap_total) }) : undefined, frac: swapPct, lv: "normal" },
  ];
  return (
    <div className="mb-3 grid grid-cols-3 gap-2 lg:grid-cols-6">
      {tiles.map((tile) => (
        <div key={tile.label} className="min-w-0 rounded-card bg-surface px-2.5 py-2.5 shadow-card sm:px-3">
          <div className="flex items-center gap-1.5 text-[11px] text-ink-3">
            <span className="truncate">{tile.label}</span>
            {tile.lv !== "normal" && (
              <span className="inline-flex shrink-0 items-center gap-1 whitespace-nowrap text-ink-2">
                <span className={cx("size-1.5 rounded-full", tile.lv === "critical" ? "bg-red" : "bg-orange")} />
                {tile.lv === "critical" ? t("machines-machinedetail.critical") : t("machines-machinedetail.high")}
              </span>
            )}
          </div>
          <div className="mt-0.5 truncate text-[15px] leading-6 font-medium text-ink tabular-nums sm:text-[17px]" title={tile.value}>{tile.value}</div>
          <div className="truncate text-[10.5px] text-ink-3 tabular-nums" title={tile.sub}>{tile.sub ?? " "}</div>
          <div className="mt-1.5 h-1 overflow-hidden rounded-full bg-field">
            {tile.frac !== undefined && (
              <div
                className={cx("h-full rounded-full transition-[width] duration-500", tile.lv === "critical" ? "bg-red" : tile.lv === "warning" ? "bg-orange" : "bg-accent")}
                style={{ width: `${Math.max(3, Math.min(100, tile.frac))}%` }}
              />
            )}
          </div>
        </div>
      ))}
    </div>
  );
}

/** Each core now (Linux), one thin column each (iOS CoreBars); hover a column for its number. */
function CoreBars({ cores }: { cores: number[] }) {
  const busiest = Math.max(...cores);
  return (
    <section className="mt-3 rounded-card bg-surface px-4 pt-3 pb-3.5 shadow-card" role="img" aria-label={t("machines-machinedetail.cores_label", { count: cores.length, busiest: percent(busiest) })}>
      <div className="flex items-baseline gap-2">
        <h3 className="truncate text-[12.5px] font-medium text-ink-2">{t("machines-machinedetail.per_core")}</h3>
        <span className="flex-1" />
        <span className="shrink-0 text-[11.5px] whitespace-nowrap text-ink-3 tabular-nums">{t("machines-machinedetail.busiest", { value: percent(busiest) })}</span>
      </div>
      <div className={cx("mt-2.5 flex h-11 items-end", cores.length > 32 ? "gap-px" : "gap-0.5")}>
        {cores.map((c, i) => (
          <div key={i} title={t("machines-machinedetail.core_title", { n: i + 1, value: percent(c) })} className="relative h-full min-w-px flex-1 overflow-hidden rounded-[2px] bg-field">
            <div className="absolute inset-x-0 bottom-0 rounded-[2px] bg-accent transition-[height] duration-500" style={{ height: `${Math.max(4, c)}%` }} />
          </div>
        ))}
      </div>
    </section>
  );
}
