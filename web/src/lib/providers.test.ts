import { describe, expect, it } from "vitest";
import { ApiError } from "./api";
import {
  autoCheckable,
  fallbackTitle,
  fillFromCatalog,
  formFromView,
  formModel,
  formProblems,
  importCanSplit,
  importCount,
  importCreates,
  importMerges,
  importDiffers,
  importErrorText,
  importFrom,
  importLeftOut,
  importBrings,
  importRow,
  importSources,
  importStays,
  importSummary,
  type ImportedProvider,
  mergeDiscovered,
  newForm,
  probeBody,
  providerBody,
  testModel,
  testText,
  type ProviderForm,
  type ProviderView,
} from "./providers";

const ready = (over: Partial<ProviderForm> = {}): ProviderForm => ({
  ...newForm(),
  name: "gw",
  base_url: "https://gw.example.com",
  api_key: "sk-test-1",
  models: [formModel({ id: "m1", context_window: 200000 })],
  model: "m1",
  ...over,
});

const stored: ProviderView = {
  name: "gw",
  kind: "launcher",
  base_url: "https://gw.example.com",
  model: "m1",
  auth: "bearer",
  has_key: true,
  has_headers: true,
  models: [{ id: "m1", context_window: 1000000, efforts: ["high", "max"], default_effort: "high" }, { id: "m2" }],
  opus_model: "m1",
  subagent_force: true,
  proxy: "http://proxy.example.com:8080",
  timeout_ms: 600000,
  effort: "high",
};

describe("newForm", () => {
  it("is a gateway typed by hand: no address, Bearer", () => {
    expect(newForm()).toMatchObject({ auth: "bearer", base_url: "" });
  });
});

