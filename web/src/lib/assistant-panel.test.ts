// A Persona chat's model pill and panel (#5033, docs/web/web.md "Model pill and model panel"): where the setup the next
// message runs on comes from (its kept pick, the default, what a side chat started with, this page's pick on an older hub
// or the new-conversation page), what the pill says, the panel's providers (what can run Persona chats, with their
// lists; what can't, off with the hub's reason; another engine than a running chat's, off) and what a pick keeps.
import { afterEach, describe, expect, it } from "vitest";
import { t } from "../i18n";
import { agentName } from "./agent-kind";
import { NEW_CONVERSATION, pickEffort, pickLauncher, pickModel } from "./assistant-chat";
import { assistantPick, assistantPillView, assistantSetup, type AssistantChat } from "./assistant-models";
import { assistantPanel, assistantPickOf } from "./assistant-panel";
import type { LauncherModels } from "./launcher-models";
import type { AssistantLaunchers, ModelList } from "./session-models";
import type { Session } from "./types";

const ALL = ["low", "medium", "high", "xhigh", "max"];
const claudeList: LauncherModels = {
  agent: "claude",
  models: [
    { id: "default", name: "Default (recommended)", default: true, resolved: "claude-opus-5-5", efforts: ALL },
    { id: "opus", name: "Opus 5.5", resolved: "claude-opus-5-5", efforts: ALL },
    { id: "haiku", name: "Haiku 5.5", resolved: "claude-haiku-5-5" },
    { id: "vendor/model-x", name: "vendor/model-x", description: "之前用过的模型 ID", custom: true },
  ],
};
const relayList: LauncherModels = { ...claudeList, locked: "--model" };
const info: AssistantLaunchers = {
  launchers: [
    { name: "claude", source: "builtin" },
    { name: "relay", source: "zshrc", model: "sonnet" },
    { name: "team-api", source: "persona", display_name: "Team API", model: "claude-sonnet-5-5" },
    { name: "codex", source: "builtin", agent: "codex" },
  ],
  def: { provider: "claude", model: "opus", effort: "high" },
  unavailable: [
    { name: "gpt-direct", display_name: "GPT direct", kind: "api", api: "responses", code: "api_protocol", reason: "直连 API 用的是 Responses 协议，跑不了主对话" },
    { name: "far-gw", kind: "launcher", code: "launcher_out_of_scope", reason: "这个服务商不对这台电脑开放" },
  ],
};
const read: Record<string, ModelList> = { claude: { list: claudeList }, relay: { list: relayList }, "team-api": { list: { agent: "claude", models: [] } } };
const listOf = (name: string) => read[name] ?? {};
const session = (x: Partial<Session> = {}): Session =>
  ({ machine_id: "server", session_id: "c1", title: "t", cwd: "/a", status: "idle", state: "idle", assistant: true, agent: "claude", launcher: "relay", model: "claude-sonnet-5-5", effort: "medium", ...x }) as Session;
const chat = (x: Partial<AssistantChat> = {}): AssistantChat => ({ session: session(), isMain: false, kept: true, config: "claude", ...x });

afterEach(() => {
  pickLauncher("server/c1", undefined);
  pickLauncher(NEW_CONVERSATION, undefined);
  pickEffort(NEW_CONVERSATION, undefined);
});

