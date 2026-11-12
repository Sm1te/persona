// Claude Code's tier models in the provider wizard (#4975): what fills itself in from the checked models, what the user
// chose stays, a saved provider never changes by itself, and when the step offers a lighter model.
import { describe, expect, it } from "vitest";
import { formFromView, formModel, newForm, providerBody, type ProviderForm } from "./providers";
import { applyTiers, setTier, suggestTiers, tierAdvice, tierLines, withAutoTiers } from "./provider-tiers";

const OPUS = "claude-opus-5-5";
const SONNET = "claude-sonnet-5-5";
const HAIKU = "claude-haiku-5-5";

/** A new Claude Code gateway with these models checked and the first (or `model`) as its default. */
function gateway(ids: string[], model = ids[0], patch: Partial<ProviderForm> = {}): ProviderForm {
  return { ...newForm(), name: "gw", base_url: "https://gw.example.com", api_key: "sk-test-1", models: ids.map((id) => formModel({ id })), model, ...patch };
}
const uncheck = (f: ProviderForm, id: string) => ({ ...f, models: f.models.map((m) => (m.id === id ? { ...m, checked: false } : m)) });

describe("suggestTiers", () => {
  it("puts an ID naming a Claude tier in that tier, and leaves the default's own tier empty", () => {
    expect(suggestTiers([OPUS, SONNET, HAIKU], OPUS)).toEqual({ sonnet: SONNET, light: HAIKU });
    expect(suggestTiers([OPUS, SONNET, HAIKU], SONNET)).toEqual({ opus: OPUS, light: HAIKU });
    expect(suggestTiers([OPUS, "claude-fable-5-1"], OPUS)).toEqual({ fable: "claude-fable-5-1" });
    // a gateway's own names, a vendor prefix, the 1M marker and a date
    expect(suggestTiers(["example-opus", "example-sonnet", "example-haiku"], "example-opus")).toEqual({ sonnet: "example-sonnet", light: "example-haiku" });
    expect(suggestTiers(["anthropic/claude-opus-5-5[1m]", "claude-haiku-4-5-20251001"], "anthropic/claude-opus-5-5[1m]")).toEqual({ light: "claude-haiku-4-5-20251001" });
  });

  it("takes the default when it is in the tier, else the newest generation", () => {
    expect(suggestTiers(["claude-opus-4-8", OPUS, "claude-haiku-4-5", HAIKU], SONNET)).toEqual({ opus: OPUS, light: HAIKU });
    expect(suggestTiers(["claude-opus-4-8", OPUS, SONNET], "claude-opus-4-8")).toEqual({ sonnet: SONNET });
  });

  it("reads other families by series and size", () => {
    expect(suggestTiers(["glm-5.3", "glm-5.3-flash"], "glm-5.3")).toEqual({ light: "glm-5.3-flash" });
    expect(suggestTiers(["glm-5.3", "glm-5.3-flash"], "glm-5.3-flash")).toEqual({ opus: "glm-5.3" });
    expect(suggestTiers(["qwen3.8-max", "qwen3.8-plus", "qwen3.8-flash"], "qwen3.8-plus")).toEqual({ opus: "qwen3.8-max", light: "qwen3.8-flash" });
    expect(suggestTiers(["qwen3.8-max", "qwen3.8-plus", "qwen3.8-flash"], "qwen3.8-max")).toEqual({ sonnet: "qwen3.8-plus", light: "qwen3.8-flash" });
    expect(suggestTiers(["deepseek-v4-pro", "deepseek-v4-flash"], "deepseek-v4-pro")).toEqual({ light: "deepseek-v4-flash" });
    expect(suggestTiers(["MiniMax-M3", "MiniMax-M3.1-Flash"], "MiniMax-M3")).toEqual({ light: "MiniMax-M3.1-Flash" });
    expect(suggestTiers(["kimi-k3.1-pro", "kimi-k3"], "kimi-k3")).toEqual({ opus: "kimi-k3.1-pro" });
    expect(suggestTiers(["kimi-k3.1-pro", "kimi-k3"], "kimi-k3.1-pro")).toEqual({ sonnet: "kimi-k3" });
    expect(suggestTiers(["mimo-v2.6-pro", "mimo-v2.6-flash", "mimo-v2.6-pro-ultraspeed"], "mimo-v2.6-flash")).toEqual({ opus: "mimo-v2.6-pro" });
    expect(suggestTiers(["example-air", "example-mini", "example-lite", "example-turbo", "example-small", "example-nano", "example"], "example")).toHaveProperty("light");
    // parameter sizes: the largest is Opus, the smallest Haiku / small model
    expect(suggestTiers(["qwen3.8-27b", "qwen3.8-235b"], "qwen3.8-27b")).toEqual({ opus: "qwen3.8-235b" });
    expect(suggestTiers(["qwen3.8-27b", "qwen3.8-235b"], "qwen3.8-235b")).toEqual({ light: "qwen3.8-27b" });
  });

  it("leaves a tier empty when nothing says which model fits it", () => {
    // generations of one plain model: newer is not bigger
    expect(suggestTiers(["glm-5.1", "glm-5.3"], "glm-5.3")).toEqual({});
    // another family's light model is not the default's
    expect(suggestTiers(["glm-5.3", "deepseek-v4-flash"], "glm-5.3")).toEqual({});
    expect(suggestTiers([OPUS, "deepseek-v4-flash"], OPUS)).toEqual({});
    expect(suggestTiers([OPUS], OPUS)).toEqual({});
    expect(suggestTiers([], "")).toEqual({});
  });

  it("still takes an ID that names a Claude tier when the default is another family's", () => {
    expect(suggestTiers(["glm-5.3", "glm-5.3-flash", HAIKU], "glm-5.3")).toEqual({ light: HAIKU });
  });
});