describe("formProblems", () => {
  it("passes a ready form", () => {
    expect(formProblems(ready(), true)).toEqual({ connect: "", reach: "", models: "", advanced: "" });
  });
  it("checks the connection step", () => {
    expect(formProblems(ready({ name: "" }), true).connect).toBe("要填启动 ID");
    expect(formProblems(ready({ name: "1bad" }), true).connect).toMatch(/启动 ID 只能/);
    expect(formProblems(ready({ base_url: "ftp://x" }), true).connect).toMatch(/http/);
    expect(formProblems(ready({ base_url: "https://u:p@x.example.com" }), true).connect).toMatch(/凭据/);
    expect(formProblems(ready({ api_key: "" }), true).connect).toBe("要填 API Key");
    // editing one that has a key: left empty keeps it
    expect(formProblems(ready({ api_key: "" }), false, true).connect).toBe("");
  });
  it("needs no startup ID where the hub makes one up, but checks a typed one and the display name (#5029)", () => {
    const auto = (over: Partial<ProviderForm>) => formProblems(ready(over), true, false, true);
    expect(auto({ name: "", display_name: "我的中转 站" })).toEqual({ connect: "", reach: "", models: "", advanced: "" });
    expect(auto({ name: "1bad" }).connect).toMatch(/启动 ID 只能/);
    expect(auto({ name: "test" }).connect).toBe("不能叫 test");
    expect(auto({ name: "", display_name: "字".repeat(65) }).connect).toBe("显示名最多 64 个字，不能换行");
    // an older hub still needs the ID; a saved provider always has its own
    expect(formProblems(ready({ name: "", display_name: "我的中转 站" }), true).connect).toBe("要填启动 ID");
    expect(formProblems(ready({ display_name: "Team\nrelay" }), false, true).connect).toBe("显示名最多 64 个字，不能换行");
    // the names never keep the gateway from being reached
    expect(auto({ name: "1bad", display_name: "字".repeat(65) }).reach).toBe("");
  });
  it("lets the address and the key alone decide whether the gateway can be reached (#4881)", () => {
    expect(formProblems(ready(), true)).toMatchObject({ connect: "", reach: "" });
    for (const name of ["", "1bad", "claude"]) {
      const p = formProblems(ready({ name }), true);
      expect(p.connect).not.toBe("");
      expect(p.reach).toBe("");
    }
    expect(formProblems(ready({ name: "", base_url: "ftp://x" }), true).reach).toMatch(/http/);
    expect(formProblems(ready({ name: "", api_key: "" }), true).reach).toBe("要填 API Key");
    expect(formProblems(ready({ api_key: "" }), false, true).reach).toBe("");
    // Codex runs without an address or key, so nothing keeps it from being tested
    expect(formProblems(ready({ agent: "codex", base_url: "", api_key: "" }), true).reach).toBe("");
  });
  it("ignores unsupported chat draft values without weakening other protocols' validation", () => {
    const draft = ready({ kind: "api", api: "chat", headers: "invalid", headers_touched: true, proxy: "invalid", no_proxy: "example.com", env: "invalid", env_touched: true, timeout_ms: "bad", compact_window: "bad", max_context: "bad", effort: "high", opus_model: "tier-a", sonnet_model: "tier-b", haiku_model: "tier-c", fable_model: "tier-d", subagent_force: true });
    expect(formProblems(draft, true).advanced).toBe("");
    expect(formProblems({ ...draft, api: "responses" }, true).advanced).not.toBe("");
    for (const body of [providerBody(draft, null), providerBody(draft, { name: "gw", kind: "api", api: "chat", model: "m1" }), probeBody(draft, null)]) {
      expect(body).not.toHaveProperty("headers");
      expect(body).not.toHaveProperty("proxy");
      for (const key of ["no_proxy", "env", "timeout_ms", "compact_window", "max_context", "effort", "opus_model", "sonnet_model", "haiku_model", "fable_model", "subagent_force"]) expect(body).not.toHaveProperty(key);
    }
    const responses = providerBody({ ...draft, api: "responses" }, null);
    expect(responses).toMatchObject({ headers: "invalid", proxy: "invalid", no_proxy: "example.com", opus_model: "tier-a", subagent_force: true });
    const models = [formModel({ id: "m1", context_window: 16000, efforts: ["high"], default_effort: "high" })];
    expect(providerBody({ ...draft, models }, null).models).toEqual([{ id: "m1", context_window: 16000, efforts: ["high"], default_effort: "high" }]);
  });
  it("sends Send through only for a direct API connection, and only when it changed", () => {
    const api = ready({ kind: "api", via_machine: "laptop" });
    expect(providerBody(api, null).via_machine).toBe("laptop");
    expect(providerBody(api, { name: "gw", kind: "api", via_machine: "laptop" })).not.toHaveProperty("via_machine");
    expect(providerBody({ ...api, via_machine: "" }, { name: "gw", kind: "api", via_machine: "laptop" }).via_machine).toBe("");
    expect(probeBody(api, "gw").via_machine).toBe("laptop");
    expect(providerBody(ready({ via_machine: "laptop" }), null)).not.toHaveProperty("via_machine");
    expect(formFromView({ name: "gw", kind: "api", via_machine: "laptop" }).via_machine).toBe("laptop");
  });
  it("checks the models step", () => {
    expect(formProblems(ready({ model: "", models: [] }), true).models).toBe("至少勾选或添加一个模型");
    expect(formProblems(ready({ model: "" }), true).models).toBe("选一个默认模型");
    expect(formProblems(ready({ model: "m9" }), true).models).toBe("默认模型要在勾选的模型里");
    expect(formProblems(ready({ models: [formModel({ id: "m1" }), formModel({ id: "m1" })] }), true).models).toMatch(/重复/);
    expect(formProblems(ready({ models: [{ ...formModel({ id: "m1" }), context_window: "12k" }] }), true).models).toMatch(/上下文窗口/);
    expect(formProblems(ready({ models: [formModel({ id: "m1", efforts: ["low"], default_effort: "max" })] }), true).models).toMatch(/默认思考档位/);
    const mismatch = ready({ kind: "api", api: "chat", models: [formModel({ id: "m1", efforts: ["low"], default_effort: "high" })] });
    expect(formProblems(mismatch, true).models).toMatch(/默认思考档位/);
    expect(formProblems({ ...mismatch, api: "messages" }, true).models).toMatch(/默认思考档位/);
    expect(formProblems({ ...mismatch, api: "responses" }, true).models).toMatch(/默认思考档位/);
    expect(providerBody(mismatch, null).models).toEqual([{ id: "m1", efforts: ["low"], default_effort: "high" }]);
    expect(mismatch.models[0].efforts).toEqual(["low"]);
    // an unchecked duplicate does not count
    expect(formProblems(ready({ models: [formModel({ id: "m1" }), formModel({ id: "m1" }, false)] }), true).models).toBe("");
  });
  it("checks the advanced fields", () => {
    expect(formProblems(ready({ headers: "no colon", headers_touched: true }), true).advanced).toMatch(/请求头第 1 行/);
    expect(formProblems(ready({ headers: "X-A: 1\n\nX-B: 2", headers_touched: true }), true).advanced).toBe("");
    expect(formProblems(ready({ env: "A=1\nbad name=2", env_touched: true }), true).advanced).toMatch(/环境变量第 2 行/);
    expect(formProblems(ready({ env: Array(51).fill("A=1").join("\n"), env_touched: true }), true).advanced).toMatch(/最多 50/);
    expect(formProblems(ready({ env: "# off for this one\nexport HTTP_PROXY=\nno_proxy=*.example.com\nexport\tB=1\n\uFEFFC=2 # c", env_touched: true }), true).advanced).toBe("");
    expect(formProblems(ready({ env: "\n\nexport =1", env_touched: true }), true).advanced).toMatch(/环境变量第 3 行/);
    expect(formProblems(ready({ proxy: "proxy.example.com" }), true).advanced).toMatch(/代理/);
    expect(formProblems(ready({ proxy: "socks5://127.0.0.1:1080" }), true).advanced).toBe("");
    expect(formProblems(ready({ timeout_ms: "-1" }), true).advanced).toMatch(/超时/);
    expect(formProblems(ready({ max_context: "1.5" }), true).advanced).toMatch(/最大上下文/);
    expect(formProblems(ready({ args: "--ok bad" }), true).advanced).toMatch(/额外参数/);
  });
});

