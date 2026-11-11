import { afterEach, describe, expect, it, vi } from "vitest";
import {
  assignByDefault,
  assignSummary,
  displayNameProblem,
  launcherNameProblem,
  loadPresets,
  presetBody,
  presetChoices,
  presetDefaultModel,
  presetHost,
  presetRunsClaude,
  presetSubtitle,
  presetTestBody,
  presetTestable,
  presetTitle,
  signupHref,
  testableKey,
  type LauncherPreset,
} from "./launcher-presets";

const team: LauncherPreset = {
  id: "example-gateway/team", plugin: "example-gateway", name: "示例网关 · 团队", kind: "launcher", base_url: "https://gateway.example.com/v1",
  model: "example-pro", models: [{ id: "example-mini", name: "Example Mini" }],
};
const glm: LauncherPreset = {
  id: "builtin/glm", plugin: "builtin", name: "Zhipu GLM", display_name: "智谱 GLM", kind: "launcher", base_url: "https://open.example.cn/api/anthropic",
  auth: "bearer", model: "glm-x", signup_url: "https://open.example.cn/keys",
};
const api: LauncherPreset = { id: "example-gateway/api", plugin: "example-gateway", name: "示例 API", kind: "api", api: "responses", base_url: "not a url", models: [{ id: "m1" }, { id: "m2" }] };

afterEach(() => vi.unstubAllGlobals());

