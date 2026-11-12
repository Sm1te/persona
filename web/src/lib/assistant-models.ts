// What a Persona chat's model pill says (#5033, docs/web/web.md "Model pill and model panel"; docs/assistant/model-config.md
// "A conversation's own model"): the launcher, model and effort its next message runs on, and where that comes from.
// A Persona chat switches models freely: its own pick is kept on the hub (`model_pick`, feature `assistant_model_pick`)
// and holds from the next message on every device; without one the main chat follows Model configuration › Main chat
// and another chat keeps what it started with. On an older hub a pick lasts for this page and goes with each message
// (lib/assistant-chat.ts), as before; the new-conversation page's pick goes with the conversation's start. First-screen
// code: the panel's own data is lib/assistant-panel.ts, loaded with the panel.
import { agentId } from "./agent-kind";
import { conversationKey, launcherOffered, NEW_CONVERSATION, pickedEffort, pickedLauncher, pickedModel } from "./assistant-chat";
import { modelName } from "./launcher-models";
import { providerTitle } from "./provider-names";
import { currentModelRow, modelLabel, type AssistantLaunchers, type ModelList, type PillView } from "./session-models";
import type { ModelPick, Session } from "./types";

/** The hub keeps a Persona chat's pick (`model_pick`, PUT / DELETE …/conversations/{id}/model). */
export const ASSISTANT_MODEL_PICK_FEATURE = "assistant_model_pick";

/** A Persona chat as its pill sees it. */
export interface AssistantChat {
  /** Absent: the new-conversation page. */
  session?: Session;
  isMain: boolean;
  /** The hub keeps picks (ASSISTANT_MODEL_PICK_FEATURE). */
  kept: boolean;
  /** The assistant's own launcher (AssistantInfo.launcher), for when nothing else says. */
  config?: string;
}

/** Where a chat's setup comes from: its own pick; the default (the main chat, the new-conversation page); or what it
 *  started with (another chat). */
export type SetupSource = "pick" | "default" | "own";

/** What the chat's next message runs on: model absent is the launcher's default entry, effort absent the default's. */
export interface AssistantSetup {
  provider: string;
  model?: string;
  effort?: string;
  source: SetupSource;
}

/** The key of this page's picks for the chat (an older hub's, the new-conversation page's). */
export const assistantPickKey = (c: Pick<AssistantChat, "session">) => (c.session ? conversationKey(c.session.machine_id, c.session.session_id) : NEW_CONVERSATION);

/** The chat's own pick: `model_pick` on a hub that keeps it, else this page's (an older hub, the new-conversation page). */
export function assistantPick(c: AssistantChat): ModelPick | undefined {
  if (c.session && c.kept) return c.session.model_pick?.launcher ? c.session.model_pick : undefined;
  const key = assistantPickKey(c);
  const launcher = pickedLauncher(key);
  if (!launcher) return undefined;
  const out: ModelPick = { launcher };
  const model = pickedModel(key);
  const effort = pickedEffort(key);
  if (model) out.model = model;
  if (effort) out.effort = effort;
  return out;
}

/** The setup the chat's next message runs on (as the hub decides it, assistant/start.go sendModel). */
export function assistantSetup(c: AssistantChat, info: AssistantLaunchers | undefined): AssistantSetup {
  const s = c.session;
  const pick = assistantPick(c);
  if (pick) return { provider: pick.launcher, model: pick.model, effort: pick.effort ?? s?.effort, source: "pick" };
  const def = info?.def;
  if (!s || c.isMain) {
    const provider = def?.provider || c.config || s?.launcher || "claude";
    return { provider, model: def?.model || (s?.launcher === provider ? s.model : undefined), effort: s?.effort ?? (def?.effort || undefined), source: "default" };
  }
  // a launcher the list no longer offers is not what the next message runs on: the hub moves the chat to the assistant's
  const own = launcherOffered(info?.launchers ?? null, s.launcher) ? s.launcher : undefined;
  return { provider: own || c.config || "claude", model: own ? s.model : undefined, effort: s.effort, source: "own" };
}

/** What the pill draws: the launcher (its name for people), the model as its list names it (none set: its default entry,
 *  else the model it is configured with), the effort, Fast. */
export function assistantPillView(c: AssistantChat, setup: AssistantSetup, info: AssistantLaunchers | undefined, e: ModelList): PillView {
  const launcher = info?.launchers.find((l) => l.name === setup.provider);
  const row = currentModelRow(e.list?.models ?? [], setup.model);
  const id = setup.model || launcher?.model;
  const out: PillView = {
    engine: agentId(launcher?.agent_type || launcher?.agent || c.session?.agent),
    provider: launcher ? providerTitle(launcher) : setup.provider,
    loading: e.list === undefined && Boolean(e.loading),
    failed: e.list === undefined && Boolean(e.error),
  };
  const model = row ? modelName(row) : id ? modelLabel(id) : undefined;
  if (model) out.model = model;
  if (setup.effort) out.effort = setup.effort;
  if (c.session?.service_tier === "priority") out.fast = true;
  return out;
}
