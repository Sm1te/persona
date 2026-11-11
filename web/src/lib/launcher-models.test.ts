import { afterEach, describe, expect, it, vi } from "vitest";
import {
  customModel, defaultDetail, defaultLabel, effortChoices, fitEffort, followedBinding, forgetCustomModel, launcherAgent, launcherModels, modelDetail, modelName, modelRows,
  pickerLauncher, machineLaunchers, oneOff, providerModels, startPicks, takesTypedModel, tierLeftOut, typedModelOffered, withoutCustom, type AgentModel, type LauncherModels,
} from "./launcher-models";

const all = ["low", "medium", "high", "xhigh", "max"];
const claude: LauncherModels = { agent: "claude", models: [
  { id: "default", name: "Default (recommended)", description: "Use the default model", resolved: "claude-opus-5-5", default: true, efforts: all },
  { id: "opus", name: "Opus 5.5", description: "Best for everyday complex work", resolved: "claude-opus-5-5", efforts: all },
  { id: "haiku", name: "Haiku 5.5", resolved: "claude-haiku-5-5" },
  { id: "gateway/glm-5.3", name: "glm-5.3", description: "From gateway", efforts: ["low", "medium", "high"] },
] };
const codex: LauncherModels = { agent: "codex", models: [
  { id: "gpt-5.5", name: "GPT-5.5", default: true, efforts: ["low", "medium", "high"], default_effort: "medium" },
  { id: "gpt-5.5-codex", name: "GPT-5.5-Codex", description: "Tuned for long agentic coding", efforts: ["low", "medium", "high"] },
] };
const ids = (rows: AgentModel[]) => rows.map((m) => m.id);

afterEach(() => vi.unstubAllGlobals());

describe("哪个启动器的列表", () => {
  it("the chosen launcher; following 模型配置, the one it runs", () => {
    expect(pickerLauncher("codex", {})).toBe("codex");
    expect(pickerLauncher("", followedBinding("", { provider: "launcher.claude", model: "m" }))).toBe("claude");
    expect(pickerLauncher("", followedBinding("", { provider: "legacy.ai" }))).toBe("");
    expect(pickerLauncher("", followedBinding("", undefined))).toBe("");
  });

  it("keeps 模型配置's model only while its launcher is the one that runs", () => {
    const effective = { provider: "launcher.claude", model: "model-chat", effort: "high" };
    expect(followedBinding("", effective)).toBe(effective);
    expect(followedBinding("claude", effective)).toBe(effective);
    expect(followedBinding("codex", effective)).toEqual({});
  });

  it("knows the agent from the list, else from the launcher list", () => {
    const launchers = [{ name: "claude" }, { name: "codex", agent: "codex" }, { name: "wrapped", agent_type: "traex" }];
    expect(launcherAgent("codex", launchers, null)).toBe("codex");
    expect(launcherAgent("wrapped", launchers)).toBe("traex");
    expect(launcherAgent("claude", launchers)).toBe("claude");
    expect(launcherAgent("unknown", launchers)).toBe("claude");
    expect(launcherAgent("claude", launchers, { agent: "codex", models: [] })).toBe("codex");
  });
});

describe("模型显示名称", () => {
  it.each([
    ["gpt-6.1-sol", "6.1 Sol"],
    ["gpt-6.1", "6.1"],
    ["gpt-5.5-codex", "5.5 Codex"],
    ["gpt-4o-mini", "4o Mini"],
    ["gpt-4.1-2025-04-14", "4.1 2025 04 14"],
    ["gpt-oss-120b", "oss 120b"],
    ["claude-opus-5-5", "claude-opus-5-5"],
    ["model-x", "model-x"],
    ["gpt-", "gpt-"],
    ["gpt-6.1--sol", "gpt-6.1--sol"],
    ["", ""],
  ])("formats %s as %s without a model catalog", (id, label) => {
    expect(modelName({ id })).toBe(label);
    expect(modelName({ id, name: id })).toBe(label);
  });

  it("keeps explicit aliases; empty names fall back to the id", () => {
    expect(modelName({ id: "gpt-6.1-sol", name: "我的模型" })).toBe("我的模型");
    expect(modelName({ id: "gpt-6.1-sol", name: "GPT 专用" })).toBe("GPT 专用");
    expect(modelName({ id: "gpt-6.1-sol", name: "gpt-4o-mini" })).toBe("gpt-4o-mini");
    expect(modelName({ id: "gpt-6.1-sol", name: "GPT-6.1-Sol" })).toBe("6.1 Sol");
    expect(modelName({ id: "gpt-6.1", name: "" })).toBe("6.1");
    expect(modelName({ id: "gpt-6.1", name: "   " })).toBe("6.1");
    expect(modelName({ id: "", name: "默认" })).toBe("默认");
    expect(modelName(claude.models[1])).toBe("Opus 5.5");
  });

  it("searches the displayed words and keeps picks and details as raw ids", () => {
    const model = { id: "gpt-6.1-sol", name: "gpt-6.1-sol", efforts: ["high"] };
    const list = { agent: "claude", models: [model] };
    expect(modelRows(list.models, "6.1 Sol").rows).toEqual([model]);
    expect(modelDetail(model)).toBe("");
    expect(modelDetail({ ...model, name: "我的模型" })).toBe(model.id);
    expect(startPicks({ agent: "claude", list, model: model.id, effort: "high" })).toEqual({ model: "gpt-6.1-sol", effort: "high" });
    expect(model).toEqual({ id: "gpt-6.1-sol", name: "gpt-6.1-sol", efforts: ["high"] });
  });
});

