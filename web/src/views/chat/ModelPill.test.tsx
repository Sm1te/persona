// @vitest-environment jsdom
// The composer's model pill (#5032, after Cindy's): what it shows in each state and at each composer width, and how an
// Agent's pill drives the model panel: the providers and state it hands over, a pick applied model first then effort
// (a typed model ID too, #1644), busy and locked, Codex's effort and Fast, `/model` opening it, and the lists shown at
// once and read again behind them. The panel itself has its own tests (ui/model-panel); here it is a stand-in that keeps
// the props it was given.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { t } from "../../i18n";
import type { PanelProvider } from "../../lib/model-panel";
import { requestModelPicker } from "../../lib/model-switch";
import { getSession, getState, upsertSession } from "../../lib/store";
import type { Session } from "../../lib/types";
import type { ModelPanelProps } from "../../ui/model-panel/ModelPanel";

vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
});
const panel = vi.hoisted(() => ({ props: null as ModelPanelProps | null }));
vi.mock("../../ui/model-panel/ModelPanel", () => ({
  ModelPanel: (p: ModelPanelProps) => {
    panel.props = p;
    return <div data-testid="panel">{p.currentExtra}</div>;
  },
}));
const toast = vi.hoisted(() => vi.fn());
vi.mock("../../ui/atoms", async (orig) => ({ ...(await orig<typeof import("../../ui/atoms")>()), toast }));

const { AgentModelPill, ModelPill, pillEffort } = await import("./ModelPill");

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const opus = { engine: "claude", provider: "gateway", model: "Opus 5.5", effort: "high" };
const markup = (node: React.ReactElement) => renderToStaticMarkup(node);
const classOf = (html: string, attr: string) => new RegExp(`<[^>]*${attr}[^>]*class="([^"]*)"`).exec(html)?.[1] ?? new RegExp(`<[^>]*class="([^"]*)"[^>]*${attr}`).exec(html)?.[1] ?? "";