describe("withAutoTiers", () => {
  it("fills a new provider's tiers, marked Auto, and the save carries them", () => {
    const f = withAutoTiers(gateway([OPUS, SONNET, HAIKU]));
    expect(f).toMatchObject({ opus_model: "", sonnet_model: SONNET, small_model: HAIKU, haiku_model: "", tier_source: { sonnet: "auto", light: "auto" } });
    const body = providerBody(f, null);
    expect(body).toMatchObject({ model: OPUS, sonnet_model: SONNET, small_model: HAIKU });
    expect(body).not.toHaveProperty("opus_model");
    expect(body).not.toHaveProperty("haiku_model");
  });

  it("follows the checked models and the default while a tier is still automatic", () => {
    let f = withAutoTiers(gateway([OPUS, SONNET, HAIKU]));
    f = withAutoTiers({ ...f, model: SONNET });
    expect(f).toMatchObject({ opus_model: OPUS, sonnet_model: "", small_model: HAIKU, tier_source: { opus: "auto", light: "auto" } });
    expect(f.tier_source).not.toHaveProperty("sonnet");
    f = withAutoTiers(uncheck(f, HAIKU));
    expect(f.small_model).toBe("");
    expect(f.tier_source).not.toHaveProperty("light");
  });

  it("never changes a tier the user chose, an empty one included", () => {
    let f = withAutoTiers(gateway([OPUS, SONNET, HAIKU]));
    f = setTier(f, "sonnet", "");
    f = setTier(f, "light", SONNET);
    f = withAutoTiers({ ...f, models: [...f.models, formModel({ id: "claude-haiku-6" })] });
    f = withAutoTiers({ ...f, model: SONNET });
    expect(f).toMatchObject({ sonnet_model: "", small_model: SONNET, opus_model: OPUS, tier_source: { sonnet: "user", light: "user", opus: "auto" } });
    expect(providerBody(f, null)).not.toHaveProperty("sonnet_model");
  });

  it("keeps a value typed where it is not tracked, and fills nothing for other kinds", () => {
    const typed = withAutoTiers(gateway([OPUS, SONNET, HAIKU], OPUS, { small_model: "my-small" }));
    expect(typed.small_model).toBe("my-small");
    const filled = withAutoTiers(gateway([OPUS, SONNET, HAIKU]));
    const codex = withAutoTiers({ ...filled, agent: "codex" });
    expect(codex).toMatchObject({ sonnet_model: "", small_model: "", tier_source: {} });
    const api = withAutoTiers({ ...filled, kind: "api" });
    expect(api).toMatchObject({ sonnet_model: "", small_model: "" });
  });

  it("leaves a saved provider as it is, even with empty tiers", () => {
    const saved = formFromView({ name: "gw", base_url: "https://gw.example.com", has_key: true, model: OPUS, models: [{ id: OPUS }, { id: SONNET }, { id: HAIKU }] });
    expect(withAutoTiers(saved)).toBe(saved);
    expect(withAutoTiers({ ...saved, model: SONNET }).opus_model).toBe("");
    const f = withAutoTiers(gateway([OPUS, SONNET]));
    expect(withAutoTiers(f)).toBe(f);
  });
});

describe("tierLines", () => {
  it("says what each line runs, the default model when empty", () => {
    const f = withAutoTiers(gateway([OPUS, SONNET, HAIKU]));
    expect(tierLines(f)).toEqual([
      { row: "opus", model: OPUS, follows: "session", auto: false },
      { row: "sonnet", model: SONNET, follows: null, auto: true },
      { row: "light", model: HAIKU, follows: null, auto: true },
      { row: "subagent", model: OPUS, follows: "session", auto: false },
    ]);
  });

  it("shows the Haiku tier and the small model apart when a saved provider has them apart", () => {
    const saved = formFromView({ name: "gw", model: OPUS, haiku_model: HAIKU, small_model: "", models: [{ id: OPUS }, { id: HAIKU }] });
    expect(tierLines(saved).map((l) => [l.row, l.model])).toEqual([["opus", OPUS], ["sonnet", OPUS], ["haiku", HAIKU], ["small", OPUS], ["subagent", OPUS]]);
    // the same model in both: one line again
    expect(tierLines({ ...saved, small_model: HAIKU }).map((l) => l.row)).toEqual(["opus", "sonnet", "light", "subagent"]);
    // setting the joint line moves the Haiku tier with it
    const joined = setTier({ ...saved, small_model: HAIKU }, "light", SONNET);
    expect(joined).toMatchObject({ small_model: SONNET, haiku_model: "" });
    // kept apart as the step opened: an emptied Haiku tier follows the small model, and says so
    const emptied = { ...saved, haiku_model: "", small_model: SONNET };
    expect(tierLines(emptied, true).find((l) => l.row === "haiku")).toEqual({ row: "haiku", model: SONNET, follows: "small", auto: false });
    expect(tierLines({ ...emptied, small_model: "" }, true).find((l) => l.row === "haiku")).toEqual({ row: "haiku", model: OPUS, follows: "session", auto: false });
  });
});