describe("providerBody", () => {
  it("a new provider sends what is set, not the empty ones", () => {
    const b = providerBody(ready({ timeout_ms: "30000", subagent_force: true }), null);
    expect(b).toMatchObject({ name: "gw", base_url: "https://gw.example.com", model: "m1", auth: "bearer", api_key: "sk-test-1", timeout_ms: 30000, subagent_force: true });
    expect(b.models).toEqual([{ id: "m1", context_window: 200000 }]);
    for (const k of ["headers", "env", "proxy", "opus_model", "compact_window", "effort"]) expect(b).not.toHaveProperty(k);
  });
  it("a new provider with no startup ID sends it empty, for the hub to make up, with the display name (#5029)", () => {
    expect(providerBody(ready({ name: " ", display_name: " 我的中转 站 " }), null)).toMatchObject({ name: "", display_name: "我的中转 站", base_url: "https://gw.example.com" });
    expect(providerBody(ready(), null)).toMatchObject({ name: "gw", display_name: "" });
  });
  it("a PUT renames: the display name always goes, \"\" back to showing the ID (#5029)", () => {
    const named: ProviderView = { ...stored, display_name: "Team relay" };
    const f = formFromView(named);
    expect(f.display_name).toBe("Team relay");
    expect(providerBody(f, named)).toMatchObject({ name: "gw", display_name: "Team relay" });
    expect(providerBody({ ...f, display_name: " 团队中转 " }, named).display_name).toBe("团队中转");
    expect(providerBody({ ...f, display_name: "" }, named).display_name).toBe("");
  });
  it("an unchanged PUT leaves out the #2455 fields, the key and the headers", () => {
    const b = providerBody(formFromView(stored), stored);
    expect(b).toMatchObject({ name: "gw", base_url: "https://gw.example.com", model: "m1", auth: "bearer" });
    for (const k of ["models", "opus_model", "subagent_force", "proxy", "timeout_ms", "effort", "headers", "env", "api_key"]) expect(b).not.toHaveProperty(k);
  });
  it("a PUT sends only what changed; cleared headers go as an empty string", () => {
    const f = { ...formFromView(stored), proxy: "", timeout_ms: "900000", headers: "", headers_touched: true, env: " HTTPS_PROXY=\n", env_touched: true };
    const b = providerBody(f, stored);
    expect(b).toMatchObject({ proxy: "", timeout_ms: 900000, headers: "", env: " HTTPS_PROXY=" });
    expect(b).not.toHaveProperty("models");
    expect(b).not.toHaveProperty("effort");
  });
  it("unchecking a model sends the new list", () => {
    const f = formFromView(stored);
    f.models[1] = { ...f.models[1], checked: false };
    expect(providerBody(f, stored).models).toEqual([{ id: "m1", context_window: 1000000, efforts: ["high", "max"], default_effort: "high" }]);
  });
  it.each(["responses", "chat"] as const)("%s direct API providers send no launcher-only fields", (api) => {
    const b = providerBody(ready({ kind: "api", api, small_model: "s", args: "--x" }), null);
    expect(b).toMatchObject({ kind: "api", api, small_model: "", args: [], auth: "api_key" });
  });
  it("retains the protocol when editing and still requires a key for new chat providers", () => {
    const old: ProviderView = { name: "chat", kind: "api", api: "chat", model: "m1", base_url: "https://gw.example.com", has_key: true };
    const form = formFromView(old);
    expect(providerBody(form, old)).toMatchObject({ kind: "api", api: "chat", auth: "api_key" });
    expect(formProblems({ ...form, api_key: "" }, true).connect).toBe("要填 API Key");
    expect(formProblems(form, false, true).connect).toBe("");
  });
});

