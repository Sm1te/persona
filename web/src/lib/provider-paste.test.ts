import { describe, expect, it } from "vitest";
import { t } from "../i18n";
import {
  answered,
  defaultAction,
  draftForm,
  draftModels,
  editDraft,
  foundAliases,
  headerLines,
  newPaste,
  noteText,
  pasteActions,
  pasteChoice,
  withEditDraft,
  type ListedAlias,
  type PasteDraft,
  type PasteResult,
} from "./provider-paste";
import { formFromView, formModel } from "./providers";
import { withAutoTiers } from "./provider-tiers";

const draft = (over: Partial<PasteDraft> = {}, provider: Partial<PasteDraft["provider"]> = {}): PasteDraft => ({
  label: "kimi",
  provider: { kind: "launcher", agent: "claude", api: "messages", base_url: "https://gw.example.com", auth: "bearer", api_key: "sk-example-1", ...provider },
  ...over,
});
const result = (drafts: PasteDraft[], notes: PasteResult["notes"] = []): PasteResult => ({ format: "shell", drafts, notes });

describe("draftForm", () => {
  it("fills in the connection, the models checked with the default first, numbers as text and the args", () => {
    const f = draftForm(draft({}, {
      name: "kimi-2", display_name: "Example AI", model: "example-large", models: [{ id: "example-small", context_window: 128000 }],
      timeout_ms: 600000, compact_window: 100000, max_context: 200000, args: ["--verbose", "--debug"], proxy: "http://127.0.0.1:7890", effort: "high",
    }));
    expect(f).toMatchObject({
      kind: "launcher", agent: "claude", api: "messages", name: "kimi-2", display_name: "Example AI", base_url: "https://gw.example.com",
      auth: "bearer", api_key: "sk-example-1", model: "example-large", timeout_ms: "600000", compact_window: "100000", max_context: "200000",
      args: "--verbose --debug", proxy: "http://127.0.0.1:7890", effort: "high",
    });
    expect(f.models.map((m) => [m.id, m.checked, m.context_window])).toEqual([["example-large", true, ""], ["example-small", true, "128000"]]);
  });

  it("takes the first model as the default when none is named, and Bearer when no sign-in is", () => {
    const f = draftForm(draft({}, { auth: undefined, api_key: undefined, models: [{ id: "a" }, { id: "b" }] }));
    expect(f).toMatchObject({ model: "a", auth: "bearer", api_key: "" });
  });

  it("keeps the tiers it sets as the user's, so only the others fill in by themselves", () => {
    const f = draftForm(draft({}, { model: "glm-5", models: [{ id: "glm-5" }, { id: "glm-5-air" }, { id: "glm-5-pro" }], opus_model: "glm-5" }));
    expect(f.tier_source).toEqual({ opus: "user" });
    const filled = withAutoTiers(f);
    expect(filled.opus_model).toBe("glm-5");
    expect(filled.small_model).toBe("glm-5-air");
    expect(filled.tier_source).toMatchObject({ opus: "user", light: "auto" });
    // a Haiku tier set keeps the light one from filling in
    expect(draftForm(draft({}, { haiku_model: "glm-5-air" })).tier_source).toEqual({ light: "user" });
  });

  it("sends the headers and environment it brings, and only those", () => {
    expect(draftForm(draft({}, { headers: "X-Team: a", env: "DISABLE_TELEMETRY=1" }))).toMatchObject({ headers: "X-Team: a", headers_touched: true, env: "DISABLE_TELEMETRY=1", env_touched: true });
    expect(draftForm(draft())).toMatchObject({ headers: "", headers_touched: false, env: "", env_touched: false });
  });

  it("reads a direct API draft as one", () => {
    expect(draftForm(draft({}, { kind: "api", api: "chat", agent: undefined }))).toMatchObject({ kind: "api", api: "chat", agent: "claude" });
  });
});

