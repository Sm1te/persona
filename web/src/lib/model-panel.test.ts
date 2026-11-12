// The unified model panel's pure parts (#5032): which rows each view lists and in which groups, the search, the setup a
// row's pick sends, what is kept (favorites, recent setups, overrides), when a row can be picked or customized, and
// where the list scrolls and the flyout sits.
import { describe, expect, it } from "vitest";
import {
  addFavorite,
  alignedScrollTop,
  contextSize,
  copySetup,
  effortColor,
  effortColorAt,
  flyoutPlace,
  matchesQuery,
  overrideKey,
  panelSections,
  pushRecent,
  RECENT_MAX,
  removeFavorite,
  resetOverride,
  rowBlock,
  rowCustomizable,
  rowSetup,
  sameSetup,
  setOverride,
  updateFavorite,
  type PanelFavorite,
  type PanelModel,
  type PanelProvider,
  type PanelState,
  type SectionsInput,
} from "./model-panel";

const CLAUDE_EFFORTS = ["low", "medium", "high", "xhigh", "max"];
const opus: PanelModel = { id: "opus", name: "Opus 5.5", resolved: "claude-opus-5-5", description: "Most capable", efforts: CLAUDE_EFFORTS, defaultEffort: "high", context: 1_000_000 };
const sonnet: PanelModel = { id: "sonnet", name: "Sonnet 5", resolved: "claude-sonnet-5", description: "Fast and smart", efforts: CLAUDE_EFFORTS, defaultEffort: "medium" };
const haiku: PanelModel = { id: "haiku", name: "Haiku 4.5", efforts: [] };
const typedBefore: PanelModel = { id: "vendor/model-x", name: "vendor/model-x", efforts: [], custom: true };
const gpt: PanelModel = { id: "gpt-5.5", name: "GPT 5.5", efforts: ["minimal", "low", "medium", "high", "xhigh"], defaultEffort: "medium", fast: true };

const claude = (over: Partial<PanelProvider> = {}): PanelProvider => ({ id: "claude", name: "Claude", engine: "claude", models: [opus, sonnet, haiku, typedBefore], typable: true, ...over });
const gateway = (over: Partial<PanelProvider> = {}): PanelProvider => ({ id: "gw", name: "Gateway", engine: "claude", models: [{ ...opus, id: "gw-opus", resolved: undefined, name: "Gateway Opus" }], ...over });
const codex = (over: Partial<PanelProvider> = {}): PanelProvider => ({ id: "codex", name: "Codex", engine: "codex", models: [gpt], ...over });

const state = (over: Partial<PanelState> = {}): PanelState => ({ current: { provider: "claude", model: "claude-opus-5-5", effort: "xhigh" }, recommended: ["claude"], ...over });

const input = (over: Partial<SectionsInput> = {}): SectionsInput => ({
  providers: [claude(), gateway(), codex()],
  state: state(),
  view: { kind: "all" },
  query: "",
  favorites: [],
  recent: [],
  overrides: {},
  ...over,
});

const shape = (o: Partial<SectionsInput> = {}) => panelSections(input(o)).map((s) => `${s.kind}${s.provider ? `:${s.provider.id}` : ""}${s.loading ? " (loading)" : ""} [${s.rows.map((r) => `${r.kind === "model" ? "" : `${r.kind}:`}${r.model.id}${r.current ? "*" : ""}`).join(", ")}]`);