describe("formFromView", () => {
  it("brings back what was saved", () => {
    const f = formFromView(stored);
    expect(f).toMatchObject({ name: "gw", auth: "bearer", opus_model: "m1", subagent_force: true, proxy: "http://proxy.example.com:8080", timeout_ms: "600000", effort: "high", headers: "", headers_touched: false });
    expect(f.models.map((m) => [m.id, m.context_window, m.checked])).toEqual([["m1", "1000000", true], ["m2", "", true]]);
  });
  it("keeps a default model that is not in the list", () => {
    expect(formFromView({ name: "x", model: "solo" }).models.map((m) => m.id)).toEqual(["solo"]);
  });
});

describe("fallbackTitle", () => {
  it("is what an empty display name ends up as: the startup ID, else the host the hub names it after (#5029)", () => {
    expect(fallbackTitle(ready({ name: "", base_url: "https://relay.example.com/v1" }), true)).toBe("relay.example.com");
    // the hub names it after the host without its port or IPv6 brackets
    expect(fallbackTitle(ready({ name: "", base_url: "https://GW.example.com:8443/v1" }), true)).toBe("gw.example.com");
    expect(fallbackTitle(ready({ name: "", base_url: "http://[::1]:8080" }), true)).toBe("::1");
    expect(fallbackTitle(ready({ name: " my_gw " }), true)).toBe("my_gw");
    expect(fallbackTitle(formFromView(stored), true)).toBe("gw");
    // an older hub names nothing; an address that does not parse names nothing yet
    expect(fallbackTitle(ready({ name: "" }), false)).toBe("");
    expect(fallbackTitle(ready({ name: "", base_url: "relay.example" }), true)).toBe("");
  });
});

describe("probes", () => {
  it("by name when editing, the typed key and touched headers over it", () => {
    expect(probeBody(ready({ api_key: "" }), "gw", "m1")).toEqual({ base_url: "https://gw.example.com", auth: "bearer", api: "messages", name: "gw", model: "m1" });
    expect(probeBody(ready({ api: "responses", headers: "", headers_touched: true }), null)).toEqual({ base_url: "https://gw.example.com", auth: "bearer", api: "responses", api_key: "sk-test-1", headers: "" });
  });
  it("words the test result", () => {
    expect(testText({ ok: true, ms: 412 })).toBe("连接成功（412 ms）");
    expect(testText({ ok: false, ms: 0, error: "401 认证失败" })).toBe("401 认证失败");
  });
  it("tests with the default, else the first checked model", () => {
    expect(testModel(ready())).toBe("m1");
    expect(testModel(ready({ model: "", models: [formModel({ id: "a" }, false), formModel({ id: "b" })] }))).toBe("b");
  });
  it("keeps existing Chat effort metadata and drops only inconsistent new discovery metadata", () => {
    const old = [formModel({ id: "a", efforts: ["high"], default_effort: "high" }), formModel({ id: "empty" })];
    const found = [
      { id: "a", context_window: 100, efforts: ["low"], default_effort: "low" },
      { id: "empty", context_window: 200, efforts: ["low"], default_effort: "low" },
      { id: "bad", context_window: 300, efforts: ["low"], default_effort: "high" },
      { id: "good", context_window: 400, efforts: ["high"], default_effort: "high" },
    ];
    const merged = mergeDiscovered(old, found, true);
    expect(merged[0]).toMatchObject({ context_window: "100", efforts: ["high"], default_effort: "high" });
    expect(merged[1]).toMatchObject({ context_window: "200", efforts: [], default_effort: "" });
    expect(merged[2]).toMatchObject({ context_window: "300", efforts: [], default_effort: "" });
    expect(merged[3]).toMatchObject({ context_window: "400", efforts: ["high"], default_effort: "high" });
  });
  it("merges discovered models without losing what was typed", () => {
    const first = mergeDiscovered([], [{ id: "a", context_window: 200000 }, { id: "b" }]);
    expect(first.map((m) => [m.id, m.checked])).toEqual([["a", true], ["b", true]]);
    const again = mergeDiscovered([{ ...first[0], context_window: "123" }], [{ id: "a", context_window: 200000 }, { id: "c" }]);
    expect(again.map((m) => [m.id, m.context_window, m.checked])).toEqual([["a", "123", true], ["c", "", false]]);
    const many = mergeDiscovered([], Array.from({ length: 9 }, (_, i) => ({ id: `m${i}` })));
    expect(many.every((m) => !m.checked)).toBe(true);
  });
});