describe("drafts on a stored provider", () => {
  it("lists the default model among the models", () => {
    expect(draftModels(draft({}, { model: "m1", models: [{ id: "m2" }] })).map((m) => m.id)).toEqual(["m1", "m2"]);
    expect(draftModels(draft({}, { model: "m2", models: [{ id: "m2" }] })).map((m) => m.id)).toEqual(["m2"]);
  });

  it("merges by adding the models the provider lacks, checked; replacing puts the key in", () => {
    const d = draft({}, { model: "m1", models: [{ id: "m2", context_window: 64000 }, { id: "m3" }] });
    expect(editDraft(d, "merge")).toEqual({ step: "models", models: [{ id: "m1" }, { id: "m2", context_window: 64000 }, { id: "m3" }] });
    expect(editDraft(d, "key")).toEqual({ step: "connect", api_key: "sk-example-1" });
    const stored = formFromView({ name: "gw", model: "m1", models: [{ id: "m1" }, { id: "m3" }] });
    const merged = withEditDraft(stored, editDraft(d, "merge"));
    expect(merged.models.map((m) => [m.id, m.checked])).toEqual([["m1", true], ["m3", true], ["m2", true]]);
    expect(merged.models[2]).toEqual(formModel({ id: "m2", context_window: 64000 }));
    expect(merged.model).toBe("m1");
    expect(withEditDraft(stored, editDraft(d, "key"))).toEqual({ ...stored, api_key: "sk-example-1" });
  });

  it("merges into a provider with the same key by default, else adds a new one", () => {
    expect(defaultAction(draft())).toEqual({ type: "new" });
    expect(defaultAction(draft({ matches: [{ name: "a", same_key: false }, { name: "b", same_key: true }] }))).toEqual({ type: "merge", provider: "b" });
  });

  it("offers adding the models where the key is the same, replacing the key where it is not, then a new provider", () => {
    const d = draft({ matches: [{ name: "same", same_key: true }, { name: "other", display_name: "Other", same_key: false }] });
    expect(pasteActions(d).map((a) => [a.type, a.type === "new" ? "" : a.provider])).toEqual([["merge", "same"], ["key", "other"], ["new", ""]]);
    // no key pasted: nothing to put in, so it can only add its models
    const keyless = draft({ matches: [{ name: "other", same_key: false }] }, { api_key: undefined });
    expect(pasteActions(keyless).map((a) => a.type)).toEqual(["merge", "new"]);
  });
});

describe("the paste state", () => {
  it("chooses a single draft by itself and waits for a choice among several", () => {
    const one = answered({ ...newPaste(), text: "x" }, "x", result([draft({ matches: [{ name: "gw", same_key: true }] })]));
    expect(one).toMatchObject({ draft: 0, action: { type: "merge", provider: "gw" } });
    expect(pasteChoice(one)?.action).toEqual({ type: "merge", provider: "gw" });
    const two = answered({ ...newPaste(), text: "x" }, "x", result([draft(), draft({ label: "b" })]));
    expect(two).toMatchObject({ draft: -1, action: null });
    expect(pasteChoice(two)).toBeNull();
  });

  it("drops an answer for an older text, and offers nothing while the text differs from the one read", () => {
    const s = { ...newPaste(), text: "new" };
    expect(answered(s, "old", result([draft()]))).toBe(s);
    const read = answered({ ...newPaste(), text: "old" }, "old", result([draft()]));
    expect(pasteChoice(read)).not.toBeNull();
    expect(pasteChoice({ ...read, text: "old and more" })).toBeNull();
  });

  it("reads a missing list as empty", () => {
    const s = answered({ ...newPaste(), text: "x" }, "x", { format: "" } as PasteResult);
    expect(s.result?.answer).toEqual({ format: "", drafts: [], notes: [] });
  });
});

describe("headerLines", () => {
  it("says the headers it sends and leaves out, by reason", () => {
    const lines = headerLines(draft({
      skipped_headers: [{ name: "X-Org-Token", reason: "credential" }, { name: "anthropic-version", reason: "protocol" }, { name: "anthropic-beta", reason: "protocol" }, { name: "X-New", reason: "later" }],
    }, { headers: "X-Team: a\nX-Trace: on\nnot a header" }));
    expect(lines).toEqual([
      t("lib-provider-paste.headers_kept", { names: "X-Team、X-Trace", count: 2 }),
      t("lib-provider-paste.headers_left_credential", { names: "X-Org-Token", count: 1 }),
      t("lib-provider-paste.headers_left_protocol", { names: "anthropic-version、anthropic-beta", count: 2 }),
      t("lib-provider-paste.headers_left_other", { names: "X-New", count: 1 }),
    ]);
    expect(headerLines(draft())).toEqual([]);
  });
});