describe("the setup a Persona chat's next message runs on", () => {
  it("its kept pick, on every device; the main chat follows the default without one, another chat keeps what it started with", () => {
    expect(assistantSetup(chat({ session: session({ model_pick: { launcher: "team-api", effort: "low" } }) }), info)).toEqual({ provider: "team-api", model: undefined, effort: "low", source: "pick" });
    expect(assistantSetup(chat({ isMain: true }), info)).toEqual({ provider: "claude", model: "opus", effort: "medium", source: "default" });
    expect(assistantSetup(chat(), info)).toEqual({ provider: "relay", model: "claude-sonnet-5-5", effort: "medium", source: "own" });
    // a launcher the list no longer offers is not what it runs on: the assistant's
    expect(assistantSetup(chat({ session: session({ launcher: "gone", model: "kimi" }) }), info)).toEqual({ provider: "claude", model: undefined, effort: "medium", source: "own" });
  });

  it("an older hub keeps this page's pick; the new-conversation page has its own, the default otherwise", () => {
    pickLauncher("server/c1", "team-api");
    pickModel("server/c1", "claude-haiku-5-5");
    expect(assistantPick(chat({ kept: false }))).toEqual({ launcher: "team-api", model: "claude-haiku-5-5" });
    expect(assistantPick(chat())).toBeUndefined(); // a hub that keeps picks: only model_pick counts
    expect(assistantSetup(chat({ session: undefined }), info)).toEqual({ provider: "claude", model: "opus", effort: "high", source: "default" });
    pickLauncher(NEW_CONVERSATION, "relay");
    pickEffort(NEW_CONVERSATION, "max");
    expect(assistantSetup(chat({ session: undefined }), info)).toEqual({ provider: "relay", model: undefined, effort: "max", source: "pick" });
  });

  it("the pill: the provider's name for people, its list's name for the model (none set: the default entry, else the model it is set up with)", () => {
    const view = (c: AssistantChat) => assistantPillView(c, assistantSetup(c, info), info, listOf(assistantSetup(c, info).provider));
    expect(view(chat({ isMain: true }))).toMatchObject({ engine: "claude", provider: "claude", model: "Opus 5.5", effort: "medium" });
    expect(view(chat({ session: session({ model_pick: { launcher: "team-api" } }) }))).toMatchObject({ provider: "Team API", model: "sonnet 5.5" });
    expect(view(chat({ session: session({ model_pick: { launcher: "claude" } }) })).model).toBe("Default (recommended)");
    expect(view(chat({ session: session({ model_pick: { launcher: "codex", model: "gpt-5.5" } }) }))).toMatchObject({ engine: "codex", model: "5.5" });
  });
});