describe("the sections of a view", () => {
  it("All: Recommended is the model that runs now, then the recommended providers' other models, which leave their own group", () => {
    expect(shape()).toEqual([
      "recommended [opus*, sonnet, haiku, vendor/model-x]",
      "provider:gw [gw-opus]",
      "provider:codex [gpt-5.5]",
    ]);
  });

  it("All: favorites come first and stay in their providers' groups too", () => {
    const favorites: PanelFavorite[] = [{ uid: "f1", provider: "gw", model: "gw-opus", effort: "low" }];
    expect(shape({ favorites })).toEqual([
      "favorites [favorite:gw-opus]",
      "recommended [opus*, sonnet, haiku, vendor/model-x]",
      "provider:gw [gw-opus]",
      "provider:codex [gpt-5.5]",
    ]);
  });

  it("All with nothing recommended: the model that runs now still leads, and only it leaves its group", () => {
    expect(shape({ state: state({ recommended: [] }) })).toEqual([
      "recommended [opus*]",
      "provider:claude [sonnet, haiku, vendor/model-x]",
      "provider:gw [gw-opus]",
      "provider:codex [gpt-5.5]",
    ]);
  });

  it("a provider's view lists its favorites, then all its models, the current one in place", () => {
    const favorites: PanelFavorite[] = [{ uid: "f1", provider: "gw", model: "gw-opus" }, { uid: "f2", provider: "claude", model: "sonnet", effort: "max" }];
    expect(shape({ view: { kind: "provider", provider: "claude" }, favorites })).toEqual([
      "favorites [favorite:sonnet]",
      "provider:claude [opus*, sonnet, haiku, vendor/model-x]",
    ]);
  });

  it("Favorites lists the saved setups only; Recent the last ones applied, the one running now marked", () => {
    const favorites: PanelFavorite[] = [{ uid: "f1", provider: "codex", model: "gpt-5.5", fast: true }];
    expect(shape({ view: { kind: "favorites" }, favorites })).toEqual(["favorites [favorite:gpt-5.5]"]);
    const recent = [{ provider: "claude", model: "opus", effort: "xhigh" }, { provider: "claude", model: "opus", effort: "low" }, { provider: "gone", model: "x" }];
    expect(shape({ view: { kind: "recent" }, recent })).toEqual(["recent [recent:opus*, recent:opus]"]);
    expect(shape({ view: { kind: "recent" } })).toEqual([]);
    expect(shape({ view: { kind: "favorites" } })).toEqual([]);
  });

  it("a recent setup saved as a favorite carries that favorite (its star removes it)", () => {
    const favorites: PanelFavorite[] = [{ uid: "f9", provider: "claude", model: "opus", effort: "low" }];
    const [s] = panelSections(input({ view: { kind: "recent" }, favorites, recent: [{ provider: "claude", model: "opus", effort: "low" }, { provider: "claude", model: "opus", effort: "high" }] }));
    expect(s.rows.map((r) => r.starredUid)).toEqual(["f9", undefined]);
  });

  it("a provider whose list is being read shows its header and a spinner; one that failed or can't be used shows its header", () => {
    const providers = [claude(), gateway({ models: null, loading: true }), codex({ models: null, error: "offline" }), { ...codex(), id: "cx2", models: null, unavailable: "Needs a new conversation" }];
    expect(shape({ providers })).toEqual([
      "recommended [opus*, sonnet, haiku, vendor/model-x]",
      "provider:gw (loading) []",
      "provider:codex []",
      "provider:cx2 []",
    ]);
    // under a search, only the waiting one stays (and Claude Code's offer to use what is typed as a model ID)
    expect(shape({ providers, query: "zzz" })).toEqual(["provider:claude [typed:zzz]", "provider:gw (loading) []"]);
  });

  it("an emptied group leaves no header", () => {
    expect(shape({ providers: [claude({ models: [opus] }), codex()] })).toEqual(["recommended [opus*]", "provider:codex [gpt-5.5]"]);
  });
});

describe("the search", () => {
  it("matches every word against the name, id, concrete model, description and provider", () => {
    const p = claude();
    expect(matchesQuery(p, opus, "opus")).toBe(true);
    expect(matchesQuery(p, opus, "claude-opus-5-5")).toBe(true);
    expect(matchesQuery(p, opus, "CAPABLE most")).toBe(true);
    expect(matchesQuery(p, opus, "claude opus")).toBe(true);
    expect(matchesQuery(p, opus, "opus sonnet")).toBe(false);
    expect(matchesQuery(gateway(), gateway().models![0], "gateway")).toBe(true);
  });

  it("searches every provider whatever cell is on", () => {
    expect(shape({ view: { kind: "provider", provider: "codex" }, query: "opus" })).toEqual(["recommended [opus*]", "provider:gw [gw-opus]"]);
    expect(shape({ view: { kind: "favorites" }, query: "gpt" })).toEqual(["provider:claude [typed:gpt]", "provider:codex [gpt-5.5]"]);
  });

  it("offers a typed model ID as “Use …” in each provider that takes typed IDs and a pick here", () => {
    expect(shape({ query: "vendor/new-model" })).toEqual(["provider:claude [typed:vendor/new-model]"]);
    // not a valid ID, already listed, or a provider that can't take a pick here: nothing to offer
    expect(shape({ query: "two words" })).toEqual([]);
    expect(shape({ query: "vendor/model-x" })).toEqual(["recommended [vendor/model-x]"]);
    expect(shape({ query: "vendor/new-model", providers: [claude({ unavailable: "another engine" })] })).toEqual([]);
    expect(shape({ query: "vendor/new-model", providers: [claude({ locked: "--model" })] })).toEqual([]);
    // a provider that isn't recommended (an Agent's other launcher, switched to with the next message) offers it too
    expect(shape({ query: "vendor/new-model", state: state({ recommended: [] }) })).toEqual(["provider:claude [typed:vendor/new-model]"]);
    const [s] = panelSections(input({ query: "vendor/new-model" }));
    expect(s.rows[0].setup).toEqual({ provider: "claude", model: "vendor/new-model" });
    // a word that names a listed model is a search: no "Use" row beside its match
    expect(shape({ query: "son" })).not.toContain("provider:claude [typed:son]");
    expect(shape({ query: "son" }).join(" ")).not.toContain("typed:son");
  });
});

