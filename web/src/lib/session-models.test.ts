// @vitest-environment jsdom
// The model panel's data for an Agent's chat (#5032, lib/session-models.ts and lib/session-panel.ts): the computer's
// launchers as providers, the session's own the only one that switches (others wait for #5035, another engine needs a new
// conversation, a Codex thread keeps its model), the row it runs, what a pick sends and in which order, what the pill
// says, and the lists kept and read again behind them.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { t } from "../i18n";
import type { LauncherModels } from "./launcher-models";
import { currentModelRow, forgetModel, modelLabel, modelList, pillView, readModels, type ModelList } from "./session-models";
import { pickPlan, sessionPanel, speedStep } from "./session-panel";
import type { Launcher, Session } from "./types";

const ALL = ["low", "medium", "high", "xhigh", "max"];
const claudeList: LauncherModels = {
  agent: "claude",
  models: [
    { id: "default", name: "Default (recommended)", default: true, resolved: "claude-opus-5-5", efforts: ALL, default_effort: "high" },
    { id: "opus", name: "Opus 5.5", resolved: "claude-opus-5-5", efforts: ALL, default_effort: "high", description: "Most capable", price: { input: 4, output: 20, cache_read: 0.2 } },
    { id: "haiku", name: "Haiku 5.5", resolved: "claude-haiku-5-5", efforts: ["low", "high"] },
    { id: "vendor/model-x", name: "vendor/model-x", custom: true },
  ],
};
const codexList: LauncherModels = { agent: "codex", models: [{ id: "gpt-5.5", name: "gpt-5.5", efforts: ["low", "high"] }, { id: "gpt-5.5-mini", name: "gpt-5.5-mini" }] };
const launchers: Launcher[] = [{ name: "claude" }, { name: "gateway" }, { name: "cx", agent: "codex" }];

const session = (x: Partial<Session> = {}): Session =>
  ({ machine_id: "mac", session_id: "s1", title: "t", cwd: "/code", status: "idle", state: "idle", agent: "claude", launcher: "gateway", model: "claude-opus-5-5", effort: "high", context_window: 200000, ...x }) as Session;
const codex = (x: Partial<Session> = {}) =>
  session({ agent: "codex", source_kind: "codex_app_server", launcher: "cx", capabilities: { send: true }, model: "gpt-5.5", efforts: ["low", "medium", "high"], service_tiers: [{ id: "default", name: "Standard" }, { id: "priority", name: "Fast" }], ...x });
const lists = (m: Record<string, ModelList>) => (name: string) => m[name] ?? {};