describe("the panel's providers", () => {
  it("what can run Persona chats, each with its list; what can't, off with the hub's reason (not hidden)", () => {
    const { providers, state } = assistantPanel(assistantSetup(chat({ isMain: true }), info), info, listOf, { typed: true });
    expect(providers.map((p) => [p.id, p.name, p.engine, p.unavailable ?? ""])).toEqual([
      ["claude", "claude", "claude", ""],
      ["relay", "relay", "claude", ""],
      ["team-api", "Team API", "claude", ""],
      ["codex", "codex", "codex", ""],
      ["gpt-direct", "GPT direct", "claude", "直连 API 用的是 Responses 协议，跑不了主对话"],
      ["far-gw", "far-gw", "claude", "这个服务商不对这台电脑开放"],
    ]);
    expect(providers.find((p) => p.id === "gpt-direct")!.models).toBeNull();
    // the provider it runs on is the one whose models switch "at once" (Recommended); its row is the default's model
    expect(state).toEqual({ current: { provider: "claude", model: "opus", effort: "medium" }, recommended: ["claude"] });
    expect(providers.find((p) => p.id === "codex")).toMatchObject({ loading: true, models: null });
  });

  it("an ID typed before is listed (and can be forgotten); typed IDs only where the hub keeps them; a launcher that fixes its model offers its default only", () => {
    const { providers } = assistantPanel(assistantSetup(chat({ isMain: true }), info), info, listOf, { typed: true });
    const claude = providers.find((p) => p.id === "claude")!;
    expect(claude.models!.find((m) => m.id === "vendor/model-x")).toMatchObject({ custom: true, efforts: ALL });
    expect(claude.typable).toBe(true);
    expect(assistantPanel(assistantSetup(chat({ isMain: true }), info), info, listOf, { typed: false }).providers[0].typable).toBeUndefined();
    const relay = providers.find((p) => p.id === "relay")!;
    expect(relay.models!.map((m) => m.id)).toEqual(["default"]);
    expect(relay.typable).toBeUndefined();
    // a direct API whose list is empty: the model it is set up with
    expect(providers.find((p) => p.id === "team-api")!.models).toEqual([{ id: "claude-sonnet-5-5", name: "sonnet 5.5", efforts: ALL }]);
  });

  it("a chat that runs keeps its engine: another engine's providers are off with why, as in an Agent's panel; the new-chat page offers every kind", () => {
    const pickable = (ps: { id: string; unavailable?: string }[]) => ps.filter((p) => !p.unavailable).map((p) => p.id);
    const claudeChat = assistantPanel(assistantSetup(chat({ isMain: true }), info), info, listOf, { typed: true, engine: "claude", running: true }).providers;
    expect(claudeChat.find((p) => p.id === "codex")).toMatchObject({ unavailable: t("chat-modelpanel.other_engine", { from: agentName("claude"), to: agentName("codex") }) });
    expect(claudeChat.find((p) => p.id === "codex")!.typable).toBeUndefined();
    expect(pickable(claudeChat)).toEqual(["claude", "relay", "team-api"]);
    // the hub's own reasons stay theirs
    expect(claudeChat.find((p) => p.id === "far-gw")!.unavailable).toBe("这个服务商不对这台电脑开放");
    // a Codex chat: the Claude Code providers are the other engine; the one it runs on stays
    const c = chat({ session: session({ agent: "codex", launcher: "codex", model: "gpt-5.5" }) });
    const codexChat = assistantPanel(assistantSetup(c, info), info, listOf, { engine: "codex", running: true }).providers;
    expect(pickable(codexChat)).toEqual(["codex"]);
    expect(codexChat.find((p) => p.id === "relay")!.unavailable).toBe(t("chat-modelpanel.other_engine", { from: agentName("codex"), to: agentName("claude") }));
    // the new-chat page runs nothing yet
    expect(pickable(assistantPanel(assistantSetup(chat({ session: undefined }), info), info, listOf, { typed: true }).providers)).toEqual(["claude", "relay", "team-api", "codex"]);
  });

  it("a pick whose launcher is gone stays shown, off, with why", () => {
    const c = chat({ session: session({ model_pick: { launcher: "old-gw", model: "glm-5" } }) });
    const { providers, state } = assistantPanel(assistantSetup(c, info), info, listOf, {});
    expect(providers[0]).toMatchObject({ id: "old-gw", unavailable: t("chat-modelpill.provider_gone"), models: [{ id: "glm-5" }] });
    expect(state.current).toEqual({ provider: "old-gw", model: "glm-5", effort: "medium" });
    expect(state.recommended).toEqual([]);
  });

  it("Codex's Fast on the model it runs, as it runs", () => {
    const c = chat({ session: session({ launcher: "codex", model: "gpt-5.5", service_tier: "priority" }) });
    const withList = (name: string) => (name === "codex" ? { list: { agent: "codex", models: [{ id: "gpt-5.5", name: "GPT-5.5", efforts: ["low", "high"] }] } } : listOf(name));
    const { providers, state } = assistantPanel(assistantSetup(c, info), info, withList, { fast: true, fastOn: true });
    expect(providers.find((p) => p.id === "codex")!.models![0].fast).toBe(true);
    expect(state.current).toMatchObject({ provider: "codex", model: "gpt-5.5", fast: true });
  });
});

describe("what a pick keeps", () => {
  const now = { provider: "claude", model: "opus", effort: "medium" };
  it("the launcher, the model unless it is the default entry, the effort the setup has", () => {
    expect(assistantPickOf({ provider: "claude", model: "haiku", effort: "low" }, claudeList, now)).toEqual({ launcher: "claude", model: "haiku", effort: "low" });
    expect(assistantPickOf({ provider: "claude", model: "default" }, claudeList, now)).toEqual({ launcher: "claude" });
    expect(assistantPickOf({ provider: "team-api", model: "claude-sonnet-5-5" }, { agent: "claude", models: [] }, now)).toEqual({ launcher: "team-api", model: "claude-sonnet-5-5" });
    expect(assistantPickOf({ provider: "claude", model: "~vendor/model-latest[1m]" }, claudeList, now)).toEqual({ launcher: "claude", model: "~vendor/model-latest[1m]" });
  });

  it("nothing for an ID the hub would refuse, or the setup the chat runs now", () => {
    expect(assistantPickOf({ provider: "claude", model: "vendor model" }, claudeList, now)).toBeNull();
    expect(assistantPickOf({ provider: "claude", model: "opus", effort: "medium" }, claudeList, now)).toBeNull();
    expect(assistantPickOf({ provider: "claude", model: "opus", effort: "max" }, claudeList, now)).toEqual({ launcher: "claude", model: "opus", effort: "max" });
  });
});