describe("the pill's states", () => {
  it("normal: provider tile, model, engine mark, effort, chevron; the tooltip and name say it all", () => {
    const html = markup(<ModelPill current={opus} onOpen={() => {}} />);
    expect(html).toMatch(/^<button type="button" data-model-pill="ready" aria-haspopup="listbox" aria-expanded="false"/);
    expect(html).toContain(`aria-label="${t("chat-modelpill.aria_effort", { model: "Opus 5.5", effort: "高" })}"`);
    expect(html).toContain('title="Claude Code · Opus 5.5 · gateway · 高"');
    // in this order: the tile's letter, the name, the engine (no "·" before it), the effort, then the chevron
    const text = html.replace(/<[^>]+>/g, "|").split("|").filter(Boolean);
    expect(text).toEqual(["G", "Opus 5.5", "高"]);
    expect(html.indexOf('data-pill-engine="claude"')).toBeGreaterThan(html.indexOf("Opus 5.5"));
    expect(html.indexOf("data-pill-effort")).toBeGreaterThan(html.indexOf('data-pill-engine="claude"'));
    expect(html).not.toContain("·</span>");
    // bare at rest, a capsule on hover and while open
    expect(html).toContain("border-transparent");
    expect(html).toContain("hover:border-line-strong");
    expect(html).toContain("aria-expanded:bg-hover");
  });

  it("takes the composer card's widths: 148 px under 600, a step smaller under 520, the tile and chevron alone under 420", () => {
    const html = markup(<ModelPill current={opus} onOpen={() => {}} />);
    const root = /^<button[^>]*class="([^"]*)"/.exec(html)![1];
    expect(root).toContain("max-w-[min(320px,100%)]");
    expect(root).toContain("min-w-[72px]");
    expect(root).toContain("@max-[600px]/composer:w-[148px]");
    expect(root).toContain("@max-[520px]/composer:text-[12px]");
    expect(root).toContain("@max-[420px]/composer:w-[64px]");
    const name = /<span title="Opus 5.5" class="([^"]*)"/.exec(html)![1];
    expect(name).toContain("@max-[600px]/composer:max-w-[108px]");
    expect(name).toContain("@max-[420px]/composer:hidden");
    expect(name).toContain("shrink-[1000]"); // the name gives way before the effort
    expect(classOf(html, 'data-pill-engine="claude"')).toContain("@max-[420px]/composer:hidden");
    // narrow, the effort may truncate (the engine and effort stay; the name gives way first)
    expect(classOf(html, "data-pill-effort")).toContain("@max-[600px]/composer:truncate");
  });

  it("Fast: a filled bolt in the neutral colour, and Fast in the tooltip", () => {
    const html = markup(<ModelPill current={{ ...opus, engine: "codex", model: "5.5", fast: true }} onOpen={() => {}} />);
    expect(html).toContain("fill-current");
    expect(html).toContain(`title="Codex · 5.5 · gateway · 高 · ${t("chat-sessiondialogs.speed_fast")}"`);
  });

  it("loading or failed, the pill keeps its 64 px under 420 px: only a waiting switch makes it grow there", () => {
    for (const html of [
      markup(<ModelPill current={{ engine: "claude", provider: "gateway" }} loading onOpen={() => {}} />),
      markup(<ModelPill current={{ engine: "claude", provider: "gateway" }} failed onOpen={() => {}} />),
    ]) {
      const root = /^<button[^>]*class="([^"]*)"/.exec(html)![1];
      expect(root).toContain("@max-[420px]/composer:w-[64px]");
      expect(root).not.toContain("@max-[420px]/composer:w-auto");
    }
  });

  it("loading before a model is known: a spinner and Loading model…", () => {
    const html = markup(<ModelPill current={{ engine: "claude", provider: "gateway" }} loading onOpen={() => {}} />);
    expect(html).toContain('data-model-pill="loading"');
    expect(html).toContain("border-t-ink-2"); // the spinner
    expect(html).toContain(t("chat-modelpill.loading"));
    // not known and not loading: what runs when no model is set
    expect(markup(<ModelPill current={{ engine: "claude", provider: "gateway" }} onOpen={() => {}} />)).toContain(t("chat-modelpill.default_model"));
  });

  it("the list failed: a warning in the error colour, said in the tooltip", () => {
    const none = markup(<ModelPill current={{ engine: "claude", provider: "gateway" }} failed onOpen={() => {}} />);
    expect(none).toContain('data-model-pill="failed"');
    expect(none).toContain(t("chat-modelpill.failed"));
    expect(none).toContain("text-red");
    const known = markup(<ModelPill current={opus} failed onOpen={() => {}} />);
    expect(known).toContain(`title="Claude Code · Opus 5.5 · gateway · 高 · ${t("chat-modelpill.failed")}"`);
    expect(known.match(/text-red/g)!.length).toBe(2); // the tile and the warning after the effort
  });

  it("pending (#5035): a clock first, the next engine named, both setups in the tooltip", () => {
    const next = { engine: "codex", provider: "cx", model: "5.5", effort: "low" };
    const html = markup(<ModelPill current={opus} pending={next} onOpen={() => {}} />);
    expect(html).toContain('data-model-pill="pending"');
    expect(html.indexOf("<svg")).toBeLessThan(html.indexOf(">C<")); // the clock before the tile
    // the target's engine before the name, gone when the pill is narrow
    expect(html).toContain('<span data-pill-target="true" class="max-w-[96px] shrink-0 truncate text-ink-3 @max-[600px]/composer:hidden">Codex</span>');
    expect(html).toContain('data-pill-engine="codex"');
    expect(html).toContain(`title="${t("chat-modelpill.current_and_next", { current: "Claude Code · Opus 5.5 · gateway · 高", next: "Codex · 5.5 · cx · 低" })}"`);
    expect(html).toContain(`aria-label="${t("chat-modelpill.aria_pending_effort", { agent: "Codex", model: "5.5", effort: "低" })}"`);
  });

  it("pending on another launcher of the same engine: that launcher named and drawn before the model", () => {
    const next = { engine: "claude", provider: "work", model: "GLM 5.3", effort: "high" };
    const html = markup(<ModelPill current={opus} pending={next} onOpen={() => {}} />);
    expect(html).toMatch(/data-pill-target[^>]*>work<\/span>/);
    expect(html).toContain(">W<"); // the target's tile
    // under 420 px the clock, the tile and the chevron: wider than the plain pill's 64 px
    expect(html).toContain("@max-[420px]/composer:w-auto");
    expect(html).not.toContain("@max-[420px]/composer:w-[64px]");
    expect(html).toContain(`title="${t("chat-modelpill.current_and_next", { current: "Claude Code · Opus 5.5 · gateway · 高", next: "Claude Code · GLM 5.3 · work · 高" })}"`);
    expect(html).toContain(`aria-label="${t("chat-modelpill.aria_pending_effort", { agent: "work", model: "GLM 5.3", effort: "高" })}"`);
  });

  it("disabled while a switch is on its way", () => {
    const html = markup(<ModelPill current={opus} disabled onOpen={() => {}} />);
    expect(html).toContain("disabled=\"\"");
    expect(html).toContain("pointer-events-none");
    expect(html).toContain("opacity-50");
  });

  it("a label, not a button, where nothing can be switched", () => {
    const html = markup(<ModelPill current={opus} />);
    expect(html).toMatch(/^<span data-model-pill="label" title="Claude Code · Opus 5.5 · gateway · 高"/);
    expect(html).not.toContain("<button");
    expect(html).toContain(`<span class="sr-only">${t("chat-modelpill.aria_effort", { model: "Opus 5.5", effort: "高" })}</span>`);
    expect(html.match(/<svg/g)).toBeNull(); // no chevron (Claude's mark is an image)
  });

  it("says every effort in full outside English (ModelPill.en.test.tsx has English's short Extra)", () => {
    expect(pillEffort("xhigh")).toBe("很高");
    expect(pillEffort("max")).toBe("最高");
  });
});

