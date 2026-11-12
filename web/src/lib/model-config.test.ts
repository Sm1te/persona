import { afterEach, describe, expect, it, vi } from "vitest";
import {
  agentsOverride, chooseProvider, draftFor, modelConfiguration, modelPatch, modelPurpose, modelSummary, providersFor, runtimeChoices, runtimeFallbackText, saveModelPurpose, withPurposeAfter,
  type ModelProvider, type ModelPurpose,
} from "./model-config";
import { executionForm, purposeDescription, purposeGroups } from "./model-config-execution";

const purpose: ModelPurpose = {
  id: "lark.agent", name: "飞书 /agent", group: "对话", kind: "launcher", revision: 3,
  override: { provider: "strong", model: "main", effort: "high" },
  effective: { provider: "strong", model: "main", effort: "high" }, fields: ["provider", "model", "effort"],
};
const providers: ModelProvider[] = [
  { id: "strong", name: "常用启动器", kind: "launcher", hidden: true },
  { id: "other", name: "其他启动器", kind: "launcher" },
  { id: "hidden", name: "隐藏启动器", kind: "launcher", hidden: true },
  { id: "api", name: "直接 API", kind: "api" },
];

afterEach(() => vi.unstubAllGlobals());

describe("用途配置", () => {
  it("uses overrides for editing, never silently fixes a missing provider", () => {
    expect(draftFor(purpose)).toEqual({ provider: "strong", model: "main", effort: "high", runtime: "" });
    expect(draftFor({ ...purpose, override: { provider: "gone" } })).toEqual({ provider: "gone", model: "", effort: "", runtime: "" });
    expect(draftFor({ ...purpose, override: {} })).toEqual({ provider: "", model: "", effort: "", runtime: "" });
  });
  it("clears model and effort when the provider changes", () => {
    const draft = draftFor(purpose);
    expect(chooseProvider(draft, "api")).toEqual({ provider: "api", model: "", effort: "", runtime: "" });
    expect(chooseProvider(draft, "strong")).toBe(draft);
  });
  it("sends revision and explicit null to restore supported fields", () => {
    expect(modelPatch(purpose, { provider: " other ", model: "", effort: " low ", runtime: "" })).toEqual({ revision: 3, provider: "other", model: null, effort: "low" });
    expect(modelPatch({ ...purpose, fields: ["model"] }, { provider: "ignored", model: "", effort: "high", runtime: "" })).toEqual({ revision: 3, model: null });
    expect(modelPatch({ ...purpose, fields: [] }, draftFor(purpose))).toEqual({ revision: 3 });
  });
  it("uses server capability candidates and keeps a referenced hidden provider", () => {
    expect(providersFor(purpose, providers).map((p) => p.id)).toEqual(["strong", "other"]);
    expect(providersFor({ ...purpose, provider_ids: ["api"] }, providers).map((p) => p.id)).toEqual(["api"]);
    expect(providersFor({ ...purpose, provider_ids: [] }, providers)).toEqual([]);
  });
  it("groups and searches real purposes without generating skills", () => {
    const triage: ModelPurpose = { ...purpose, id: "inbox.triage", name: "分拣", group: "收件箱", kind: "api" };
    expect(purposeGroups([purpose, triage], "分拣")).toEqual([["后台处理 · 直接 API", [triage]]]);
    expect(purposeGroups([purpose, triage], " MAIN ").length).toBe(2);
    expect(purposeGroups([purpose], "技能")).toEqual([]);
    expect(modelSummary(purpose, providers)).toBe("常用启动器 · main · 思考 high");
  });
  it("sets apart only the uses of Agents the hub gives fields of their own", () => {
    for (const id of ["lark.agent", "inbox.agent"]) {
      expect(agentsOverride({ ...purpose, id, parent: "agent.new" })).toBe(true);
      expect(agentsOverride({ ...purpose, id, parent: "agent.new", fields: [] })).toBe(false); // an older hub: read-only
      expect(agentsOverride({ ...purpose, id, parent: "agent.new", fields: undefined })).toBe(false);
    }
    expect(agentsOverride({ ...purpose, id: "agent.new" })).toBe(false); // the category itself
    expect(agentsOverride({ ...purpose, id: "inbox.judge", parent: "inbox.triage" })).toBe(false);
  });
});

