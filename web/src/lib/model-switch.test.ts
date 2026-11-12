import { afterEach, describe, expect, it, vi } from "vitest";
import type { AgentModel } from "./launcher-models";
import { canSwitchModel, isCurrentModel, isModelCommand, modelBusy, onModelPicker, requestModelPicker, sessionLauncher } from "./model-switch";
import type { Session } from "./types";

const session = (extra: Partial<Session> = {}): Session => ({ machine_id: "m", session_id: "s", status: "idle", agent: "claude", ...extra }) as Session;
const all = ["low", "medium", "high", "xhigh", "max"];
const models: AgentModel[] = [
  { id: "default", name: "Default", default: true, resolved: "claude-opus-5-5", efforts: all },
  { id: "opus", name: "Opus 5.5", resolved: "claude-opus-5-5", efforts: all },
  { id: "haiku", name: "Haiku 5.5", resolved: "claude-haiku-5-5" },
  { id: "gateway/glm", name: "glm", efforts: ["low", "high"] },
];

describe("which sessions can switch model", () => {
  it("is a Claude Code terminal session that takes messages", () => {
    expect(canSwitchModel(session())).toBe(true);
    expect(canSwitchModel(session({ agent: undefined }))).toBe(true);
  });

  it("is not Codex, a runtime's, a Persona conversation, an app-server thread or an offline session", () => {
    expect(canSwitchModel(undefined)).toBe(false);
    expect(canSwitchModel(session({ agent: "codex" }))).toBe(false);
    expect(canSwitchModel(session({ agent: "traex" }))).toBe(false);
    expect(canSwitchModel(session({ assistant: true }))).toBe(false);
    expect(canSwitchModel(session({ source_kind: "codex_app_server", capabilities: { send: true } as Session["capabilities"] }))).toBe(false);
    expect(canSwitchModel(session({ status: "offline" }))).toBe(false);
  });

  it("honors explicit runtime model capability and its launcher", () => {
    const agy = session({ agent: "agy", source_kind: "runtime", launcher: "agy", capabilities: { send: true, model: true } as Session["capabilities"] });
    expect(canSwitchModel(agy)).toBe(true);
    expect(sessionLauncher(agy)).toBe("agy");
    expect(canSwitchModel({ ...agy, capabilities: { ...agy.capabilities!, model: false } })).toBe(false);
    expect(canSwitchModel({ ...agy, status: "offline" })).toBe(false);
    expect(canSwitchModel({ ...agy, assistant: true })).toBe(false);
  });

  it("is busy while the agent works or waits on you", () => {
    expect(modelBusy(session())).toBe("");
    expect(modelBusy(session({ status: "working" }))).toContain("正在工作");
    expect(modelBusy(session({ state: "needs_you" }))).toContain("正在工作");
  });

  it("asks the launcher the session runs with, plain claude without one", () => {
    expect(sessionLauncher({ launcher: "gateway" })).toBe("gateway");
    expect(sessionLauncher({})).toBe("claude");
  });
});

describe("the current model", () => {
  it("matches the list's id or what it stands for, ignoring case", () => {
    expect(isCurrentModel(models[1], "opus")).toBe(true);
    expect(isCurrentModel(models[1], "Claude-Opus-5-5")).toBe(true);
    expect(isCurrentModel(models[2], "claude-opus-5-5")).toBe(false);
    expect(isCurrentModel(models[1], undefined)).toBe(false);
    expect(isCurrentModel(models[1], "")).toBe(false);
  });
});

describe("the /model command", () => {
  it("is /model alone", () => {
    expect(isModelCommand("/model")).toBe(true);
    expect(isModelCommand("/model ")).toBe(true);
    expect(isModelCommand("  /model  ")).toBe(true);
    expect(isModelCommand("/model opus")).toBe(false);
    expect(isModelCommand("/models")).toBe(false);
    expect(isModelCommand("use /model")).toBe(false);
  });
});

describe("opening the panel from the composer", () => {
  afterEach(() => vi.restoreAllMocks());

  it("reaches the conversation's pill and nobody else's", () => {
    const open = vi.fn();
    const off = onModelPicker("m/s", open);
    expect(requestModelPicker("m/other")).toBe(false);
    expect(requestModelPicker("m/s")).toBe(true);
    expect(open).toHaveBeenCalledTimes(1);
    off();
    expect(requestModelPicker("m/s")).toBe(false); // an agent without a switch: the composer sends as before
  });

  it("keeps the newer pill when an older one unsubscribes late", () => {
    const a = vi.fn(), b = vi.fn();
    const offA = onModelPicker("m/s", a);
    const offB = onModelPicker("m/s", b);
    offA();
    expect(requestModelPicker("m/s")).toBe(true);
    expect(b).toHaveBeenCalled();
    offB();
  });
});