describe("import preview", () => {
  const variant = (alias: string, model: string, extra: Partial<ImportedProvider> = {}): ImportedProvider =>
    ({ name: alias, model, models: [{ id: model }], aliases: [alias], sources: [{ alias, file: "~/.zshrc", hide: "ok" }], ...extra });
  const group: ImportedProvider = {
    name: "cc_kimi", base_url: "https://api.example.com/anthropic", model: "kimi", models: [{ id: "kimi" }, { id: "kimi-fast" }],
    aliases: ["cc_kimi", "cc_kimi_fast"], differs: ["tiers", "proxy"],
    sources: [{ alias: "cc_kimi", file: "~/.zshrc", hide: "ok" }, { alias: "cc_kimi_fast", file: "~/.zshrc", hide: "in_use", uses: ["主对话"] }],
    variants: [variant("cc_kimi", "kimi"), variant("cc_kimi_fast", "kimi-fast", { existing: "kimi_gw", sources: [{ alias: "cc_kimi_fast", file: "~/.zshrc", hide: "in_use", uses: ["主对话"] }] })],
  };
  const single: ImportedProvider = {
    name: "cc_glm", model: "glm", aliases: ["cc_glm"], env_names: ["DISABLE_TELEMETRY"],
    skipped_env: [{ name: "GLM_TOKEN", reason: "credential" }, { name: "PATH", reason: "computer" }, { name: "WORK", reason: "shell" }],
    sources: [{ alias: "cc_glm", file: "~/.bashrc", hide: "elsewhere", elsewhere: ["Mac Studio"] }],
  };

  it("shows the host, the model count, the aliases and where it merges", () => {
    const row = importRow({ name: "cc_glm", base_url: "https://open.example.com/api/anthropic", model: "glm-5", models: [{ id: "glm-5" }, { id: "glm-5-air" }], aliases: ["cc_glm", "cc_glm_air"], existing: "glm" });
    expect(row).toEqual({ name: "cc_glm", host: "open.example.com", models: "2 个模型", aliases: "cc_glm、cc_glm_air", action: "合并到 glm", merge: true });
    expect(importRow({ name: "a", model: "m", aliases: ["a"] })).toMatchObject({ host: "官方地址", models: "1 个模型", action: "新建", merge: false });
  });
  it("names a provider by what it will be saved under, a split group by its own name", () => {
    // an alias Persona already lists: the hub saves it as <alias>-2
    expect(importCreates({ name: "cc_glm", create_as: "cc_glm-2", aliases: ["cc_glm"] })).toBe("cc_glm-2");
    expect(importCreates(single)).toBe("cc_glm");
    expect(importRow({ ...single, create_as: "cc_glm-2" }).name).toBe("cc_glm-2");
    expect(importRow({ ...group, create_as: "cc_kimi-2" }, false).name).toBe("cc_kimi-2");
    expect(importRow({ ...group, create_as: "cc_kimi-2" }, true).name).toBe("cc_kimi");
  });
  it("counts a split group's providers on its row and on the Import button", () => {
    expect(importCanSplit(group)).toBe(true);
    expect(importCanSplit({ ...group, differs: undefined })).toBe(false);
    expect(importCanSplit({ ...group, variants: [group.variants![0]!] })).toBe(false);
    expect(importRow(group, true)).toMatchObject({ action: "2 个供应商", merge: false });
    expect(importRow(group, false)).toMatchObject({ action: "新建" });
    const picked = new Set(["cc_kimi", "cc_glm"]);
    expect(importCount([group, single], picked, new Set(["cc_kimi"]))).toBe(3);
    expect(importCount([group, single], picked, new Set())).toBe(2);
    expect(importCount([group, single], new Set(["cc_glm"]), new Set(["cc_kimi"]))).toBe(1);
    // a group without the choice never counts as split
    expect(importCount([single], new Set(["cc_glm"]), new Set(["cc_glm"]))).toBe(1);
  });
  it("merges only when a picked group, as chosen, lands in a provider that is there", () => {
    const both = new Set(["cc_kimi", "cc_glm"]);
    // split: cc_kimi_fast's variant merges into kimi_gw
    expect(importMerges([group, single], both, new Set(["cc_kimi"]))).toBe(true);
    // one provider: the group itself has nowhere to merge
    expect(importMerges([group, single], both, new Set())).toBe(false);
    // the group that would merge is not picked
    expect(importMerges([group, single], new Set(["cc_glm"]), new Set(["cc_kimi"]))).toBe(false);
    expect(importMerges([{ ...single, existing: "glm" }], new Set(["cc_glm"]), new Set())).toBe(true);
    expect(importMerges([{ ...single, existing: "glm" }], new Set(), new Set())).toBe(false);
  });
  it("says where the aliases came from, what they bring and what they leave out", () => {
    expect(importFrom(group)).toEqual(["来自 ~/.zshrc 里的 cc_kimi、cc_kimi_fast"]);
    expect(importFrom({ ...single, aliases: ["cc_glm", "cc_x"], sources: [...single.sources!, { alias: "cc_x", file: "~/.zshrc", hide: "none" }] }))
      .toEqual(["来自 ~/.bashrc 里的 cc_glm", "来自 ~/.zshrc 里的 cc_x"]);
    // an older hub names no file
    expect(importFrom({ name: "a", aliases: ["a", "b"] })).toEqual(["来自 a、b"]);
    expect(importBrings(single)).toBe("一并带上 DISABLE_TELEMETRY。");
    expect(importBrings(group)).toBe("");
    expect(importLeftOut(single)).toEqual(["不带上 GLM_TOKEN：看起来是密钥或密码。", "不带上 WORK：值要在 shell 里才能算出来。", "不带上 PATH：只适用于那台电脑。"]);
    expect(importLeftOut({ ...single, skipped_env: [{ name: "A", reason: "limit" }, { name: "B", reason: "new" }] })).toEqual(["不带上 A：一个供应商最多带 50 个。", "不带上 B。"]);
    expect(importDiffers(group)).toBe("这几个 alias 的各档模型和代理不一样。");
    expect(importDiffers({ ...group, differs: ["effort", "someday"] })).toBe("这几个 alias 的默认思考档位和someday不一样。");
    expect(importDiffers(single)).toBe("");
  });
  it("speaks for the sources of the providers the picked groups land in", () => {
    const split = importSources([group, single], new Set(["cc_kimi", "cc_glm"]), new Set(["cc_kimi"]));
    expect(split.map((s) => [s.alias, s.hide])).toEqual([["cc_kimi", "ok"], ["cc_kimi_fast", "in_use"], ["cc_glm", "elsewhere"]]);
    expect(importSources([group, single], new Set(["cc_glm"]), new Set())).toEqual(single.sources);
    expect(importStays(group.sources![1]!)).toBe("cc_kimi_fast 保留：主对话在用它。");
    expect(importStays(single.sources![0]!)).toBe("cc_glm 保留：新的供应商无法在Mac Studio上替代它。");
    expect(importStays({ alias: "x", hide: "in_use" })).toBe("x 保留。");
  });
  it("sums up and words the two errors the hub names", () => {
    expect(importSummary({ created: ["a", "b"], updated: ["c"] })).toBe("新建 2 个，合并 1 个");
    expect(importSummary({ created: ["a", "b"], updated: ["c"], hidden: [], kept: [] })).toBe("新建 2 个，合并 1 个");
    expect(importSummary({ created: ["a"], updated: [], hidden: ["a", "b"], kept: [{ alias: "c", hide: "in_use", uses: ["主对话"] }] })).toBe("新建 1 个，合并 0 个；隐藏 alias 2 个，保留 1 个");
    expect(importErrorText(new ApiError(409, "x", { code: "node_outdated" }))).toBe("这台电脑的 Persona 需要更新");
    expect(importErrorText(new ApiError(503, "x", { code: "offline" }))).toBe("这台电脑不在线");
    expect(importErrorText(new ApiError(502, "坏了", { code: "internal" }))).toContain("HTTP 502");
    expect(importErrorText(new ApiError(502, "private internal exception", { code: "internal" }))).not.toContain("private internal exception");
  });
});