describe("执行形态", () => {
  it("orders by kind and launch, with extension API purposes kept under Extensions", () => {
    const chat: ModelPurpose = { ...purpose, id: "assistant", launch: "headless" };
    const lark: ModelPurpose = { ...purpose, parent: "agent.new", launch: "headless" };
    const agents: ModelPurpose = { ...purpose, id: "agent.new", launch: "engine" };
    const api: ModelPurpose = { ...purpose, id: "kb", kind: "api" };
    const extension: ModelPurpose = { ...api, id: "extension.example", parent: "kb" };
    const voice: ModelPurpose = { ...purpose, id: "plugin.voice", kind: "plugin" };
    expect(purposeGroups([extension, api, agents, voice, chat, lark], "")).toEqual([
      ["Persona 对话 · 后台进程", [chat, lark]], ["Agents · 交互式", [agents]],
      ["后台处理 · 直接 API", [api]], ["扩展", [extension, voice]],
    ]);
    expect(purposeGroups([chat, agents], "后台进程")).toEqual([["Persona 对话 · 后台进程", [chat]]]);
    // An older hub's catalog still renders, without trusting its inheritance-group labels.
    expect(purposeGroups([{ ...chat, launch: undefined }], "")[0][0]).toBe("Persona 对话 · 后台进程");
    expect(purposeGroups([{ ...lark, launch: undefined }], "")[0][0]).toBe("Persona 对话 · 后台进程");
  });

  it("keeps form fixed while the provider, runtime, protocol and machine change its backend", () => {
    const chat: ModelPurpose = { ...purpose, id: "assistant", launch: "headless" };
    expect(executionForm(chat, providers)).toEqual({ label: "后台进程", backend: "claude -p" });
    expect(executionForm({ ...chat, effective: { provider: "launcher.codex", runtime: "agentsdk" } }, providers).backend).toBe("codex app-server");
    expect(executionForm({ ...chat, effective: { runtime: "agentsdk" } }, providers).backend).toBe("Agent SDK");
    const agent: ModelPurpose = { ...chat, id: "agent.new", launch: "engine" };
    expect(executionForm(agent, providers)).toEqual({ label: "交互式", backend: "按机器 · claude" });
    const machine = { id: "box", name: "Computer", online: true, engine_default: "orca" };
    expect(executionForm({ ...agent, machine_id: "box" }, providers, [machine]).backend).toBe("orca · claude");
    expect(executionForm(agent, providers, [machine]).backend).toBe("按机器 · claude");
    for (const [protocol, backend] of [["messages", "Messages"], ["responses", "Responses"], ["chat", "Chat"]] as const) {
      const direct: ModelPurpose = { ...chat, id: "kb", kind: "api", launch: undefined, effective: { provider: "direct" } };
      expect(executionForm(direct, [{ id: "direct", name: "Direct", kind: "api", api: protocol }]))
        .toEqual({ label: "直接 API", backend });
    }
  });
});

describe("主对话的运行方式", () => {
  const assistant: ModelPurpose = {
    ...purpose, id: "assistant", name: "主对话", override: { provider: "strong" }, effective: { provider: "strong" }, runtimes: ["cli", "agentsdk"],
    fields: ["provider", "model", "effort", "runtime"],
  };
  it("offers only the runtimes the hub lists, and names non-Claude CLI runtimes accurately", () => {
    expect(runtimeChoices(assistant).map((r) => r.label)).toEqual(["Claude Code CLI", "Agent SDK"]);
    expect(runtimeChoices(assistant, "codex")).toEqual([{ id: "cli", label: "Codex CLI" }]);
    expect(runtimeChoices(assistant, "agy")).toEqual([{ id: "cli", label: "Antigravity ACP" }]);
    expect(runtimeChoices(assistant, "omp")).toEqual([{ id: "cli", label: "omp CLI" }]);
    expect(runtimeChoices({ ...assistant, runtimes: ["agentsdk", "future"] }).map((r) => r.id)).toEqual(["agentsdk"]);
    expect(runtimeChoices(purpose)).toEqual([]);
  });
  it("edits the stored runtime, CLI when none, and keeps it across a provider change", () => {
    expect(draftFor(assistant).runtime).toBe("cli");
    expect(draftFor({ ...assistant, override: { runtime: "agentsdk" } }).runtime).toBe("agentsdk");
    expect(draftFor(purpose).runtime).toBe("");
    expect(chooseProvider(draftFor({ ...assistant, override: { runtime: "agentsdk" } }), "other").runtime).toBe("agentsdk");
  });
  it("saves the runtime only for a purpose that has one", () => {
    expect(modelPatch(assistant, { provider: "strong", model: "", effort: "", runtime: "agentsdk" }))
      .toEqual({ revision: 3, provider: "strong", model: null, effort: null, runtime: "agentsdk" });
    expect(modelPatch(purpose, { provider: "strong", model: "", effort: "", runtime: "agentsdk" })).not.toHaveProperty("runtime");
  });
  it("says Agent SDK in the summary, nothing for the CLI", () => {
    expect(modelSummary({ ...assistant, effective: { provider: "strong", runtime: "agentsdk" } }, providers)).toBe("常用启动器 · Agent SDK");
    expect(modelSummary({ ...assistant, effective: { provider: "strong", runtime: "cli" } }, providers)).toBe("常用启动器");
  });
});