describe("列表", () => {
  it("「默认」 says what it runs; rows add the id or concrete model only when the name doesn't say it", () => {
    expect(defaultDetail(claude.models, {})).toBe("claude-opus-5-5");
    expect(defaultDetail(codex.models, {})).toBe("gpt-5.5");
    expect(defaultDetail(claude.models, { provider: "launcher.claude", model: "model-chat" })).toBe("model-chat");
    expect(defaultDetail([{ id: "default", name: "Default", default: true }], {})).toBe("");
    expect(modelDetail(claude.models[1])).toBe("claude-opus-5-5");
    expect(modelDetail(claude.models[3])).toBe("gateway/glm-5.3");
    expect(modelDetail(codex.models[1])).toBe("");
  });

  it("puts 「默认」 first and searches name, id, concrete model and description by every word", () => {
    expect(modelRows(claude.models, "")).toEqual({ showDefault: true, rows: claude.models.slice(1) });
    expect(ids(modelRows(claude.models, "GATEWAY").rows)).toEqual(["gateway/glm-5.3"]);
    expect(ids(modelRows(claude.models, "claude 5-5").rows)).toEqual(["opus", "haiku"]);
    expect(modelRows(claude.models, "opus")).toMatchObject({ showDefault: true });
    expect(modelRows(claude.models, "haiku")).toMatchObject({ showDefault: false });
    expect(modelRows(claude.models, "默认")).toEqual({ showDefault: true, rows: [] });
    expect(modelRows(claude.models, "chat", { model: "model-chat" }).showDefault).toBe(true);
    expect(modelRows(codex.models, "agentic").rows.map((m) => m.name)).toEqual(["GPT-5.5-Codex"]);
  });

  it("asks the launcher (again with refresh); an older hub has no list", async () => {
    vi.stubGlobal("localStorage", { getItem: () => "mock" });
    const fetcher = vi.fn()
      .mockResolvedValueOnce({ ok: true, status: 200, text: async () => JSON.stringify({ agent: "traex" }) })
      .mockResolvedValueOnce({ ok: true, status: 200, text: async () => JSON.stringify(claude) })
      .mockResolvedValueOnce({ ok: false, status: 404, text: async () => "404 page not found" })
      .mockResolvedValueOnce({ ok: false, status: 502, text: async () => JSON.stringify({ error: "claude: command not found" }) });
    vi.stubGlobal("fetch", fetcher);
    expect(await launcherModels("desk top", "traex")).toEqual({ agent: "traex", models: [] });
    expect(await launcherModels("desktop", "gateway/x", { refresh: true })).toEqual(claude);
    expect(await launcherModels("desktop", "claude")).toBeNull();
    await expect(launcherModels("desktop", "claude")).rejects.toMatchObject({ status: 502, message: "claude: command not found" });
    expect(fetcher.mock.calls.map((c) => c[0]).slice(0, 2)).toEqual([
      "/api/v1/machines/desk%20top/launchers/traex/models", "/api/v1/machines/desktop/launchers/gateway%2Fx/models?refresh=1",
    ]);
  });
});