describe("autoCheckable (#4881)", () => {
  const saved: ProviderView = { name: "gw", base_url: "https://gw.example.com", auth: "bearer", has_key: true };
  it("checks a new gateway once its address is valid and a whole key is typed, whatever its name", () => {
    expect(autoCheckable(ready({ name: "" }), null)).toBe(true);
    expect(autoCheckable(ready({ name: "1bad" }), null)).toBe(true);
    expect(autoCheckable(ready({ base_url: "" }), null)).toBe(false);
    expect(autoCheckable(ready({ base_url: "gw.example.com" }), null)).toBe(false);
    expect(autoCheckable(ready({ api_key: "" }), null)).toBe(false);
    expect(autoCheckable(ready({ api_key: "sk-1" }), null)).toBe(false);
    // Codex may leave both out, but there is nothing to check without an address
    expect(autoCheckable(ready({ agent: "codex", base_url: "", api_key: "" }), null)).toBe(false);
  });
  it("checks a saved provider only once its address, key or sign-in method changed", () => {
    const opened = { ...formFromView(saved), api_key: "" };
    expect(autoCheckable(opened, saved)).toBe(false);
    expect(autoCheckable({ ...opened, base_url: "https://gw.example.com " }, saved)).toBe(false);
    expect(autoCheckable({ ...opened, base_url: "https://other.example.com" }, saved)).toBe(true);
    expect(autoCheckable({ ...opened, api_key: "sk-new-key-1" }, saved)).toBe(true);
    expect(autoCheckable({ ...opened, auth: "api_key" }, saved)).toBe(true);
    // a new address with no key stored and none typed has nothing to sign in with
    expect(autoCheckable({ ...opened, base_url: "https://other.example.com" }, { ...saved, has_key: false })).toBe(false);
  });
});