describe("launcher presets", () => {
  it("defaults to the declared model, else the first listed", () => {
    expect(presetDefaultModel(team)).toBe("example-pro");
    expect(presetDefaultModel(api)).toBe("m1");
    expect(presetDefaultModel({ ...api, models: [] })).toBe("");
  });

  it("offers the default first when it is not listed", () => {
    expect(presetChoices(team).map((m) => m.id)).toEqual(["example-pro", "example-mini"]);
    expect(presetChoices(api).map((m) => m.id)).toEqual(["m1", "m2"]);
  });

  it("reads the kind and the host", () => {
    expect(presetSubtitle(team)).toBe("Claude Code 启动器 · gateway.example.com");
    expect(presetSubtitle(api)).toBe("直接 API");
  });

  it("checks names like the hub", () => {
    expect(launcherNameProblem("")).toBe("");
    expect(launcherNameProblem("jv_team")).toBe("");
    expect(launcherNameProblem("9x")).toMatch(/字母/);
    // every startup ID the hub keeps for itself (reservedLauncherNames)
    for (const n of ["claude", "presets", "test", "discover", "import", "health"]) expect(launcherNameProblem(n)).toBe(`不能叫 ${n}`);
    expect(launcherNameProblem("test-2")).toBe("");
  });

  it("checks display names like the hub: 64 characters, one line, any language (#5029)", () => {
    expect(displayNameProblem("")).toBe("");
    expect(displayNameProblem("我的中转 站")).toBe("");
    // counted as characters, not UTF-16 units: 64 emoji fit
    expect(displayNameProblem("🚀".repeat(64))).toBe("");
    expect(displayNameProblem(` ${"字".repeat(64)} `)).toBe("");
    expect(displayNameProblem("字".repeat(65))).toBe("显示名最多 64 个字，不能换行");
    expect(displayNameProblem("Team\nrelay")).toBe("显示名最多 64 个字，不能换行");
    expect(displayNameProblem("Team\trelay")).toBe("显示名最多 64 个字，不能换行");
  });

  it("sends the model only when another one was picked", () => {
    expect(presetBody(team, { name: " jv_team ", display_name: "", api_key: " sk-x ", model: "example-pro" })).toEqual({ name: "jv_team", api_key: "sk-x", preset: "example-gateway/team" });
    expect(presetBody(team, { name: "jv_team", display_name: "", api_key: "k", model: "example-mini" })).toEqual({ name: "jv_team", api_key: "k", preset: "example-gateway/team", model: "example-mini" });
  });

  it("leaves the name out when none is typed, so the hub names it, and asks to assign when told to", () => {
    expect(presetBody(glm, { name: "  ", display_name: "", api_key: "sk-x", model: "glm-x" }, true)).toEqual({ api_key: "sk-x", preset: "builtin/glm", assign: ["assistant", "agent.new"] });
    expect(presetBody(glm, { name: "", display_name: "", api_key: "sk-x", model: "" })).toEqual({ api_key: "sk-x", preset: "builtin/glm" });
  });

  it("sends a typed display name, trimmed; left empty the hub uses the preset's (#5029)", () => {
    expect(presetBody(glm, { name: "", display_name: " 团队的 GLM ", api_key: "sk-x", model: "glm-x" }, true)).toEqual({ display_name: "团队的 GLM", api_key: "sk-x", preset: "builtin/glm", assign: ["assistant", "agent.new"] });
    expect(presetBody(glm, { name: "glm_team", display_name: "  ", api_key: "sk-x", model: "glm-x" })).toEqual({ name: "glm_team", api_key: "sk-x", preset: "builtin/glm" });
  });

  it("tests a key against the preset: the hub fills in the address and the default model", () => {
    expect(presetTestBody(glm, " sk-x ", "")).toEqual({ preset: "builtin/glm", api_key: "sk-x", auth: "bearer" });
    expect(presetTestBody(team, "sk-x", "example-mini")).toEqual({ preset: "example-gateway/team", api_key: "sk-x", auth: "api_key", model: "example-mini" });
    expect(presetTestable({ ...api, api: "chat" })).toBe(true);
    expect(testableKey("sk-1")).toBe(false);
    expect(testableKey(" sk-12345 ")).toBe(true);
  });

  it("names a preset in the request's language and links only web pages", () => {
    expect(presetTitle(glm)).toBe("智谱 GLM");
    expect(presetTitle(team)).toBe("示例网关 · 团队");
    expect(presetHost(glm)).toBe("open.example.cn");
    expect(presetHost(api)).toBe("");
    expect(signupHref(glm)).toBe("https://open.example.cn/keys");
    expect(signupHref({ ...glm, signup_url: "javascript:alert(1)" })).toBe("");
    expect(signupHref(team)).toBe("");
  });

  it("says what was assigned and what was kept, and why", () => {
    const r = { launcher: { name: "glm" }, assigned: ["assistant"], kept: [{ purpose: "agent.new", reason: "customized" }] };
    const s = assignSummary(r, true);
    expect(s.assigned).toContain("主对话");
    expect(s.kept).toHaveLength(1);
    expect(s.kept[0]).toContain("新开的 Agent");
    expect(s.kept[0]).toContain("你选过");
    expect(assignSummary({ launcher: { name: "glm" } }, true)).toEqual({ assigned: "", kept: [] });
    expect(assignSummary(r, false)).toEqual({ assigned: "", kept: [] });
    expect(assignSummary({ ...r, assigned: [], kept: [{ purpose: "assistant", reason: "failed" }] }, true).assigned).toBe("");
  });

  // #4526: a key the region blocks is not put on the main conversation and new agents unless asked for.
  it("assigns by default only when Claude Code does not answer already and the region is not blocked", () => {
    expect(assignByDefault(false, null)).toBe(true);
    expect(assignByDefault(false, { ok: true, ms: 300 })).toBe(true);
    expect(assignByDefault(false, { ok: false, ms: 80, error: "Key 不对（401）" })).toBe(true);
    expect(assignByDefault(false, { ok: false, ms: 120, code: "region_blocked", error: "x" })).toBe(false);
    expect(assignByDefault(true, null)).toBe(false);
    expect(assignByDefault(true, { ok: true, ms: 300 })).toBe(false);
  });

  // Claude Code needs a Messages endpoint: a direct API on Responses / Chat Completions runs neither the main conversation nor Agents.
  it("runs the main conversation and Agents only on Messages", () => {
    expect(presetRunsClaude(glm)).toBe(true);
    expect(presetRunsClaude(team)).toBe(true);
    expect(presetRunsClaude(api)).toBe(false);
    expect(presetRunsClaude({ ...api, api: "chat" })).toBe(false);
    expect(presetRunsClaude({ ...api, api: "messages" })).toBe(true);
    expect(presetRunsClaude({ ...api, api: undefined })).toBe(true); // an empty api is Messages
  });

  it("an older hub without presets answers none", async () => {
    vi.stubGlobal("localStorage", { getItem: () => "jd_test" });
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ error: "not found" }), { status: 404, headers: { "Content-Type": "application/json" } })));
    expect(await loadPresets()).toEqual([]);
  });
});