describe("模型配置的列表（#943）", () => {
  it("asks for a launcher provider's list and says which machine answered; none for an older hub", async () => {
    vi.stubGlobal("localStorage", { getItem: () => "mock" });
    const fetcher = vi.fn()
      .mockResolvedValueOnce({ ok: true, status: 200, text: async () => JSON.stringify({ ...claude, machine: "desktop" }) })
      .mockResolvedValueOnce({ ok: false, status: 404, text: async () => "404 page not found" })
      .mockResolvedValueOnce({ ok: false, status: 503, text: async () => JSON.stringify({ error: "有这个启动器的电脑都不在线，列不出模型" }) });
    vi.stubGlobal("fetch", fetcher);
    expect(await providerModels("launcher.work", { refresh: true })).toEqual({ ...claude, machine: "desktop" });
    expect(await providerModels("launcher.work")).toBeNull();
    await expect(providerModels("launcher.work")).rejects.toMatchObject({ status: 503 });
    expect(fetcher.mock.calls[0][0]).toBe("/api/v1/model-config/providers/launcher.work/models?refresh=1");
    expect(fetcher.mock.calls[1][0]).toBe("/api/v1/model-config/providers/launcher.work/models");
  });

  it("asks on the purpose's own machine when it has one", async () => {
    vi.stubGlobal("localStorage", { getItem: () => "mock" });
    const fetcher = vi.fn().mockResolvedValue({ ok: true, status: 200, text: async () => JSON.stringify({ ...claude, machine: "lab box" }) });
    vi.stubGlobal("fetch", fetcher);
    await providerModels("launcher.work", { machine: "lab box" });
    expect(fetcher.mock.calls[0][0]).toBe("/api/v1/model-config/providers/launcher.work/models?machine=lab+box");
  });

  it("takes an id the list does not have, typed in the search", () => {
    expect(customModel(claude.models, "  my-gateway/model-x ")).toBe("my-gateway/model-x");
    expect(customModel(claude.models, "opus")).toBe(""); // listed: pick the row
    expect(customModel(claude.models, "")).toBe("");
    expect(customModel(claude.models, "two words")).toBe(""); // a search, not an id
  });
});

describe("思考强度", () => {
  it("offers the picked model's efforts, with none picked the one that runs, all without a list", () => {
    expect(effortChoices(claude, "")).toEqual(all);
    expect(effortChoices(claude, "gateway/glm-5.3")).toEqual(["low", "medium", "high"]);
    expect(effortChoices(claude, "haiku")).toEqual([]);
    expect(effortChoices(claude, "", { model: "haiku" })).toEqual([]);
    expect(effortChoices(claude, "", { model: "elsewhere" })).toEqual(all);
    // "Model configuration"'s model listed only under another id (an alias): not known, so not the default entry's (none here).
    const plainDefault = { agent: "claude", models: [{ id: "default", name: "Default", default: true }, { id: "sonnet", name: "Sonnet", resolved: "claude-sonnet-5-5", efforts: ["low", "high"] }] };
    expect(effortChoices(plainDefault, "", { model: "claude-sonnet-5-5" })).toEqual(all);
    expect(effortChoices(plainDefault, "")).toEqual([]);
    expect(effortChoices(null, "")).toEqual(all);
    expect(effortChoices({ agent: "traex", models: [] }, "")).toEqual(all);
  });

  it("goes back to 跟随启动器 when the pick doesn't take it", () => {
    expect(fitEffort("max", effortChoices(claude, "gateway/glm-5.3"))).toBe("");
    expect(fitEffort("high", effortChoices(claude, "gateway/glm-5.3"))).toBe("high");
    expect(fitEffort("", all)).toBe("");
  });
});

describe("启动时发什么", () => {
  it("sends a picked model unless it is the default or the launcher fixes its own", () => {
    expect(startPicks({ agent: "claude", list: claude, model: "opus", effort: "" })).toEqual({ model: "opus" });
    expect(startPicks({ agent: "claude", list: claude, model: "default", effort: "" })).toEqual({});
    expect(startPicks({ agent: "claude", list: claude, model: "", effort: "" })).toEqual({});
    expect(startPicks({ agent: "claude", list: { ...claude, locked: "固定了模型" }, model: "opus", effort: "high" })).toEqual({ effort: "high" });
    expect(startPicks({ agent: "claude", list: null, model: "opus", effort: "" })).toEqual({});
  });

  it("sends an effort only to Claude Code and only one the model takes", () => {
    expect(startPicks({ agent: "claude", list: claude, model: "gateway/glm-5.3", effort: "high" })).toEqual({ model: "gateway/glm-5.3", effort: "high" });
    expect(startPicks({ agent: "claude", list: claude, model: "haiku", effort: "high" })).toEqual({ model: "haiku" });
    expect(startPicks({ agent: "claude", list: null, model: "", effort: "max" })).toEqual({ effort: "max" });
    expect(startPicks({ agent: "codex", list: codex, model: "gpt-5.5-codex", effort: "high" })).toEqual({ model: "gpt-5.5-codex" });
  });
});