describe("fillFromCatalog", () => {
  const typed = (over: Partial<ReturnType<typeof formModel>> = {}) => ({ ...formModel({ id: "claude-fable-5-1" }), ...over });
  const found = [{ id: "claude-fable-5-1", name: "Fable 5.1", context_window: 1000000, efforts: ["low", "medium", "high"], default_effort: "medium" }];

  it("fills a typed model's empty name, window and thinking levels from the hub's catalog (#4975)", () => {
    const [m] = fillFromCatalog([typed()], found);
    expect(m).toMatchObject({ id: "claude-fable-5-1", name: "Fable 5.1", context_window: "1000000", efforts: ["low", "medium", "high"], default_effort: "medium", checked: true });
  });

  it("keeps what the user typed meanwhile, and ignores models not in the list", () => {
    const rows = [typed({ name: "Mine", context_window: "300000", efforts: ["high"] }), formModel({ id: "other" })];
    const out = fillFromCatalog(rows, [...found, { id: "nope", context_window: 1 }]);
    expect(out[0]).toMatchObject({ name: "Mine", context_window: "300000", efforts: ["high"], default_effort: "" });
    expect(out[1]).toBe(rows[1]);
    expect(fillFromCatalog(rows, [])).toBe(rows);
  });

  it("takes no thinking levels for Chat Completions", () => {
    const [m] = fillFromCatalog([typed()], found, true);
    expect(m).toMatchObject({ context_window: "1000000", efforts: [], default_effort: "" });
  });
});