describe("the setup a row sends", () => {
  it("the model that runs now: what runs", () => {
    expect(rowSetup(claude(), opus, state())).toEqual({ provider: "claude", model: "opus", effort: "xhigh" });
    expect(rowSetup(codex(), gpt, state({ current: { provider: "codex", model: "gpt-5.5", effort: "low", fast: true } }))).toEqual({ provider: "codex", model: "gpt-5.5", effort: "low", fast: true });
  });

  it("another model keeps the running effort when it takes it, else its own default", () => {
    expect(rowSetup(claude(), sonnet, state())).toEqual({ provider: "claude", model: "sonnet", effort: "xhigh" });
    expect(rowSetup(claude(), haiku, state())).toEqual({ provider: "claude", model: "haiku" });
    expect(rowSetup(codex(), gpt, state({ current: { provider: "claude", model: "opus", effort: "max" } }))).toEqual({ provider: "codex", model: "gpt-5.5" });
  });

  it("an override wins for its effort, and is the only source of Fast", () => {
    expect(rowSetup(claude(), sonnet, state(), { effort: "low" })).toEqual({ provider: "claude", model: "sonnet", effort: "low" });
    expect(rowSetup(codex(), gpt, state(), { effort: "high", fast: true })).toEqual({ provider: "codex", model: "gpt-5.5", effort: "high", fast: true });
    // Fast on a model that has none, an effort it doesn't take: left out
    expect(rowSetup(claude(), sonnet, state(), { effort: "ultra", fast: true })).toEqual({ provider: "claude", model: "sonnet", effort: "xhigh" });
  });

  it("a saved copy drops what the model no longer takes; one whose list isn't read stays as it is", () => {
    expect(copySetup({ provider: "claude", model: "opus", effort: "ultra", fast: true }, opus, true)).toEqual({ provider: "claude", model: "opus" });
    expect(copySetup({ provider: "codex", model: "gpt-5.5", effort: "low", fast: true }, gpt, true)).toEqual({ provider: "codex", model: "gpt-5.5", effort: "low", fast: true });
    expect(copySetup({ provider: "x", model: "m", effort: "low" }, { id: "m", name: "m", efforts: [] }, false)).toEqual({ provider: "x", model: "m", effort: "low" });
  });

  it("rows of the current model carry its override flag; favorites their copy", () => {
    const favorites: PanelFavorite[] = [{ uid: "f1", provider: "claude", model: "sonnet", effort: "low" }];
    const sections = panelSections(input({ favorites, overrides: { [overrideKey("claude", "sonnet")]: { effort: "max" } } }));
    const fav = sections[0].rows[0];
    expect(fav.setup).toEqual({ provider: "claude", model: "sonnet", effort: "low" });
    const row = sections[1].rows.find((r) => r.model.id === "sonnet")!;
    expect(row.customized).toBe(true);
    expect(row.setup.effort).toBe("max");
  });
});