describe("启动器的默认值 (#2455)", () => {
  it("names the default's provider and model", () => {
    expect(defaultLabel({ provider: "kcc", model: "kimi-k2.6", effort: "high" })).toBe("默认：kcc · kimi-k2.6");
    expect(defaultLabel({ provider: "kcc", model: "gateway/glm-5.3" })).toBe("默认：kcc · glm-5.3");
    expect(defaultLabel({ provider: "kcc", model: "" })).toBe("默认：kcc");
    expect(defaultLabel(null, "claude")).toBe("默认：claude");
    expect(defaultLabel(undefined)).toBe("默认");
    expect(oneOff()).toBe("仅这一次");
  });
  it("titles the default's provider by its display name (#5029)", () => {
    const names = [{ name: "relay-example-com", display_name: "我的中转 站" }, { name: "kcc" }];
    expect(defaultLabel({ provider: "relay-example-com", model: "glm-5.3" }, "", names)).toBe("默认：我的中转 站 · glm-5.3");
    expect(defaultLabel(null, "relay-example-com", names)).toBe("默认：我的中转 站");
    // an rc alias, or one not in the list: its ID as before
    expect(defaultLabel({ provider: "kcc", model: "" }, "", names)).toBe("默认：kcc");
    expect(defaultLabel({ provider: "gone", model: "" }, "", names)).toBe("默认：gone");
    expect(defaultLabel(undefined, "", names)).toBe("默认");
  });
  it("asks for a purpose's default", async () => {
    vi.stubGlobal("localStorage", { getItem: () => "mock" });
    const body = { launchers: [{ name: "kcc" }], default: { provider: "kcc", model: "kimi-k2.6", runtime: "agentsdk" } };
    const fetcher = vi.fn().mockResolvedValue({ ok: true, status: 200, text: async () => JSON.stringify(body) });
    vi.stubGlobal("fetch", fetcher);
    expect((await machineLaunchers("m1", { purpose: "assistant" })).default).toEqual(body.default);
    await machineLaunchers("m1");
    expect(fetcher.mock.calls.map((c) => c[0])).toEqual(["/api/v1/machines/m1/launchers?purpose=assistant", "/api/v1/machines/m1/launchers"]);
  });
});