describe("noteText", () => {
  it("says every code in words, naming the alias or the line", () => {
    expect(noteText({ code: "unrecognized" })).toBe(t("lib-provider-paste.unrecognized"));
    expect(noteText({ code: "invalid_json" })).toBe(t("lib-provider-paste.invalid_json"));
    expect(noteText({ code: "line_unused", line: 3 })).toBe(t("lib-provider-paste.line_unused", { line: 3 }));
    expect(noteText({ code: "not_claude", name: "ll" })).toBe(t("lib-provider-paste.not_claude_alias", { name: "ll" }));
    expect(noteText({ code: "not_claude", line: 2 })).toBe(t("lib-provider-paste.not_claude_line", { line: 2 }));
    expect(noteText({ code: "no_gateway", name: "cc" })).toBe(t("lib-provider-paste.no_gateway_alias", { name: "cc" }));
    expect(noteText({ code: "no_gateway" })).toBe(t("lib-provider-paste.no_gateway"));
    expect(noteText({ code: "url_shell", line: 4 })).toBe(t("lib-provider-paste.url_shell_line", { line: 4 }));
    expect(noteText({ code: "url_user", name: "cc" })).toBe(t("lib-provider-paste.url_user_alias", { name: "cc" }));
    expect(noteText({ code: "url_user" })).toBe(t("lib-provider-paste.url_user"));
    expect(noteText({ code: "key_shell" })).toBe(t("lib-provider-paste.key_shell"));
    expect(noteText({ code: "no_key" })).toBe(t("lib-provider-paste.no_key"));
    expect(noteText({ code: "key_helper" })).toBe(t("lib-provider-paste.key_helper"));
    expect(noteText({ code: "curl_option", name: "-u" })).toBe(t("lib-provider-paste.curl_option", { name: "-u" }));
    expect(noteText({ code: "curl_path", name: "/api/run" })).toBe(t("lib-provider-paste.curl_path", { name: "/api/run" }));
    expect(noteText({ code: "cindy_kind", name: "preset" })).toBe(t("lib-provider-paste.cindy_kind"));
    expect(noteText({ code: "cindy_auth", name: "oauth" })).toBe(t("lib-provider-paste.cindy_auth_oauth"));
    expect(noteText({ code: "cindy_auth", name: "none" })).toBe(t("lib-provider-paste.cindy_auth_none"));
    expect(noteText({ code: "cindy_endpoint", name: "google-generative-ai" })).toBe(t("lib-provider-paste.cindy_endpoint", { name: "google-generative-ai" }));
    expect(noteText({ code: "too_many" })).toBe(t("lib-provider-paste.drafts_limit", { max: 50 }));
    // never the code itself
    for (const code of ["unrecognized", "no_key", "too_many"]) expect(noteText({ code })).not.toContain(code);
  });

  it("reads a code it does not know as the generic note", () => {
    expect(noteText({ code: "from_a_newer_hub" })).toBe(t("lib-provider-paste.other"));
    expect(noteText({ code: "from_a_newer_hub", line: 7 })).toBe(t("lib-provider-paste.other_line", { line: 7 }));
    // a code that needs a name it lacks
    expect(noteText({ code: "curl_option" })).toBe(t("lib-provider-paste.other"));
  });
});

describe("foundAliases", () => {
  const machines = [{ id: "laptop", name: "Laptop", online: false }, { id: "desk", name: "Desk", online: true }];
  const alias = (name: string, more: Partial<ListedAlias> = {}): ListedAlias => ({ name, source: "zshrc", base_url: "https://gw.example.com", machines: ["laptop"], ...more });

  it("lists shown rc-file aliases with an address that are not imported yet", () => {
    const list: ListedAlias[] = [
      alias("cc_gw"),
      alias("cc_hidden", { hidden: true }),
      alias("cc_plain", { base_url: undefined }),
      alias("cc_wrapper", { source: "path" }),
      alias("cc_done"),
      alias("cc_elsewhere"),
      alias("cc_nowhere", { machines: [] }),
      { name: "gw", source: "persona", origin: "zshrc:cc_done@laptop" },
      // imported from another computer: not this alias on its computers
      { name: "gw2", source: "persona", origin: "zshrc:cc_elsewhere@desk" },
    ];
    expect(foundAliases(list, machines).map((a) => a.name)).toEqual(["cc_gw", "cc_elsewhere"]);
  });

  it("reads it on its first online computer, else its first, and says whether that one is online", () => {
    expect(foundAliases([alias("a", { machines: ["laptop", "desk"] })], machines)[0]).toEqual({ name: "a", base_url: "https://gw.example.com", machine: "desk", computer: "Desk", online: true });
    expect(foundAliases([alias("a", { machines: ["laptop"] })], machines)[0]).toMatchObject({ machine: "laptop", computer: "Laptop", online: false });
    expect(foundAliases([alias("a", { machines: ["gone"] })], machines)[0]).toMatchObject({ machine: "gone", computer: "gone", online: false });
  });
});