describe("tierAdvice", () => {
  it("says nothing while a new provider's tiers fill themselves in", () => {
    expect(tierAdvice(withAutoTiers(gateway([OPUS, SONNET, HAIKU])), false)).toBeNull();
    expect(tierAdvice(withAutoTiers(gateway([OPUS])), false)).toBeNull();
  });

  it("offers the lighter model when the Haiku tier lands on the Opus one", () => {
    const f = setTier(withAutoTiers(gateway([OPUS, SONNET, HAIKU])), "light", "");
    expect(tierAdvice(f, false)).toEqual({ sameAsOpus: OPUS, haiku: true, small: true, tiers: { light: HAIKU } });
    const fixed = applyTiers(f, { light: HAIKU });
    expect(fixed).toMatchObject({ small_model: HAIKU, haiku_model: "", tier_source: { light: "user" } });
    expect(tierAdvice(fixed, false)).toBeNull();
    // no lighter model checked: nothing to offer
    expect(tierAdvice(setTier(withAutoTiers(gateway([OPUS, SONNET])), "light", ""), false)).toBeNull();
  });

  it("offers a saved provider's empty tiers what the checked models suggest, and changes nothing until applied", () => {
    const view = { name: "gw", base_url: "https://gw.example.com", has_key: true, model: OPUS, models: [{ id: OPUS }, { id: SONNET }, { id: HAIKU }] };
    const saved = formFromView(view);
    expect(tierAdvice(saved, true)).toEqual({ sameAsOpus: OPUS, haiku: true, small: true, tiers: { sonnet: SONNET, light: HAIKU } });
    const put = providerBody(saved, view);
    for (const k of ["opus_model", "sonnet_model", "haiku_model", "small_model"]) expect(put[k] ?? "").toBe("");
    const applied = applyTiers(saved, { sonnet: SONNET, light: HAIKU });
    expect(tierAdvice(applied, true)).toBeNull();
    expect(providerBody(applied, view)).toMatchObject({ sonnet_model: SONNET, small_model: HAIKU });
    // a saved Sonnet tier is the user's: only the empty ones are offered
    expect(tierAdvice(formFromView({ ...view, sonnet_model: OPUS, small_model: HAIKU }), true)).toBeNull();
  });

  it("joins the Haiku tier and the small model when the Haiku tier sat on the Opus model", () => {
    const saved = formFromView({ name: "gw", model: OPUS, haiku_model: OPUS, small_model: HAIKU, sonnet_model: SONNET, models: [{ id: OPUS }, { id: SONNET }, { id: HAIKU }] });
    const advice = tierAdvice(saved, true);
    expect(advice).toEqual({ sameAsOpus: OPUS, haiku: true, small: false, tiers: { light: HAIKU } });
    const fixed = applyTiers(saved, advice!.tiers);
    expect(fixed).toMatchObject({ haiku_model: "", small_model: HAIKU });
    expect(tierAdvice(fixed, true)).toBeNull();
  });

  it("keeps a Haiku tier the user set apart, and says only the background requests sit on Opus", () => {
    const saved = formFromView({ name: "gw", model: OPUS, haiku_model: "claude-haiku-4-5", small_model: "", models: [{ id: OPUS }, { id: SONNET }, { id: HAIKU }] });
    const advice = tierAdvice(saved, true);
    expect(advice).toEqual({ sameAsOpus: OPUS, haiku: false, small: true, tiers: { sonnet: SONNET, light: HAIKU } });
    const fixed = applyTiers(saved, advice!.tiers);
    expect(fixed).toMatchObject({ haiku_model: "claude-haiku-4-5", small_model: HAIKU, sonnet_model: SONNET });
    expect(tierAdvice(fixed, true)).toBeNull();
  });

  it("is only for Claude Code providers", () => {
    const f = setTier(withAutoTiers(gateway([OPUS, SONNET, HAIKU])), "light", "");
    expect(tierAdvice({ ...f, agent: "codex" }, false)).toBeNull();
    expect(tierAdvice({ ...f, kind: "api" }, false)).toBeNull();
  });
});