describe("手动输入的模型 ID（#1644）", () => {
  const typed: LauncherModels = { agent: "claude", models: [
    ...claude.models,
    { id: "vendor/model-x", name: "vendor/model-x", description: "之前用过的模型 ID", custom: true },
    { id: "~vendor/model-latest", name: "~vendor/model-latest", custom: true },
  ] };

  it("offers a typed ID only the hub would take", () => {
    expect(customModel(claude.models, "~vendor/model-latest[1m]")).toBe("~vendor/model-latest[1m]");
    expect(customModel(claude.models, "-x")).toBe("");
    expect(customModel(claude.models, "a$b")).toBe("");
    expect(customModel(claude.models, "模型")).toBe("");
  });

  it("Claude Code and Codex take one, unless the launcher fixes its own --model", () => {
    expect(takesTypedModel("claude") && takesTypedModel("codex")).toBe(true);
    expect(takesTypedModel("traex") || takesTypedModel("agy")).toBe(false);
    expect(typedModelOffered("claude", claude)).toBe(true);
    expect(typedModelOffered("claude", undefined)).toBe(true); // the list couldn't be read
    expect(typedModelOffered("claude", { ...claude, locked: "固定了模型" })).toBe(false);
  });

  it("keeps an unlisted pick in the start; never a bad one, a locked launcher's, or with an older hub", () => {
    expect(startPicks({ agent: "claude", list: claude, model: "acme/coder-9", effort: "high" })).toEqual({ model: "acme/coder-9", effort: "high" });
    expect(startPicks({ agent: "codex", list: codex, model: "gpt-next-preview", effort: "" })).toEqual({ model: "gpt-next-preview" });
    expect(startPicks({ agent: "claude", list: undefined, model: "acme/coder-9", effort: "" })).toEqual({ model: "acme/coder-9" });
    expect(startPicks({ agent: "claude", list: { agent: "claude", models: [] }, model: "acme/coder-9", effort: "" })).toEqual({ model: "acme/coder-9" });
    expect(startPicks({ agent: "claude", list: claude, model: "two words", effort: "" })).toEqual({});
    expect(startPicks({ agent: "claude", list: { ...claude, locked: "固定了模型" }, model: "acme/coder-9", effort: "" })).toEqual({});
    expect(startPicks({ agent: "claude", list: null, model: "acme/coder-9", effort: "" })).toEqual({});
    expect(startPicks({ agent: "traex", list: undefined, model: "acme/coder-9", effort: "" })).toEqual({});
    expect(startPicks({ agent: "claude", list: typed, model: "vendor/model-x", effort: "" })).toEqual({ model: "vendor/model-x" });
  });

  it("a typed ID's efforts aren't known: all of them", () => {
    expect(effortChoices(typed, "vendor/model-x")).toEqual(all);
    expect(effortChoices(typed, "acme/coder-9")).toEqual(all);
    expect(effortChoices(typed, "haiku")).toEqual([]);
  });

  it("forgets one off the list, and puts it back where it was", () => {
    const { list, restore } = withoutCustom(typed, "vendor/model-x");
    expect(ids(list.models)).toEqual(["default", "opus", "haiku", "gateway/glm-5.3", "~vendor/model-latest"]);
    expect(ids(restore(list).models)).toEqual(ids(typed.models));
    expect(withoutCustom(typed, "opus").list).toBe(typed); // only typed IDs
    expect(restore(typed)).toBe(typed); // already back
  });

  it("DELETE …/models/custom?model=", async () => {
    vi.stubGlobal("localStorage", { getItem: () => "mock" });
    const fetch = vi.fn().mockResolvedValue({ ok: true, status: 200, text: async () => JSON.stringify({ ok: true }) });
    vi.stubGlobal("fetch", fetch);
    await forgetCustomModel("mac", "cc gw", "~vendor/model-latest[1m]");
    const [url, init] = fetch.mock.calls[0] as unknown as [string, RequestInit];
    expect(init.method).toBe("DELETE");
    expect(url).toContain("/api/v1/machines/mac/launchers/cc%20gw/models/custom?model=~vendor%2Fmodel-latest%5B1m%5D");
  });
});

describe("Agent presets: what a launcher can't take (#2225)", () => {
  const review = { name: "review", launcher: "gw", model: "gateway/glm-5.3", effort: "high" };
  it("takes a listed model and an effort it supports", () => {
    expect(tierLeftOut(review, { agent: "claude", list: claude })).toEqual([]);
  });
  it("takes a typed model on Claude Code, and the list's default as running anyway", () => {
    expect(tierLeftOut({ ...review, model: "vendor/other" }, { agent: "claude", list: claude })).toEqual([]);
    expect(tierLeftOut({ ...review, model: "default", effort: "" }, { agent: "claude", list: claude })).toEqual([]);
  });
  it("names the model a launcher fixing its own --model drops, and an effort the model doesn't take", () => {
    expect(tierLeftOut(review, { agent: "claude", list: { ...claude, locked: "fixed" } })).toContain("gateway/glm-5.3");
    expect(tierLeftOut({ ...review, effort: "max" }, { agent: "claude", list: claude })).toEqual(["max"]);
  });
  it("sends the list's default entry a preset names when the model settings would run another model", () => {
    const followed = { provider: "launcher.gw", model: "opus" };
    expect(startPicks({ agent: "claude", list: claude, model: "default", effort: "", followed }).model).toBe("default");
    expect(startPicks({ agent: "claude", list: claude, model: "default", effort: "" }).model).toBeUndefined();
    expect(tierLeftOut({ name: "d", launcher: "gw", model: "default" }, { agent: "claude", list: claude, followed })).toEqual([]);
  });
  it("names the effort an agent that starts without one drops", () => {
    expect(tierLeftOut({ name: "c", launcher: "codex", effort: "high" }, { agent: "codex", list: { agent: "codex", models: [] } })).toEqual(["high"]);
  });
});
