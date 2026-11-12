// A Persona conversation's own controls (the app's AssistantTopBar): what "Copy info" copies, and the picks of its model
// pill kept in this page (views/chat/ModelPill.tsx, #5033). On a hub with `assistant_model_pick` a conversation's pick is
// kept on the hub (`model_pick`) and nothing here is used for it. On an older hub a pick holds for that conversation's next
// messages, sent as `launcher` / `model` with each (POST …/messages), and the hub resumes the conversation on it; it lasts
// for this page's life, like the app's `assistantLaunchers`. The new-conversation page has its own pick under
// NEW_CONVERSATION (launcher, model and effort), sent with POST /assistant/conversations.
import { useSyncExternalStore } from "react";
import { t } from "../i18n";
import { managedProvider } from "./compatibility";
import { shellPath } from "./format";
import { modelName } from "./launcher-models";

export const NEW_CONVERSATION = "new";

/** A launcher as GET /machines/{mid}/launchers lists it (the fields the assistant needs). */
export interface AssistantLauncher {
  name: string;
  /** A Persona-managed provider's name for people (lib/provider-names.ts). */
  display_name?: string;
  model?: string;
  agent?: string;
  agent_type?: string;
  base_url?: string;
  assistant?: boolean;
  source?: string;
}

/** Runs Claude Code, Codex, or an assistant-capable runtime (such as Antigravity): what the assistant can use, as the hub's
 *  `?purpose=assistant` list (assistant/launcher_mainchat.go). A compatible CLI is left out; so are a runtime's launcher that
 *  doesn't say it runs the assistant (a Paseo provider is no shell command) and a managed Codex launcher (`personad launch`
 *  runs codex past the app-server the assistant needs). */
export function runsAssistant(l: AssistantLauncher): boolean {
  if (l.assistant) return true;
  if (l.base_url === "(traex)") return false;
  const machine = !l.source || l.source === "zshrc" || l.source === "path" || l.source === "builtin";
  if (!machine && !managedProvider(l.source)) return false;
  if (managedProvider(l.source) && l.agent === "codex") return false;
  return !l.agent || l.agent === "claude" || l.agent === "codex" || l.agent === "agy";
}

/** Whether the assistant's launcher list offers name. A list not loaded yet (null) offers everything: nothing is dropped
 *  on a guess. A loaded list, empty included, offers only its entries and plain claude: a pick it doesn't offer would be
 *  refused, and a conversation left on one moves to the main chat's. */
export function launcherOffered(list: AssistantLauncher[] | null | undefined, name?: string): boolean {
  return !name || name === "claude" || !list || list.some((l) => l.name === name);
}

/**
 * The launcher a conversation's next message uses (AssistantTopBar.launcher): the main conversation follows Settings › Assistant
 * (the hub resumes it there) unless picked here; a side one keeps its own; the new-conversation page the default.
 */
export function effectiveLauncher(o: { isNew?: boolean; isMain?: boolean; picked?: string; session?: string; config?: string }): string {
  if (o.isNew) return o.picked || o.config || "claude";
  if (o.isMain) return o.picked || o.config || o.session || "claude";
  return o.picked || o.session || o.config || "claude";
}

/** The model a launcher names, shortened (Fmt.model): the last path part, with raw GPT ids made readable. */
export function shortModel(model?: string): string {
  return modelName({ id: model ? model.split("/").pop() ?? model : "" });
}

/** What "Copy info" copies (AssistantCopyText.info): enough for another agent, or a terminal, to find the conversation. */
export function assistantInfoText(o: { title?: string; isMain: boolean; sessionId: string; machine: string; machineId: string; cwd: string; launcher: string; model?: string }): string {
  const name = [o.isMain ? t("lib-assistant-chat.main_chat") : "", o.title ? t("lib-assistant-chat.quoted_title", { title: o.title }) : ""].filter(Boolean).join(" ");
  const machine = o.machine && o.machine !== o.machineId ? t("lib-assistant-chat.machine_with_id", { machine: o.machine, id: o.machineId }) : o.machineId;
  return [
    name ? t("lib-assistant-chat.info_title_named", { name }) : t("lib-assistant-chat.info_title"),
    t("lib-assistant-chat.info_session", { id: o.sessionId }),
    t("lib-assistant-chat.info_machine", { machine }),
    t("lib-assistant-chat.info_cwd", { cwd: o.cwd }),
    o.model ? t("lib-assistant-chat.info_launcher_model", { launcher: o.launcher, model: o.model }) : t("lib-assistant-chat.info_launcher", { launcher: o.launcher }),
    t("lib-assistant-chat.info_resume", { command: `${o.cwd ? `cd ${shellPath(o.cwd)} && ` : ""}claude --resume ${o.sessionId}` }),
  ].join("\n");
}

const picks = new Map<string, string>();
/** The model picked for the next messages, with the launcher pick (`pickedModel`); absent: the launcher's default. */
const models = new Map<string, string>();
const listeners = new Set<() => void>();

export function conversationKey(mid: string, sid: string) {
  return `${mid}/${sid}`;
}

/** This conversation's pick, if any (key: conversationKey, or NEW_CONVERSATION). */
export function pickedLauncher(key: string): string | undefined {
  return picks.get(key);
}

export function pickLauncher(key: string, name: string | undefined) {
  if (name) picks.set(key, name);
  else picks.delete(key);
  models.delete(key); // another launcher: back to its default model
  listeners.forEach((l) => l());
}

/** The model picked for key, if any. */
export function pickedModel(key: string): string | undefined {
  return models.get(key);
}

/** Pick a model for key (undefined: the launcher's default); the launcher pick stays. */
export function pickModel(key: string, model: string | undefined) {
  if (model) models.set(key, model);
  else models.delete(key);
  listeners.forEach((l) => l());
}

export function usePickedModel(key: string): string | undefined {
  return useSyncExternalStore(subscribe, () => models.get(key));
}

/** The reasoning effort picked for key (the new-conversation page's, sent with its start); absent: the default's. */
const efforts = new Map<string, string>();

export function pickedEffort(key: string): string | undefined {
  return efforts.get(key);
}

export function pickEffort(key: string, effort: string | undefined) {
  if (effort) efforts.set(key, effort);
  else efforts.delete(key);
  listeners.forEach((l) => l());
}

export function usePickedEffort(key: string): string | undefined {
  return useSyncExternalStore(subscribe, () => efforts.get(key));
}

/** Drop key's whole pick, launcher, model and effort together: none of it outlives the others (the new-conversation
 *  page's would go with the start, and a hub that keeps picks would keep what nobody chose). */
export function dropPick(key: string) {
  picks.delete(key);
  models.delete(key);
  efforts.delete(key);
  listeners.forEach((l) => l());
}

function subscribe(l: () => void) {
  listeners.add(l);
  return () => {
    listeners.delete(l);
  };
}

export function usePickedLauncher(key: string): string | undefined {
  return useSyncExternalStore(subscribe, () => picks.get(key));
}