// ── an Agent's pill and its panel ──
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const flush = () => act(async () => { for (let i = 0; i < 8; i++) await Promise.resolve(); });
const ALL = ["low", "medium", "high", "xhigh", "max"];
const claudeList = () => ({ agent: "claude", models: [
  { id: "default", name: "Default", default: true, resolved: "claude-opus-5-5", efforts: ALL },
  { id: "opus", name: "Opus 5.5", resolved: "claude-opus-5-5", efforts: ALL },
  { id: "haiku", name: "Haiku 5.5", resolved: "claude-haiku-5-5", efforts: ["low", "high"] },
  { id: "vendor/model-x", name: "vendor/model-x", custom: true },
] });
let host: HTMLDivElement;
let root: Root;
let calls: { method: string; url: string; body?: unknown }[];
let listAnswer: (url: string) => Response;
let modesAnswer: (body: unknown) => Response;
let forgetAnswer: () => Response;
let quotaAnswer: () => Response;
// lists answer once this settles (a test that wants to see what shows while one is read holds it)
let hold: Promise<unknown>;
let machine = 0;

const agent = (x: Partial<Session> = {}): Session =>
  ({ machine_id: `mac-${machine}`, session_id: "s1", title: "整理发布说明", cwd: "/code", status: "idle", state: "idle", agent: "claude", launcher: "gateway", model: "claude-opus-5-5", effort: "high", pane_id: "p1", ...x }) as Session;

beforeEach(() => {
  machine++;
  panel.props = null;
  toast.mockClear();
  Object.assign(getState(), { features: [], sessions: {} });
  calls = [];
  listAnswer = (url) => (url.includes("/cx/") ? json({ agent: "codex", models: [{ id: "gpt-5.5", name: "gpt-5.5", efforts: ["low", "high"] }] }) : json(claudeList()));
  modesAnswer = () => json({ ok: true });
  forgetAnswer = () => json({ ok: true });
  quotaAnswer = () => json({ unsupported: true });
  hold = Promise.resolve();
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const body = init?.body ? JSON.parse(String(init.body)) : undefined;
    calls.push({ method: init?.method ?? "GET", url, body });
    if (url.includes("/models/custom")) return forgetAnswer();
    if (url.includes("/quota")) return quotaAnswer();
    if (url.includes("/models")) return hold.then(() => listAnswer(url));
    if (/\/launchers(\?|$)/.test(url)) return json({ launchers: [{ name: "claude" }, { name: "gateway" }, { name: "cx", agent: "codex" }] });
    if (url.endsWith("/modes")) return modesAnswer(body);
    return json({});
  }));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
});

