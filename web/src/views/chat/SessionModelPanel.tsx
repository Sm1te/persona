// The model panel of an Agent's chat (#5032, docs/web/web.md "Model pill and model panel"), loaded on its pill's first
// tap (ModelPill.tsx AgentModelPill, which also starts reading the lists): the shared panel (ui/model-panel) fed with the
// session's providers and state (lib/session-panel.ts), its picks applied through POST …/modes. On a hub with the
// in-session switch (#5035) a pick on another launcher is recorded for the next message, which a toast says.
import { useEffect, useMemo, useState } from "react";
import { t } from "../../i18n";
import { api, enc, pastHub } from "../../lib/api";
import type { PanelConfig } from "../../lib/model-panel";
import type { LauncherQuotaAnswer } from "../../lib/quota";
import { sessionLauncher } from "../../lib/model-switch";
import { LAUNCHER_SWITCH_FEATURE, pickPlan, sessionPanel, speedStep, type ModeStep } from "../../lib/session-panel";
import { forgetModel, machineLauncherList, modelList, pendingView, readModels, useModelLists } from "../../lib/session-models";
import { hubHas, useStore } from "../../lib/store";
import type { Session } from "../../lib/types";
import { ultracodeOffered } from "../../lib/ultracode";
import { toast } from "../../ui/atoms";
import { ModelPanel } from "../../ui/model-panel/ModelPanel";
import { setServiceTier, useModeSet } from "./SessionDialogs";
import { UltracodeSwitchRow } from "./Ultracode";

/** The panel for an Agent's chat: its computer's launchers as providers, picks applied through POST …/modes one at a
 *  time (a waiting switch dropped, the model, then the effort, then the speed; or one switch to another launcher), each
 *  drawn at once and put back if it fails. */
export function SessionModelPanel({ session, anchor, onClose }: { session: Session; anchor: HTMLElement; onClose: () => void }) {
  const version = useModelLists();
  const { set, latest, switchLauncher } = useModeSet(session);
  const mid = session.machine_id;
  const own = sessionLauncher(session);
  const ultracode = useStore((s) => ultracodeOffered(session, s.features));
  const switching = useStore((s) => hubHas(s.features, LAUNCHER_SWITCH_FEATURE));
  const launchers = machineLauncherList(mid);
  const { providers, state } = useMemo(
    () => sessionPanel(session, launchers, (name) => modelList(mid, name), { switching }),
    // eslint-disable-next-line react-hooks/exhaustive-deps -- `version` stands for what was read
    [session, launchers, mid, version, switching],
  );
  const [quotas, setQuotas] = useState<Readonly<Record<string, LauncherQuotaAnswer>>>({});
  const providerIDs = JSON.stringify(providers.map((p) => p.id));
  useEffect(() => {
    let dead = false;
    setQuotas({});
    for (const name of JSON.parse(providerIDs) as string[]) {
      // No refresh=1: the hub's one-minute answer is the cheap read; unsupported gateways simply draw no bar.
      api<LauncherQuotaAnswer>(`/launchers/${enc(name)}/quota?machine=${enc(mid)}`, { timeout: pastHub(45_000) })
        .then((answer) => {
          if (!dead) setQuotas((was) => ({ ...was, [name]: answer }));
        })
        .catch(() => {});
    }
    return () => {
      dead = true;
    };
    // providerIDs stands for the launchers, not every model-list refresh.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [mid, providerIDs]);

  const apply = async (steps: ModeStep[]) => {
    for (const step of steps) {
      const ok = "service_tier" in step ? await setServiceTier(latest.current, step.service_tier) : "launcher" in step ? await switchLauncher(step) : await set(step);
      if (!ok) return false;
    }
    return true;
  };
  const onPick = async (config: PanelConfig) => {
    const steps = pickPlan(latest.current, config, (name) => modelList(mid, name).list, { switching });
    if (!steps || !(await apply(steps))) return false;
    // a switch waits for the next message: say so, in the words the pill now shows
    const next = config.provider !== own && steps.length ? pendingView(latest.current, (name) => modelList(mid, name)) : null;
    if (next) toast(t("chat-modelpanel.switch_scheduled", { target: `${next.provider} · ${next.model}` }), "ok");
    return true;
  };
  const onApplyCurrent = async (patch: { effort?: string; fast?: boolean }) => {
    const s = latest.current;
    const steps: ModeStep[] = [];
    if (patch.effort && patch.effort !== s.effort) steps.push({ effort: patch.effort });
    const speed = speedStep(s, patch.fast, true);
    if (speed) steps.push(speed);
    return apply(steps);
  };
  const onReload = (provider: string) => void readModels(mid, provider, { again: true, first: provider === own });
  const onForget = (provider: string, model: string) => {
    void forgetModel(mid, provider, model).then((error) => error && toast(error, "error"));
  };

  return (
    <ModelPanel
      anchor={anchor}
      onClose={onClose}
      providers={providers}
      state={state}
      scope="agent"
      onPick={onPick}
      onApplyCurrent={onApplyCurrent}
      onReload={onReload}
      onForget={onForget}
      quotas={quotas}
      currentExtra={ultracode ? <UltracodeSwitchRow session={session} /> : undefined}
    />
  );
}
