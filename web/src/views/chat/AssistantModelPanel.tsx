// The model panel of a Persona chat (#5033, docs/web/web.md "Model pill and model panel"), loaded on its pill's first tap
// (ModelPill.tsx AssistantModelPill): the shared panel (ui/model-panel, scope "assistant") fed with the assistant
// computer's providers (lib/assistant-panel.ts), a strip on top saying whether the chat follows the default or has its
// own pick, with the way back. On a hub that keeps picks a pick is PUT for the conversation (it holds from the next
// message, on every device), drawn at once and put back with the hub's words if it says no; following the default again
// is a DELETE. An older hub: the pick lasts for this page and goes with each message, as before. The new-conversation
// page: the pick goes with the conversation's start.
import { useMemo } from "react";
import { t } from "../../i18n";
import { api, errorText, sessionPath } from "../../lib/api";
import { assistantPickKey, assistantSetup, type AssistantChat, type AssistantSetup } from "../../lib/assistant-models";
import { assistantPanel, assistantPickOf } from "../../lib/assistant-panel";
import { pickEffort, pickLauncher, pickModel } from "../../lib/assistant-chat";
import type { PanelConfig } from "../../lib/model-panel";
import { TYPED_MODELS_FEATURE } from "../../lib/model-id";
import { providerTitle } from "../../lib/provider-names";
import { speedStep } from "../../lib/session-panel";
import { assistantLaunchers, forgetModel, modelList, readModels, useModelLists } from "../../lib/session-models";
import { getSession, hubHas, upsertSession, useStore } from "../../lib/store";
import type { ModelPick, Session } from "../../lib/types";
import { toast } from "../../ui/atoms";
import { ModelPanel } from "../../ui/model-panel/ModelPanel";
import { setServiceTier } from "./SessionDialogs";

const conversationModel = (s: Session) => `/assistant/conversations/${encodeURIComponent(s.session_id)}/model`;

/** PUT …/model {launcher, model?, effort?, machine_id}: kept for the conversation. Drawn at once; put back, with the hub's
 *  words (out of the computer's scope, gone, another engine than the chat runs, can't run assistant chats…), if it says
 *  no. */
async function keepPick(s: Session, pick: ModelPick): Promise<boolean> {
  const before = s.model_pick;
  upsertSession({ ...s, model_pick: pick });
  try {
    await api(conversationModel(s), { method: "PUT", json: { ...pick, machine_id: s.machine_id } });
    return true;
  } catch (e) {
    upsertSession({ ...(getSession(s.machine_id, s.session_id) ?? s), model_pick: before });
    toast(t("chat-sessiondialogs.change_failed", { error: errorText(e) }), "error");
    return false;
  }
}

/** DELETE …/model: the conversation follows the default again (another chat: keeps what it runs). */
async function dropPick(s: Session): Promise<boolean> {
  const before = s.model_pick;
  upsertSession({ ...s, model_pick: undefined });
  try {
    await api(conversationModel(s), { method: "DELETE" });
    return true;
  } catch (e) {
    upsertSession({ ...(getSession(s.machine_id, s.session_id) ?? s), model_pick: before });
    toast(t("chat-sessiondialogs.change_failed", { error: errorText(e) }), "error");
    return false;
  }
}

/** POST …/modes {effort} on an older hub (what the effort chip did): drawn at once, put back if it fails. */
async function setEffort(s: Session, effort: string): Promise<boolean> {
  const before = s.effort;
  upsertSession({ ...s, effort });
  try {
    await api(`${sessionPath(s.machine_id, s.session_id)}/modes`, { method: "POST", json: { effort } });
    return true;
  } catch (e) {
    upsertSession({ ...(getSession(s.machine_id, s.session_id) ?? s), effort: before });
    toast(t("chat-sessiondialogs.change_failed", { error: errorText(e) }), "error");
    return false;
  }
}