describe("what is kept", () => {
  it("favorites: a copy each, an identical setup once, removed by uid, edited without twins", () => {
    let f = addFavorite([], { provider: "claude", model: "opus", effort: "high", fast: false }, "a");
    expect(f).toEqual([{ uid: "a", provider: "claude", model: "opus", effort: "high" }]);
    expect(addFavorite(f, { provider: "claude", model: "opus", effort: "high" }, "b")).toBe(f);
    f = addFavorite(f, { provider: "claude", model: "opus", effort: "low" }, "b");
    expect(f.map((x) => x.uid)).toEqual(["a", "b"]);
    expect(updateFavorite(f, "b", { effort: "max" })).toEqual([f[0], { uid: "b", provider: "claude", model: "opus", effort: "max" }]);
    // made the same as "a": "b" goes
    expect(updateFavorite(f, "b", { effort: "high" })).toEqual([f[0]]);
    expect(removeFavorite(f, "a").map((x) => x.uid)).toEqual(["b"]);
  });

  it("recent: most recent first, an identical setup moves up, at most five", () => {
    let r: { provider: string; model: string; effort?: string }[] = [];
    for (const m of ["a", "b", "c", "d", "e", "f"]) r = pushRecent(r, { provider: "p", model: m });
    expect(r.map((c) => c.model)).toEqual(["f", "e", "d", "c", "b"]);
    expect(r).toHaveLength(RECENT_MAX);
    r = pushRecent(r, { provider: "p", model: "c" });
    expect(r.map((c) => c.model)).toEqual(["c", "f", "e", "d", "b"]);
    r = pushRecent(r, { provider: "p", model: "c", effort: "low" });
    expect(r.map((c) => `${c.model}${c.effort ?? ""}`)).toEqual(["clow", "c", "f", "e", "d"]);
  });

  it("overrides: merged per model, an emptied one goes, reset removes it", () => {
    let o = setOverride({}, "claude", "opus", { effort: "low" });
    o = setOverride(o, "claude", "opus", { fast: true });
    expect(o).toEqual({ [overrideKey("claude", "opus")]: { effort: "low", fast: true } });
    expect(setOverride(o, "claude", "opus", { effort: undefined, fast: undefined })).toEqual({});
    expect(resetOverride(o, "claude", "opus")).toEqual({});
    expect(resetOverride(o, "claude", "sonnet")).toBe(o);
  });

  it("the same setup ignores empty parts", () => {
    expect(sameSetup({ provider: "p", model: "m", effort: "", fast: false }, { provider: "p", model: "m" })).toBe(true);
    expect(sameSetup({ provider: "p", model: "m", fast: true }, { provider: "p", model: "m" })).toBe(false);
  });
});

describe("what can be picked and customized", () => {
  it("not while busy, on a provider that can't take a pick, a locked launcher or a list not read; each says why", () => {
    expect(rowBlock(claude(), {})).toBeUndefined();
    expect(rowBlock(claude(), { busy: "Working" })).toEqual({ reason: "Working" });
    expect(rowBlock(codex({ unavailable: "Needs a new conversation" }), {})).toEqual({ reason: "Needs a new conversation" });
    expect(rowBlock(claude({ locked: "Fixed by the launcher" }), {})).toEqual({ reason: "Fixed by the launcher" });
    expect(rowBlock(claude({ models: null }), {})).toEqual({ reason: "" });
    // a list shown from before and read again behind it can be picked
    expect(rowBlock(claude({ loading: true }), {})).toBeUndefined();
  });

  it("the model that runs now keeps its flyout when edits apply live, even when picks are off", () => {
    const sections = panelSections(input({ providers: [codex({ unavailable: "Codex can't change the model" })], state: state({ current: { provider: "codex", model: "gpt-5.5" }, recommended: [] }) }));
    const row = sections[0].rows[0];
    expect(row.current).toBe(true);
    expect(rowCustomizable(row, {}, true)).toBe(true);
    expect(rowCustomizable(row, {}, false)).toBe(false);
    expect(rowCustomizable(row, { busy: "Working" }, true)).toBe(true);
    const other = panelSections(input())[1].rows[0];
    expect(rowCustomizable(other, {}, true)).toBe(true);
    expect(rowCustomizable(other, { busy: "Working" }, true)).toBe(false);
    expect(rowCustomizable({ ...other, kind: "recent" }, {}, true)).toBe(false);
  });
});