describe("the providers of an Agent's chat", () => {
  it("are the computer's launchers in their order, the session's own the one that switches", () => {
    const { providers, state } = sessionPanel(session(), launchers, lists({ gateway: { list: claudeList }, claude: { list: claudeList }, cx: { list: codexList } }));
    expect(providers.map((p) => [p.id, p.engine])).toEqual([["claude", "claude"], ["gateway", "claude"], ["cx", "codex"]]);
    const [plain, own, cx] = providers;
    expect(own.unavailable).toBeUndefined();
    expect(own.typable).toBe(true);
    expect(plain.unavailable).toBe(t("chat-modelpanel.other_provider"));
    expect(cx.unavailable).toBe(t("chat-modelpanel.other_engine", { from: "Claude Code", to: "Codex" }));
    expect(state.recommended).toEqual(["gateway"]);
    // the alias that stands for what runs, not the default entry that does too
    expect(state.current).toEqual({ provider: "gateway", model: "opus", effort: "high" });
    expect(state.busy).toBeUndefined();
  });

  it("maps each model: its name, efforts, default effort, description; the row that runs knows its context window", () => {
    const { providers } = sessionPanel(session(), launchers, lists({ gateway: { list: claudeList } }));
    const own = providers.find((p) => p.id === "gateway")!;
    expect(own.models).toEqual([
      { id: "default", name: "Default (recommended)", resolved: "claude-opus-5-5", efforts: ALL, defaultEffort: "high" },
      { id: "opus", name: "Opus 5.5", resolved: "claude-opus-5-5", efforts: ALL, defaultEffort: "high", description: "Most capable", context: 200000, price: { input: 4, output: 20, cache_read: 0.2 } },
      { id: "haiku", name: "Haiku 5.5", resolved: "claude-haiku-5-5", efforts: ["low", "high"] },
      // a typed ID's efforts aren't known: Claude Code takes any
      { id: "vendor/model-x", name: "vendor/model-x", efforts: ALL, custom: true },
    ]);
  });

  it("puts the session's own launcher first when the computer doesn't list it, and says while lists are read or failed", () => {
    const { providers } = sessionPanel(session({ launcher: "gone" }), launchers, lists({ claude: { error: "claude 没有登录" } }));
    expect(providers.map((p) => p.id)).toEqual(["gone", "claude", "gateway", "cx"]);
    // the own list on its way: the model it runs is its row meanwhile (its flyout works)
    expect(providers[0]).toMatchObject({ loading: true });
    expect(providers[0].models!.map((m) => m.id)).toEqual(["claude-opus-5-5"]);
    expect(providers[1]).toMatchObject({ models: null, error: "claude 没有登录" });
    expect(providers[1].loading).toBeUndefined();
    // before the launchers are read: the own one alone
    expect(sessionPanel(session(), undefined, lists({})).providers.map((p) => p.id)).toEqual(["gateway"]);
  });

  it("adds the model it runs when the list doesn't name it, and has no current model before one is known", () => {
    const own = (s: Session, l: ModelList) => sessionPanel(s, [], lists({ gateway: l })).providers[0];
    const sonnet = own(session({ model: "claude-sonnet-4-9" }), { list: claudeList });
    expect(sonnet.models!.at(-1)).toEqual({ id: "claude-sonnet-4-9", name: "sonnet 4.9", efforts: ALL, context: 200000 });
    // an older hub has no list: the model still has its row
    expect(own(session(), { list: null }).models).toEqual([{ id: "claude-opus-5-5", name: "opus 5.5", efforts: ALL, context: 200000 }]);
    // no reply yet: the default entry is what runs
    expect(sessionPanel(session({ model: undefined }), [], lists({ gateway: { list: claudeList } })).state.current?.model).toBe("default");
    expect(sessionPanel(session({ model: undefined }), [], lists({})).state.current).toBeNull();
  });

  it("says why when the launcher fixes its model, and takes no typed ID then", () => {
    const own = sessionPanel(session(), [], lists({ gateway: { list: { ...claudeList, locked: "--model" } } })).providers[0];
    expect(own.locked).toBe(t("composer.locked"));
    expect(own.typable).toBeUndefined();
  });

  it("is busy while the agent works or waits on you", () => {
    expect(sessionPanel(session({ status: "working", state: "working" }), [], lists({})).state.busy).toBe(t("composer.busy"));
    expect(sessionPanel(session({ state: "needs_you" }), [], lists({})).state.busy).toBe(t("composer.busy"));
  });

  it("a Codex thread keeps its model: its own rows say why, the model it runs has the thread's efforts and Fast", () => {
    const { providers, state } = sessionPanel(codex({ service_tier: "priority" }), launchers, lists({ cx: { list: codexList } }));
    const own = providers.find((p) => p.id === "cx")!;
    expect(own.unavailable).toBe(t("chat-modelpanel.model_fixed", { agent: "Codex" }));
    expect(own.typable).toBeUndefined();
    expect(state.recommended).toEqual([]);
    expect(state.busy).toBeUndefined();
    expect(state.current).toEqual({ provider: "cx", model: "gpt-5.5", effort: "high", fast: true });
    expect(own.models![0]).toMatchObject({ id: "gpt-5.5", efforts: ["low", "medium", "high"], fast: true });
    expect(own.models![1].fast).toBeUndefined();
    expect(providers.find((p) => p.id === "gateway")!.unavailable).toBe(t("chat-modelpanel.other_engine", { from: "Codex", to: "Claude Code" }));
  });
});