describe("统一 API 契约", () => {
  it("loads catalog/detail and saves a single purpose, without exposing keys", async () => {
    vi.stubGlobal("localStorage", { getItem: () => "mock" });
    const fetcher = vi.fn().mockImplementation(async (_url: string, init: RequestInit) => ({
      ok: true, status: 200,
      text: async () => JSON.stringify(init.method === "PATCH" ? { purpose, providers } : { purpose, purposes: [purpose], providers }),
    }));
    vi.stubGlobal("fetch", fetcher);
    expect((await modelConfiguration()).purposes).toEqual([purpose]);
    expect((await modelPurpose(purpose.id)).purpose).toEqual(purpose);
    expect((await saveModelPurpose(purpose, { provider: "", model: "", effort: "", runtime: "" })).purpose).toEqual(purpose);
    expect(fetcher.mock.calls.map((c) => c[0])).toEqual([
      "/api/v1/model-config", "/api/v1/model-config/purposes/lark.agent", "/api/v1/model-config/purposes/lark.agent",
    ]);
    expect(JSON.parse(fetcher.mock.calls[2][1].body)).toEqual({ revision: 3, provider: null, model: null, effort: null });
  });
  it("returns conflicts instead of retrying and overwriting newer settings", async () => {
    vi.stubGlobal("localStorage", { getItem: () => "mock" });
    const fetcher = vi.fn().mockResolvedValue({ ok: false, status: 409, text: async () => JSON.stringify({ error: "revision conflict" }) });
    vi.stubGlobal("fetch", fetcher);
    const draft = draftFor(purpose);
    await expect(saveModelPurpose(purpose, draft)).rejects.toMatchObject({ status: 409 });
    expect(draft).toEqual({ provider: "strong", model: "main", effort: "high", runtime: "" });
    expect(fetcher).toHaveBeenCalledTimes(1);
  });
});

describe("后台督办的用途 (#5082)", () => {
  const main: ModelPurpose = { ...purpose, id: "assistant", name: "主对话", launch: "headless" };
  const agents: ModelPurpose = { ...purpose, id: "agent.new", name: "Agents", launch: "engine" };
  const supervise: ModelPurpose = { ...purpose, id: "assistant.supervise", name: "后台督办", parent: "assistant", category_id: "assistant", launch: "headless", override: {}, inherited_from: "主对话" };

  it("goes right after Main chat, once, and only when the hub has both", () => {
    const catalog = { purposes: [main, agents], providers };
    expect(withPurposeAfter(catalog, supervise, "assistant").purposes.map((p) => p.id)).toEqual(["assistant", "assistant.supervise", "agent.new"]);
    expect(catalog.purposes).toHaveLength(2); // the hub's answer is left as it was
    expect(withPurposeAfter(catalog, undefined, "assistant")).toBe(catalog);
    const listed = { purposes: [main, agents, supervise], providers };
    expect(withPurposeAfter(listed, supervise, "assistant")).toBe(listed);
    const noMain = { purposes: [agents], providers };
    expect(withPurposeAfter(noMain, supervise, "assistant")).toBe(noMain);
  });

  it("is in Main chat's group and has a sentence saying what it is for; no other purpose has one", () => {
    const groups = purposeGroups(withPurposeAfter({ purposes: [main, agents], providers }, supervise, "assistant").purposes, "");
    expect(groups[0][1].map((p) => p.id)).toEqual(["assistant", "assistant.supervise"]);
    // an older hub's shape (no launch): still with Main chat, by its parent
    expect(purposeGroups([main, { ...supervise, launch: undefined }, agents], "")[0][1].map((p) => p.id)).toEqual(["assistant", "assistant.supervise"]);
    expect(purposeDescription(supervise)).toContain("跟进并验收");
    expect(purposeDescription(main)).toBe("");
  });
});

describe("退回 CLI", () => {
  it("says why", () => expect(runtimeFallbackText(" 没有 Node.js 22+ ")).toBe("已退回 CLI：没有 Node.js 22+"));
});
