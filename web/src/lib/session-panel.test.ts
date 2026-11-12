// Switching a running Claude Code Agent to another launcher of its engine (#5035, docs/web/web.md "Model pill and model
// panel"): on a hub with `launcher_switch` the other launchers' models are picked like the session's own, a pick there is
// one POST …/modes {launcher, model?, effort?} that the next message carries out, the pending switch is the panel's
// `pending` and its warning note, picking the session's own launcher drops it first; another engine, a Codex thread and
// an older hub stay as before. Also what the pill says of the pending switch, and which send refusals are the switch's.
import { describe, expect, it } from "vitest";
import { t } from "../i18n";
import { ApiError } from "./api";
import type { LauncherModels } from "./launcher-models";
import { pendingView, switchRefused, type ModelList } from "./session-models";
import { pickPlan, sessionPanel, type PanelOptions } from "./session-panel";
import type { Launcher, Session } from "./types";

const ALL = ["low", "medium", "high", "xhigh", "max"];
const claudeList: LauncherModels = {
  agent: "claude",
  models: [
    { id: "default", name: "Default (recommended)", default: true, resolved: "claude-opus-5-5", efforts: ALL },
    { id: "opus", name: "Opus 5.5", resolved: "claude-opus-5-5", efforts: ALL },
    { id: "haiku", name: "Haiku 5.5", resolved: "claude-haiku-5-5", efforts: ["low", "high"] },
  ],
};
const workList: LauncherModels = {
  agent: "claude",
  models: [
    { id: "default", name: "Default (recommended)", default: true, resolved: "glm-5.3", efforts: ALL },
    { id: "glm-5.3", name: "GLM 5.3", efforts: ["low", "high"] },
  ],
};
const lockedList: LauncherModels = { ...claudeList, locked: "--model" };
const codexList: LauncherModels = { agent: "codex", models: [{ id: "gpt-5.5", name: "GPT-5.5", default: true }] };
const launchers: Launcher[] = [{ name: "claude" }, { name: "work" }, { name: "pinned" }, { name: "cx", agent: "codex" }];
const read: Record<string, ModelList> = { claude: { list: claudeList }, work: { list: workList }, pinned: { list: lockedList }, cx: { list: codexList } };
const listOf = (name: string) => read[name] ?? {};
const raw = (name: string) => read[name]?.list;

const session = (x: Partial<Session> = {}): Session =>
  ({ machine_id: "mac", session_id: "s1", title: "t", cwd: "/code", status: "idle", state: "idle", agent: "claude", launcher: "claude", model: "claude-opus-5-5", effort: "high", ...x }) as Session;
const on: PanelOptions = { switching: true };
const provider = (s: Session, id: string, o: PanelOptions = on) => sessionPanel(s, launchers, listOf, o).providers.find((p) => p.id === id)!;

describe("another launcher of the engine", () => {
  it("is picked like the session's own on a hub that switches, and stays off on an older one", () => {
    const work = provider(session(), "work");
    expect(work.unavailable).toBeUndefined();
    expect(work.locked).toBeUndefined();
    expect(work.typable).toBe(true);
    expect(work.models!.map((m) => m.id)).toEqual(["default", "glm-5.3"]);
    expect(provider(session(), "work", {}).unavailable).toBe(t("chat-modelpanel.other_provider"));
    // the models that switch at once are still the session's own launcher's
    expect(sessionPanel(session(), launchers, listOf, on).state.recommended).toEqual(["claude"]);
  });

  it("another engine, a Codex thread and an agent that can't switch its model keep their reasons", () => {
    expect(provider(session(), "cx").unavailable).toBe(t("chat-modelpanel.other_engine", { from: "Claude Code", to: "Codex" }));
    const codex = session({ agent: "codex", source_kind: "codex_app_server", launcher: "cx", capabilities: { send: true }, model: "gpt-5.5", efforts: ["low", "high"] });
    expect(provider(codex, "cx").unavailable).toBe(t("chat-modelpanel.model_fixed", { agent: "Codex" }));
    expect(provider(codex, "work").unavailable).toBe(t("chat-modelpanel.other_engine", { from: "Codex", to: "Claude Code" }));
    expect(provider(session({ status: "offline", state: "offline" }), "work").unavailable).toBe(t("chat-modelpanel.other_provider"));
  });

  it("one that fixes its own model offers what it runs by default, only", () => {
    const pinned = provider(session(), "pinned");
    expect(pinned.models!.map((m) => m.id)).toEqual(["default"]);
    expect(pinned.locked).toBeUndefined();
    expect(pinned.typable).toBeUndefined();
  });
});