describe("what a pick sends", () => {
  it("the model first, then the effort, each only when it differs", () => {
    expect(pickPlan(session(), { provider: "gateway", model: "haiku", effort: "low" }, () => claudeList)).toEqual([{ model: "haiku" }, { effort: "low" }]);
    expect(pickPlan(session(), { provider: "gateway", model: "opus", effort: "max" }, () => claudeList)).toEqual([{ effort: "max" }]);
    expect(pickPlan(session(), { provider: "gateway", model: "opus", effort: "high" }, () => claudeList)).toEqual([]);
    // the default entry goes by its id, as the popover sent it; while nothing is reported it is what runs
    expect(pickPlan(session(), { provider: "gateway", model: "default" }, () => claudeList)).toEqual([]);
    expect(pickPlan(session({ model: "claude-haiku-5-5" }), { provider: "gateway", model: "default" }, () => claudeList)).toEqual([{ model: "default" }]);
    expect(pickPlan(session({ model: undefined }), { provider: "gateway", model: "default" }, () => claudeList)).toEqual([]);
  });

  it("a model ID typed by hand goes as it is (#1644); one the hub would refuse doesn't go", () => {
    expect(pickPlan(session(), { provider: "gateway", model: "~vendor/model-latest" }, () => claudeList)).toEqual([{ model: "~vendor/model-latest" }]);
    expect(pickPlan(session(), { provider: "gateway", model: "vendor/model-x" }, () => claudeList)).toEqual([{ model: "vendor/model-x" }]);
    expect(pickPlan(session(), { provider: "gateway", model: "a;b" }, () => claudeList)).toBeNull();
    // the list couldn't be read: still offered
    expect(pickPlan(session(), { provider: "gateway", model: "vendor/model-y" }, () => undefined)).toEqual([{ model: "vendor/model-y" }]);
  });

  it("nothing for another launcher, or another model where the agent can't switch", () => {
    expect(pickPlan(session(), { provider: "claude", model: "haiku" }, () => claudeList)).toBeNull();
    expect(pickPlan(session({ status: "offline", state: "offline" }), { provider: "gateway", model: "haiku" }, () => claudeList)).toBeNull();
    expect(pickPlan(codex(), { provider: "cx", model: "gpt-5.5-mini" }, () => codexList)).toBeNull();
  });

  it("a Codex thread: its effort and its speed, Standard back from Fast", () => {
    expect(pickPlan(codex(), { provider: "cx", model: "gpt-5.5", effort: "low", fast: true }, () => codexList)).toEqual([{ effort: "low" }, { service_tier: "priority" }]);
    expect(pickPlan(codex({ service_tier: "priority" }), { provider: "cx", model: "gpt-5.5", fast: false }, () => codexList)).toEqual([{ service_tier: "default" }]);
    // a saved Standard setup has no `fast` (cleanSetup drops false): picking it turns Fast off
    expect(pickPlan(codex({ service_tier: "priority" }), { provider: "cx", model: "gpt-5.5" }, () => codexList)).toEqual([{ service_tier: "default" }]);
  });

  it("an Antigravity variant brings its own thinking level", () => {
    const agy = session({ agent: "agy", launcher: "agy", source_kind: "runtime", capabilities: { send: true, model: true }, model: "future-medium", effort: "medium" });
    const list: LauncherModels = { agent: "agy", models: [
      { id: "future-medium", name: "Future Medium", efforts: ["low", "medium", "high"], default_effort: "medium" },
      { id: "future-high", name: "Future High", efforts: ["low", "medium", "high"], default_effort: "high" },
    ] };
    expect(pickPlan(agy, { provider: "agy", model: "future-high" }, () => list)).toEqual([{ model: "future-high" }, { effort: "high" }]);
    // its rows have no slider: the variants are the levels
    expect(sessionPanel(agy, [], lists({ agy: { list } })).providers[0].models!.every((m) => m.efforts.length === 0)).toBe(true);
  });
});