async function show(s: Session) {
  upsertSession(s);
  act(() => root.render(<AgentModelPill session={s} />));
  await flush();
}
const pill = () => host.querySelector<HTMLButtonElement>("button[data-model-pill]");
async function open() {
  await act(async () => pill()!.click());
  await act(async () => vi.dynamicImportSettled());
  await flush();
}
const modes = () => calls.filter((c) => c.url.endsWith("/modes")).map((c) => c.body);
const provider = (id: string) => panel.props!.providers.find((p: PanelProvider) => p.id === id)!;

describe("an Agent's pill", () => {
  it("reads its own list once for the model's name, and opens the panel with the computer's launchers", async () => {
    await show(agent());
    expect(pill()!.textContent).toContain("Opus 5.5");
    expect(calls.filter((c) => c.url.includes("/models")).map((c) => c.url.split("/launchers/")[1])).toEqual(["gateway/models"]);
    await open();
    expect(host.querySelector('[data-testid="panel"]')).not.toBeNull();
    expect(pill()!.getAttribute("aria-expanded")).toBe("true");
    expect(panel.props!.scope).toBe("agent");
    expect(panel.props!.anchor).toBe(pill());
    expect(panel.props!.providers.map((p) => p.id)).toEqual(["claude", "gateway", "cx"]);
    expect(panel.props!.state).toMatchObject({ current: { provider: "gateway", model: "opus", effort: "high" }, recommended: ["gateway"] });
    expect(provider("cx").unavailable).toBe(t("chat-modelpanel.other_engine", { from: "Claude Code", to: "Codex" }));
    // a second click closes it
    await act(async () => pill()!.click());
    expect(host.querySelector('[data-testid="panel"]')).toBeNull();
  });

  it("reads each provider's cached quota for this computer, without forcing a refresh", async () => {
    quotaAnswer = () => json({ machine: `mac-${machine}`, checked_at: "2026-11-12T00:00:00Z", quota: { gateway: "example", pools: [{ name: "a", windows: [{ label: "每周", used_ratio: 0.77 }] }] } });
    await show(agent());
    await open();
    for (const name of ["claude", "gateway", "cx"]) {
      expect(calls.some((c) => c.url.includes(`/launchers/${name}/quota?machine=mac-${machine}`) && !c.url.includes("refresh="))).toBe(true);
      expect(panel.props!.quotas?.[name]?.quota?.pools?.[0]?.windows?.[0]).toMatchObject({ label: "每周", used_ratio: 0.77 });
    }
  });

  it("applies a pick the model first, then the effort, and stops at a failure", async () => {
    await show(agent());
    await open();
    expect(await act(() => panel.props!.onPick({ provider: "gateway", model: "haiku", effort: "low" }))).toBe(true);
    expect(modes()).toEqual([{ model: "haiku" }, { effort: "low" }]);
    calls.length = 0;
    modesAnswer = () => json({ error: "dialog_open" }, 409);
    expect(await act(() => panel.props!.onPick({ provider: "gateway", model: "haiku", effort: "max" }))).toBe(false);
    expect(modes()).toEqual([{ model: "haiku" }]);
    expect(toast).toHaveBeenCalledWith(expect.stringContaining("dialog_open"), "error");
    // another launcher can't be applied here: nothing is sent
    calls.length = 0;
    expect(await act(() => panel.props!.onPick({ provider: "claude", model: "haiku" }))).toBe(false);
    expect(modes()).toEqual([]);
  });

  it("sends a model ID typed by hand as it is (#1644), and Forget puts a typed ID back with why when the hub says no", async () => {
    await show(agent());
    await open();
    expect(provider("gateway").typable).toBe(true);
    expect(await act(() => panel.props!.onPick({ provider: "gateway", model: "~vendor/model-latest" }))).toBe(true);
    expect(modes()).toEqual([{ model: "~vendor/model-latest" }]);
    forgetAnswer = () => json({ error: "只读设备不能改" }, 403);
    act(() => panel.props!.onForget!("gateway", "vendor/model-x"));
    expect(provider("gateway").models!.some((m) => m.id === "vendor/model-x")).toBe(false);
    await flush();
    expect(provider("gateway").models!.some((m) => m.id === "vendor/model-x")).toBe(true);
    expect(toast).toHaveBeenCalledWith(expect.stringMatching(/没能忘记「vendor\/model-x」：.*只读设备不能改/), "error");
  });

  it("is busy while the agent works, and says why a launcher that fixes its model can't take a pick", async () => {
    listAnswer = () => json({ ...claudeList(), locked: "--model" });
    await show(agent({ status: "working", state: "working" }));
    await open();
    expect(panel.props!.state.busy).toBe(t("composer.busy"));
    expect(provider("gateway").locked).toBe(t("composer.locked"));
    expect(provider("gateway").typable).toBeUndefined();
  });

  it("shows the list from before at once and reads it again behind it", async () => {
    await show(agent());
    await open();
    act(() => root.render(<></>));
    let release = () => {};
    hold = new Promise<void>((r) => (release = r));
    listAnswer = (url) => json(url.includes("/gateway/") ? { agent: "claude", models: [{ id: "opus", name: "Opus 5.5", resolved: "claude-opus-5-5" }, { id: "vendor/model-new", name: "vendor/model-new", custom: true }] } : claudeList());
    act(() => root.render(<AgentModelPill session={agent()} />));
    await act(async () => pill()!.click());
    await act(async () => vi.dynamicImportSettled());
    // at once: the last list
    expect(provider("gateway").models!.map((m) => m.id)).toContain("vendor/model-x");
    release();
    await flush();
    expect(calls.filter((c) => c.url.includes(`/machines/mac-${machine}/launchers/gateway/models`)).length).toBe(3); // the pill's, then each opening
    expect(provider("gateway").models!.map((m) => m.id)).toEqual(["opus", "vendor/model-new"]);
  });

  it("`/model` Enter in the composer opens it where the agent switches models", async () => {
    await show(agent());
    let opened = false;
    act(() => void (opened = requestModelPicker(`mac-${machine}/s1`)));
    expect(opened).toBe(true);
    await act(async () => vi.dynamicImportSettled());
    await flush();
    expect(host.querySelector('[data-testid="panel"]')).not.toBeNull();
  });

  it("a Codex thread: its own rows say why, its effort and Fast apply through the flyout, the speed only after the push", async () => {
    const codex = agent({ agent: "codex", source_kind: "codex_app_server", launcher: "cx", capabilities: { send: true }, model: "gpt-5.5", efforts: ["low", "medium", "high"], service_tiers: [{ id: "default", name: "Standard" }, { id: "priority", name: "Fast" }] });
    await show(codex);
    expect(requestModelPicker(`mac-${machine}/s1`)).toBe(false); // no model switch: `/model` goes as text
    await open();
    expect(provider("cx").unavailable).toBe(t("chat-modelpanel.model_fixed", { agent: "Codex" }));
    expect(panel.props!.state.recommended).toEqual([]);
    expect(provider("cx").models![0]).toMatchObject({ id: "gpt-5.5", efforts: ["low", "medium", "high"], fast: true });
    expect(await act(() => panel.props!.onApplyCurrent!({ fast: true }))).toBe(true);
    expect(modes()).toEqual([{ service_tier: "priority" }]);
    expect(getSession(codex.machine_id, "s1")?.service_tier).toBeUndefined();
    expect(await act(() => panel.props!.onApplyCurrent!({ effort: "low" }))).toBe(true);
    expect(modes()).toEqual([{ service_tier: "priority" }, { effort: "low" }]);
    // Standard back from Fast, once the session's push says it runs Fast (the speed is sent only when it changes)
    const fast = { ...getSession(codex.machine_id, "s1")!, service_tier: "priority" };
    upsertSession(fast);
    act(() => root.render(<AgentModelPill session={fast} />));
    await flush();
    calls.length = 0;
    await act(() => panel.props!.onApplyCurrent!({ fast: false }));
    expect(modes()).toEqual([{ service_tier: "default" }]);
  });

  it("switches to another launcher with the next message on a hub that does it: pending at once, said, put back on a refusal (#5035)", async () => {
    Object.assign(getState(), { features: ["launcher_switch"] });
    const s = agent();
    await show(s);
    await open();
    expect(provider("claude").unavailable).toBeUndefined();
    expect(await act(() => panel.props!.onPick({ provider: "claude", model: "haiku", effort: "low" }))).toBe(true);
    expect(modes()).toEqual([{ launcher: "claude", from: "gateway", model: "haiku", effort: "low" }]);
    expect(getSession(s.machine_id, "s1")?.pending_switch).toMatchObject({ launcher: "claude", model: "haiku", effort: "low" });
    expect(toast).toHaveBeenCalledWith(t("chat-modelpanel.switch_scheduled", { target: "claude · Haiku 5.5" }), "ok");
    // the pill shows it waiting
    act(() => root.render(<AgentModelPill session={getSession(s.machine_id, "s1")!} />));
    await flush();
    expect(pill()!.getAttribute("data-model-pill")).toBe("pending");
    expect(pill()!.getAttribute("title")).toBe(t("chat-modelpill.current_and_next", { current: "Claude Code · Opus 5.5 · gateway · 高", next: "Claude Code · Haiku 5.5 · claude · 低" }));
    // a refusal puts it back as it was, with the hub's words
    calls.length = 0;
    modesAnswer = () => json({ error: "这个启动器用的是另一个配置目录", code: "switch_config_dir" }, 409);
    expect(await act(() => panel.props!.onPick({ provider: "claude", model: "opus" }))).toBe(false);
    expect(getSession(s.machine_id, "s1")?.pending_switch).toMatchObject({ launcher: "claude", model: "haiku" });
    expect(toast).toHaveBeenLastCalledWith(expect.stringContaining("这个启动器用的是另一个配置目录"), "error");
  });

  it("a pending switch shows on the pill from the session, its note in the panel, and the session's own launcher drops it", async () => {
    Object.assign(getState(), { features: ["launcher_switch"] });
    const s = agent({ pending_switch: { launcher: "claude", model: "haiku", at: "2026-11-12T10:00:00Z" } });
    await show(s);
    expect(calls.some((c) => c.url.includes("/launchers/claude/models"))).toBe(true); // the target's list names its model
    expect(pill()!.getAttribute("data-model-pill")).toBe("pending");
    expect(pill()!.textContent).toContain("Haiku 5.5");
    await open();
    expect(panel.props!.state.note).toBe(t("chat-modelpanel.pending_note", { provider: "claude" }));
    expect(panel.props!.state.pending).toMatchObject({ provider: "claude", model: "haiku" });
    expect(await act(() => panel.props!.onPick({ provider: "gateway", model: "opus", effort: "high" }))).toBe(true);
    expect(modes()).toEqual([{ launcher: "gateway", from: "gateway" }]);
    expect(getSession(s.machine_id, "s1")?.pending_switch).toBeUndefined();
  });

  it("is a label where nothing switches (offline), and nothing at all without a model", async () => {
    await show(agent({ status: "offline", state: "offline" }));
    expect(pill()).toBeNull();
    expect(host.querySelector('[data-model-pill="label"]')!.textContent).toContain("opus 5.5");
    expect(calls).toEqual([]); // nothing to open, nothing read
    await show(agent({ status: "offline", state: "offline", model: undefined }));
    expect(host.innerHTML).toBe("");
  });
});