describe("geometry and colors", () => {
  it("puts the current row's middle at about 35% of the list, within its scroll range", () => {
    expect(alignedScrollTop({ clientHeight: 400, scrollHeight: 2000, rowTop: 1000, rowBottom: 1054 })).toBe(Math.round(1027 - 140));
    expect(alignedScrollTop({ clientHeight: 400, scrollHeight: 2000, rowTop: 20, rowBottom: 74 })).toBe(0);
    expect(alignedScrollTop({ clientHeight: 400, scrollHeight: 2000, rowTop: 1960, rowBottom: 2000 })).toBe(1600);
    expect(alignedScrollTop({ clientHeight: 40, scrollHeight: 2000, rowTop: 500, rowBottom: 560 })).toBe(500);
  });

  it("places the flyout left of the panel, right without room, its top 12 px above the row's within the panel", () => {
    const panel = { left: 600, right: 1000, top: 200, bottom: 700 };
    expect(flyoutPlace({ row: { left: 660, right: 990, top: 400, bottom: 454 }, panel, width: 264, height: 240, screenW: 1200, screenH: 900 })).toEqual({ left: 332, top: 388, side: "left" });
    // near the panel's bottom: held so its bottom meets the panel's
    expect(flyoutPlace({ row: { left: 660, right: 990, top: 680, bottom: 700 }, panel, width: 264, height: 240, screenW: 1200, screenH: 900 }).top).toBe(460);
    // no room on the left
    expect(flyoutPlace({ row: { left: 60, right: 390, top: 300, bottom: 354 }, panel: { ...panel, left: 50, right: 450 }, width: 264, height: 240, screenW: 1200, screenH: 900 })).toEqual({ left: 454, top: 288, side: "right" });
    // room on neither side: kept in the window
    const tight = flyoutPlace({ row: { left: 20, right: 300, top: 300, bottom: 354 }, panel: { left: 10, right: 310, top: 200, bottom: 700 }, width: 264, height: 240, screenW: 320, screenH: 900 });
    expect(tight.left).toBeGreaterThanOrEqual(8);
    expect(tight.left + 264).toBeLessThanOrEqual(320 - 8 + 0.5);
  });

  it("colors an effort by the level itself, mixing two while dragging", () => {
    expect(effortColor("high")).toBe("var(--effort-high)");
    expect(effortColor("ultra")).toBe("var(--effort-max)");
    expect(effortColor("minimal")).toBe("var(--effort-minimal)");
    expect(effortColor("someday")).toBe("var(--effort-medium)");
    expect(effortColorAt(["low", "medium", "high"], 1)).toBe("var(--effort-medium)");
    expect(effortColorAt(["low", "medium", "high"], 1.25)).toBe("color-mix(in srgb, var(--effort-high) 25%, var(--effort-medium))");
  });

  it("writes context windows short", () => {
    expect(contextSize(200_000)).toBe("200K");
    expect(contextSize(1_000_000)).toBe("1M");
    expect(contextSize(999_600)).toBe("1M");
    expect(contextSize(272_000)).toBe("272K");
    expect(contextSize(1_500_000)).toBe("1.5M");
  });
});

describe("the running model's row", () => {
  it("is the row named exactly what runs, not also an alias standing for it", () => {
    const dflt: PanelModel = { id: "default", name: "Default", efforts: [], resolved: "gw-model" };
    const typed: PanelModel = { id: "gw-model", name: "gw-model", efforts: [], custom: true };
    const p: PanelProvider = { id: "gw", name: "gw", engine: "claude", models: [dflt, typed] };
    const lit = panelSections({ providers: [p], state: { current: { provider: "gw", model: "gw-model" }, recommended: ["gw"] }, view: { kind: "provider", provider: "gw" }, query: "", favorites: [], recent: [], overrides: {} })
      .flatMap((s) => s.rows).filter((r) => r.current).map((r) => r.model.id);
    expect(lit).toEqual(["gw-model"]);
    // an alias still lights when no row is named what runs
    const alias = panelSections({ providers: [{ ...p, models: [dflt] }], state: { current: { provider: "gw", model: "gw-model" }, recommended: ["gw"] }, view: { kind: "provider", provider: "gw" }, query: "", favorites: [], recent: [], overrides: {} })
      .flatMap((s) => s.rows).filter((r) => r.current).map((r) => r.model.id);
    expect(alias).toEqual(["default"]);
  });

  it("shows its Fast as it runs, not a saved override", () => {
    const m: PanelModel = { id: "gpt-x", name: "x", efforts: ["low", "high"], fast: true };
    const p = { id: "codex", models: [m] };
    expect(rowSetup(p, m, { current: { provider: "codex", model: "gpt-x" } }, { fast: true }).fast).toBeUndefined();
    expect(rowSetup(p, m, { current: { provider: "codex", model: "gpt-x", fast: true } }, {}).fast).toBe(true);
    // another model's row still takes its override
    expect(rowSetup(p, m, { current: { provider: "codex", model: "other" } }, { fast: true }).fast).toBe(true);
  });
});