describe("what the pill says", () => {
  it("the list's name for the model once read, a short id before; the launcher, engine, effort and speed", () => {
    expect(pillView(session(), { list: claudeList })).toEqual({ engine: "claude", provider: "gateway", model: "Opus 5.5", effort: "high", loading: false, failed: false });
    expect(pillView(session(), {}).model).toBe("opus 5.5");
    expect(pillView(codex({ service_tier: "priority" }), {})).toMatchObject({ engine: "codex", provider: "cx", model: "5.5", fast: true });
    expect(pillView(session({ launcher: undefined, agent: undefined }), {})).toMatchObject({ engine: "claude", provider: "claude" });
  });

  it("loading or failed only while there is no list; no model before one is known, the default entry's name once read", () => {
    expect(pillView(session({ model: undefined }), { loading: true })).toMatchObject({ loading: true, failed: false });
    expect(pillView(session({ model: undefined }), { loading: true }).model).toBeUndefined();
    expect(pillView(session(), { list: claudeList, loading: true }).loading).toBe(false);
    expect(pillView(session(), { error: "x" })).toMatchObject({ failed: true, model: "opus 5.5" });
    expect(pillView(session({ model: undefined }), { list: claudeList }).model).toBe("Default (recommended)");
  });

  it("names ids briefly where no list does", () => {
    expect(modelLabel("claude-opus-5-5")).toBe("opus 5.5");
    expect(modelLabel("anthropic/claude-opus-5-5")).toBe("opus 5.5");
    expect(modelLabel("gpt-5.5-codex")).toBe("5.5 Codex");
    expect(modelLabel("vendor/model-x")).toBe("model-x");
    expect(currentModelRow(claudeList.models, "CLAUDE-HAIKU-5-5")?.id).toBe("haiku");
    expect(currentModelRow(claudeList.models, "nothing")).toBeUndefined();
  });
});

// ── the lists kept for the page's life ──
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const flush = async () => { for (let i = 0; i < 10; i++) await Promise.resolve(); };

