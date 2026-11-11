// The model panel of an Agent's chat (#5032, docs/web/web.md "Model pill and model panel"), loaded on its pill's first
// tap (ModelPill.tsx AgentModelPill, which also starts reading the lists): the shared panel (ui/model-panel) fed with the
// session's providers and state (lib/session-panel.ts), its picks applied through POST …/modes.
import { useMemo } from "react";
import type { PanelConfig } from "../../lib/model-panel";
import { sessionLauncher } from "../../lib/model-switch";
import { pickPlan, sessionPanel, speedStep, type ModeStep } from "../../lib/session-panel";
import { forgetModel, machineLauncherList, modelList, readModels, useModelLists } from "../../lib/session-models";
import { useStore } from "../../lib/store";
import type { Session } from "../../lib/types";
import { ultracodeOffered } from "../../lib/ultracode";
import { toast } from "../../ui/atoms";
import { ModelPanel } from "../../ui/model-panel/ModelPanel";
import { setServiceTier, useModeSet } from "./SessionDialogs";
import { UltracodeSwitchRow } from "./Ultracode";

/** The panel for an Agent's chat: its computer's launchers as providers, picks applied through POST …/modes one at a
 *  time (the model, then the effort, then the speed), each drawn at once and put back if it fails. */
export function SessionModelPanel({ session, anchor, onClose }: { session: Session; anchor: HTMLElement; onClose: () => void }) {
  const version = useModelLists();
  const { set, latest } = useModeSet(session);
  const mid = session.machine_id;
  const own = sessionLauncher(session);
  const ultracode = useStore((s) => ultracodeOffered(session, s.features));
  const launchers = machineLauncherList(mid);
  // eslint-disable-next-line react-hooks/exhaustive-deps -- `version` stands for what was read
  const { providers, state } = useMemo(() => sessionPanel(session, launchers, (name) => modelList(mid, name)), [session, launchers, mid, version]);

  const apply = async (steps: ModeStep[]) => {
    for (const step of steps) {
      const ok = "service_tier" in step ? await setServiceTier(latest.current, step.service_tier) : await set(step);
      if (!ok) return false;
    }
    return true;
  };
  const onPick = async (config: PanelConfig) => {
    const steps = pickPlan(latest.current, config, modelList(mid, own).list);
    return steps ? apply(steps) : false;
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
      currentExtra={ultracode ? <UltracodeSwitchRow session={session} /> : undefined}
    />
  );
}