/** The strip on top: where the setup comes from and, for a pick, the way back. */
export function PickStrip({ chat, setup, named, onBack }: { chat: AssistantChat; setup: AssistantSetup; named: string; onBack?: () => void }) {
  const text = !chat.session
    ? t("chat-modelpill.header_new", { setup: named })
    : setup.source === "pick"
      ? t(chat.kept ? "chat-modelpill.header_picked" : "chat-modelpill.header_picked_page", { setup: named })
      : setup.source === "default"
        ? t("chat-modelpill.header_default", { setup: named })
        : t("chat-modelpill.header_own", { setup: named });
  // the main chat and the new one go back to the default; another chat's pick is undone (it then keeps what it runs)
  const back = chat.session && !chat.isMain ? t("chat-modelpill.undo_pick") : t("chat-modelpill.follow_default");
  return (
    <div data-pick-strip={setup.source} className="flex min-w-0 items-baseline gap-2 border-b border-line px-3.5 py-2 text-[12px] leading-[1.45] text-ink-2">
      <span className="min-w-0 flex-1 break-words">{text}</span>
      {setup.source === "pick" && onBack && (
        <button type="button" onClick={onBack} className="shrink-0 font-medium whitespace-nowrap text-accent-ink hover:underline">
          {back}
        </button>
      )}
    </div>
  );
}

export function AssistantModelPanel({ chat, mid, anchor, onClose }: { chat: AssistantChat; mid: string; anchor: HTMLElement; onClose: () => void }) {
  const version = useModelLists();
  const typed = useStore((s) => hubHas(s.features, TYPED_MODELS_FEATURE));
  const { info } = assistantLaunchers(mid);
  const setup = assistantSetup(chat, info);
  const engine = chat.session?.agent || "claude";
  // a chat that runs keeps its engine; one whose agent isn't known is refused nothing on a guess, as on the hub
  const running = Boolean(chat.session?.agent);
  const fast = Boolean(chat.session?.service_tiers?.some((x) => x.id === "priority"));
  const fastOn = chat.session?.service_tier === "priority";
  const { providers, state } = useMemo(
    () => assistantPanel(setup, info, (name) => modelList(mid, name), { typed, engine, running, fast, fastOn }),
    // eslint-disable-next-line react-hooks/exhaustive-deps -- `version` stands for what was read
    [setup.provider, setup.model, setup.effort, info, mid, typed, engine, running, fast, fastOn, version],
  );
  const latest = () => (chat.session ? (getSession(chat.session.machine_id, chat.session.session_id) ?? chat.session) : undefined);
  const key = assistantPickKey(chat);

  // the page's own pick (an older hub, the new-conversation page): launcher, then its model, then the effort
  const keepHere = (pick: ModelPick) => {
    pickLauncher(key, pick.launcher);
    pickModel(key, pick.model);
    if (!chat.session) pickEffort(key, pick.effort);
  };
  const apply = async (pick: ModelPick): Promise<boolean> => {
    const s = latest();
    if (s && chat.kept) return keepPick(s, pick);
    keepHere(pick);
    // an older hub keeps the effort on the session (what the effort chip did)
    if (s && pick.effort && pick.effort !== s.effort) return setEffort(s, pick.effort);
    return true;
  };
  const onPick = async (config: PanelConfig) => {
    const pick = assistantPickOf(config, modelList(mid, config.provider).list, state.current);
    return pick ? apply(pick) : config.provider === state.current?.provider && config.model === state.current.model;
  };
  const onApplyCurrent = async (patch: { effort?: string; fast?: boolean }) => {
    const s = latest();
    if (patch.fast !== undefined) {
      const speed = s && speedStep(s, patch.fast, true);
      return speed && "service_tier" in speed ? setServiceTier(s, speed.service_tier) : false;
    }
    const now = state.current;
    if (!patch.effort || !now) return false;
    const pick = assistantPickOf({ ...now, effort: patch.effort }, modelList(mid, now.provider).list, null);
    return pick ? apply(pick) : false;
  };
  const back = () => {
    const s = latest();
    if (s && chat.kept) void dropPick(s);
    else {
      pickLauncher(key, undefined);
      pickEffort(key, undefined);
    }
  };
  const onReload = (provider: string) => void readModels(mid, provider, { again: true, first: provider === setup.provider });
  const onForget = (provider: string, model: string) => {
    void forgetModel(mid, provider, model).then((error) => error && toast(error, "error"));
  };
  const launcher = info?.launchers.find((l) => l.name === setup.provider);
  const row = providers.find((p) => p.id === setup.provider)?.models?.find((m) => m.id === state.current?.model);
  const named = [launcher ? providerTitle(launcher) : setup.provider, row?.name].filter(Boolean).join(" · ");

  return (
    <ModelPanel
      anchor={anchor}
      onClose={onClose}
      providers={providers}
      state={state}
      scope="assistant"
      onPick={onPick}
      onApplyCurrent={onApplyCurrent}
      onReload={onReload}
      onForget={onForget}
      header={<PickStrip chat={chat} setup={setup} named={named} onBack={back} />}
    />
  );
}