describe("reading the lists", () => {
  let waiting: { url: string; answer: (r: Response) => void }[];
  let calls: { method: string; url: string }[];
  beforeEach(() => {
    waiting = [];
    calls = [];
    vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      calls.push({ method: init?.method ?? "GET", url });
      return new Promise<Response>((answer) => waiting.push({ url, answer }));
    }));
  });
  afterEach(() => vi.unstubAllGlobals());
  const answer = async (part: string, r: Response) => {
    const at = waiting.findIndex((w) => w.url.includes(part));
    waiting.splice(at, 1)[0].answer(r);
    await flush();
  };

  it("reads once, then shows the last list at once and reads it again behind it", async () => {
    void readModels("m-once", "gateway");
    await flush();
    expect(modelList("m-once", "gateway")).toEqual({ list: undefined, loading: true });
    await answer("/machines/m-once/launchers/gateway/models", json(claudeList));
    expect(modelList("m-once", "gateway").list?.models).toHaveLength(4);
    await readModels("m-once", "gateway"); // kept: no second read
    expect(calls).toHaveLength(1);
    void readModels("m-once", "gateway", { again: true });
    await flush();
    expect(modelList("m-once", "gateway")).toMatchObject({ loading: true, list: { agent: "claude" } });
    // a failed read again keeps the list shown, and says nothing
    await answer("/machines/m-once/launchers/gateway/models", json({ error: "offline" }, 502));
    expect(modelList("m-once", "gateway")).toEqual({ list: expect.objectContaining({ agent: "claude" }) });
  });

  it("says why when the first read fails", async () => {
    void readModels("m-fail", "gateway");
    await flush();
    await answer("/machines/m-fail/launchers/gateway/models", json({ error: "claude 没有登录" }, 502));
    expect(modelList("m-fail", "gateway").error).toContain("claude 没有登录");
    expect(modelList("m-fail", "gateway").list).toBeUndefined();
  });

  it("reads at most three lists at a time, the session's own ahead of the queue", async () => {
    for (const name of ["a", "b", "c", "d"]) void readModels("m-queue", name);
    void readModels("m-queue", "own", { first: true });
    await flush();
    expect(waiting.map((w) => w.url.split("/launchers/")[1])).toEqual(["a/models", "b/models", "c/models"]);
    await answer("/launchers/a/", json(claudeList));
    expect(waiting.map((w) => w.url.split("/launchers/")[1])).toEqual(["b/models", "c/models", "own/models"]);
    for (const name of ["b", "c", "own", "d"]) await answer(`/launchers/${name}/`, json(claudeList));
    expect(waiting).toEqual([]);
  });

  it("forgets a typed ID at once, and puts it back with why when the hub says no", async () => {
    void readModels("m-forget", "gateway");
    await flush();
    await answer("/launchers/gateway/models", json(claudeList));
    const done = forgetModel("m-forget", "gateway", "vendor/model-x");
    await flush();
    expect(modelList("m-forget", "gateway").list!.models.some((m) => m.id === "vendor/model-x")).toBe(false);
    expect(calls.at(-1)).toEqual({ method: "DELETE", url: expect.stringContaining("/machines/m-forget/launchers/gateway/models/custom?model=vendor%2Fmodel-x") });
    await answer("/models/custom", json({ error: "只读设备不能改" }, 403));
    expect(await done).toMatch(/没能忘记「vendor\/model-x」：.*只读设备不能改/);
    expect(modelList("m-forget", "gateway").list!.models.map((m) => m.id)).toEqual(["default", "opus", "haiku", "vendor/model-x"]);
  });
});

describe("a speed change", () => {
  const tiers = [{ id: "default", name: "Standard" }, { id: "priority", name: "Fast" }];
  it("is sent only where the thread has Fast and only when it changes", () => {
    expect(speedStep(session({ service_tiers: tiers }), true)).toEqual({ service_tier: "priority" });
    expect(speedStep(session({ service_tiers: tiers }), false)).toBeUndefined();
    expect(speedStep(session({ service_tiers: tiers, service_tier: "priority" }), false)).toEqual({ service_tier: "default" });
    expect(speedStep(session({ service_tiers: tiers, service_tier: "priority" }), true)).toBeUndefined();
    expect(speedStep(session(), true)).toBeUndefined();
    expect(speedStep(session({ service_tiers: tiers }), undefined)).toBeUndefined();
    // the flyout's switch goes whatever the session says (its speed arrives only with the push)
    expect(speedStep(session({ service_tiers: tiers }), false, true)).toEqual({ service_tier: "default" });
    expect(speedStep(session(), false, true)).toBeUndefined();
  });
});

describe("the running model while its list is not in", () => {
  it("stays in the panel (its flyout works) while the list is read, and when it couldn't be", () => {
    for (const [e, flag] of [[{ loading: true }, "loading"], [{ error: "no CLI" }, "error"]] as const) {
      const { providers, state } = sessionPanel(session(), launchers, lists({ gateway: e }));
      const own = providers.find((p) => p.id === "gateway")!;
      expect(own[flag]).toBeTruthy();
      expect(own.models?.map((m) => m.id)).toEqual(["claude-opus-5-5"]);
      expect(own.models?.[0].efforts.length).toBeGreaterThan(1);
      expect(state.current?.model).toBe("claude-opus-5-5");
    }
    // another provider's list on its way: no rows yet
    expect(sessionPanel(session(), launchers, lists({})).providers.find((p) => p.id === "claude")!.models).toBeNull();
  });
});