describe("a switch that waits for the next message", () => {
  it("is the panel's pending setup, with the warning note; none for the session's own launcher", () => {
    const { state } = sessionPanel(session({ pending_switch: { launcher: "work", model: "glm-5.3", effort: "low", at: "2026-11-12T10:00:00Z" } }), launchers, listOf, on);
    expect(state.pending).toEqual({ provider: "work", model: "glm-5.3", effort: "low" });
    expect(state.note).toBe(t("chat-modelpanel.pending_note", { provider: "work" }));
    // no model picked: the launcher's default entry
    expect(sessionPanel(session({ pending_switch: { launcher: "work" } }), launchers, listOf, on).state.pending).toEqual({ provider: "work", model: "default" });
    const none = sessionPanel(session({ pending_switch: { launcher: "claude" } }), launchers, listOf, on).state;
    expect(none.pending).toBeUndefined();
    expect(none.note).toBeUndefined();
  });

  it("the pill names it: the launcher, its list's name for the model (the default when none was picked), the effort", () => {
    expect(pendingView(session({ pending_switch: { launcher: "work", model: "glm-5.3" } }), listOf)).toEqual({ engine: "claude", provider: "work", model: "GLM 5.3", effort: "high" });
    expect(pendingView(session({ pending_switch: { launcher: "work", effort: "low" } }), listOf)).toEqual({ engine: "claude", provider: "work", model: "Default (recommended)", effort: "low" });
    // its list not read: a short form of the ID, else "Default model"
    expect(pendingView(session({ pending_switch: { launcher: "relay", model: "claude-sonnet-5-5" } }), () => ({}))!.model).toBe("sonnet 5.5");
    expect(pendingView(session({ pending_switch: { launcher: "relay" } }), () => ({}))!.model).toBe(t("chat-modelpill.default_model"));
    expect(pendingView(session(), listOf)).toBeNull();
    expect(pendingView(session({ pending_switch: { launcher: "claude" } }), listOf)).toBeNull();
  });
});

describe("what a pick on another launcher sends", () => {
  it("one request recording the switch: the model unless it is that launcher's default, the effort when the setup has one", () => {
    expect(pickPlan(session(), { provider: "work", model: "glm-5.3", effort: "low" }, raw, on)).toEqual([{ launcher: "work", from: "claude", model: "glm-5.3", effort: "low" }]);
    expect(pickPlan(session(), { provider: "work", model: "default" }, raw, on)).toEqual([{ launcher: "work", from: "claude" }]);
    // a model ID typed by hand goes as it is; one the hub would refuse doesn't go
    expect(pickPlan(session(), { provider: "work", model: "~vendor/next" }, raw, on)).toEqual([{ launcher: "work", from: "claude", model: "~vendor/next" }]);
    expect(pickPlan(session(), { provider: "work", model: "a;b" }, raw, on)).toBeNull();
    // the launcher that fixes its model takes its default only
    expect(pickPlan(session(), { provider: "pinned", model: "default" }, raw, on)).toEqual([{ launcher: "pinned", from: "claude" }]);
    expect(pickPlan(session(), { provider: "pinned", model: "haiku" }, raw, on)).toBeNull();
  });

  it("nothing on an older hub, to another engine, from a Codex thread, or when that very switch waits already", () => {
    expect(pickPlan(session(), { provider: "work", model: "glm-5.3" }, raw, {})).toBeNull();
    expect(pickPlan(session(), { provider: "cx", model: "gpt-5.5" }, raw, on)).toBeNull();
    const codex = session({ agent: "codex", source_kind: "codex_app_server", launcher: "cx", capabilities: { send: true }, model: "gpt-5.5" });
    expect(pickPlan(codex, { provider: "work", model: "glm-5.3" }, raw, on)).toBeNull();
    const waiting = session({ pending_switch: { launcher: "work", model: "glm-5.3", effort: "low" } });
    expect(pickPlan(waiting, { provider: "work", model: "glm-5.3", effort: "low" }, raw, on)).toEqual([]);
    expect(pickPlan(waiting, { provider: "work", model: "glm-5.3", effort: "high" }, raw, on)).toEqual([{ launcher: "work", from: "claude", model: "glm-5.3", effort: "high" }]);
  });

  it("the session's own launcher while a switch waits: drops it first, then the usual steps", () => {
    const waiting = session({ pending_switch: { launcher: "work" } });
    expect(pickPlan(waiting, { provider: "claude", model: "opus", effort: "high" }, raw, on)).toEqual([{ launcher: "claude", from: "claude" }]);
    expect(pickPlan(waiting, { provider: "claude", model: "haiku", effort: "low" }, raw, on)).toEqual([{ launcher: "claude", from: "claude" }, { model: "haiku" }, { effort: "low" }]);
    expect(pickPlan(session(), { provider: "claude", model: "haiku" }, raw, on)).toEqual([{ model: "haiku" }]);
  });
});

describe("a message the switch refused", () => {
  it("is a 409 with a switch_ code; others are not", () => {
    expect(switchRefused(new ApiError(409, "gone", { code: "switch_launcher_missing", delivered: "none" }))).toBe(true);
    expect(switchRefused(new ApiError(409, "failed", { code: "switch_failed_stopped", delivered: "none" }))).toBe(true);
    expect(switchRefused(new ApiError(409, "draft", { code: "input_draft", delivered: "none" }))).toBe(false);
    expect(switchRefused(new Error("x"))).toBe(false);
  });
});
