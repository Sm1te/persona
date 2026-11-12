// @vitest-environment jsdom
// The import dialog of Settings › Model providers (#5024) through its real HTTP client: the defaults it opens with (one
// provider per alias when the aliases differ, hiding on), the body it posts, the reasons an alias stays, and the
// computer-less and failing cases.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((media: string) => ({ matches: false, media, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
vi.mock("../../ui/atoms", async (original) => ({ ...await original<typeof import("../../ui/atoms")>(), toast: vi.fn() }));
import { t } from "../../i18n";
import type { ImportedProvider } from "../../lib/providers";
import { getState } from "../../lib/store";
import type { Machine } from "../../lib/types";
import { toast } from "../../ui/atoms";
import { ProviderImport } from "./ProviderImport";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

type Call = { method: string; path: string; query: string; body?: Record<string, unknown> };
const MACHINES: Machine[] = [{ id: "laptop", name: "Laptop", online: true }, { id: "desk", name: "Desk", online: false }, { id: "old", name: "Old", online: true }];
const src = (alias: string, hide: "ok" | "in_use" | "elsewhere" | "none", more: Record<string, unknown> = {}) => ({ alias, file: "~/.zshrc", hide, ...more });

/** A group whose aliases set more than the model differently (the choice), and one from ~/.bashrc with variables. */
const PREVIEW: ImportedProvider[] = [
  {
    name: "cc_team", base_url: "https://gw.example.com", auth: "bearer", has_key: true, model: "team-large",
    models: [{ id: "team-large" }, { id: "team-small" }], aliases: ["cc_team", "cc_team_small"], differs: ["tiers", "effort"],
    sources: [src("cc_team", "ok"), src("cc_team_small", "in_use", { uses: ["Main chat"] })],
    variants: [
      { name: "cc_team", create_as: "cc_team-2", base_url: "https://gw.example.com", model: "team-large", models: [{ id: "team-large" }], aliases: ["cc_team"], env_names: ["DISABLE_TELEMETRY"], sources: [src("cc_team", "ok")] },
      { name: "cc_team_small", base_url: "https://gw.example.com", model: "team-small", models: [{ id: "team-small" }], aliases: ["cc_team_small"], existing: "team_gw",
        sources: [src("cc_team_small", "in_use", { uses: ["Main chat"] })] },
    ],
  },
  {
    name: "cc_relay", create_as: "cc_relay-2", base_url: "https://relay.example.com", auth: "bearer", has_key: true, model: "relay-pro", models: [{ id: "relay-pro" }],
    aliases: ["cc_relay"], env_names: ["CLAUDE_CODE_MAX_OUTPUT_TOKENS", "DISABLE_TELEMETRY"],
    skipped_env: [{ name: "RELAY_TOKEN", reason: "credential" }, { name: "WORKDIR", reason: "shell" }],
    sources: [{ alias: "cc_relay", file: "~/.bashrc", hide: "elsewhere", elsewhere: ["Desk"] }],
  },
];

let calls: Call[];
let preview: (machine: string) => Response;
let answer: () => Response;
let host: HTMLDivElement;
let root: Root;
let imported: () => void;
let closed: () => void;

const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });
const flush = async () => { for (let i = 0; i < 6; i++) await act(async () => { await Promise.resolve(); }); };
const text = () => document.body.textContent ?? "";
const q = <T extends Element = HTMLElement>(selector: string) => {
  const el = document.body.querySelector<T>(selector);
  expect(el, selector).not.toBeNull();
  return el!;
};
const click = async (el: Element) => { act(() => (el as HTMLElement).click()); await flush(); };
const importButton = () => [...document.body.querySelectorAll("button")].find((b) => b.textContent?.startsWith(t("settings-providerimport.import")))!;
const posted = () => calls.find((c) => c.method === "POST")!.body!;
const radio = (group: string, split: boolean) => q(`[role="radiogroup"][aria-label="${t("settings-providerimport.choice_label", { name: group })}"] [data-split="${split}"]`);

async function draw(machines: Machine[] = MACHINES) {
  Object.assign(getState(), { machines });
  act(() => root.render(<ProviderImport open onClose={closed} onImported={imported} />));
  await flush();
}

beforeEach(() => {
  calls = [];
  imported = vi.fn();
  closed = vi.fn();
  vi.mocked(toast).mockClear();
  preview = (machine) => machine === "old" ? json({ error: "too old", code: "node_outdated" }, 409)
    : machine === "desk" ? json({ error: "offline", code: "offline" }, 503)
    : json({ machine, providers: PREVIEW });
  answer = () => json({ created: ["cc_team", "cc_relay"], updated: ["team_gw"], hidden: ["cc_team"], kept: [{ alias: "cc_team_small", hide: "in_use", uses: ["Main chat"] }, { alias: "cc_relay", hide: "elsewhere", elsewhere: ["Desk"] }] });
  vi.stubGlobal("fetch", vi.fn((url: RequestInfo | URL, init?: RequestInit) => {
    const [path, query = ""] = String(url).split("?");
    const call = { method: init?.method ?? "GET", path: path!, query, body: init?.body ? JSON.parse(String(init.body)) : undefined };
    calls.push(call);
    if (call.path.endsWith("/launchers/import")) {
      return Promise.resolve(call.method === "GET" ? preview(new URLSearchParams(query).get("machine") ?? "") : answer());
    }
    return Promise.resolve(json({}));
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

describe("provider import", () => {
  it("opens on the first online computer with one provider per alias where they differ, and hiding on", async () => {
    await draw();
    expect(calls[0]).toMatchObject({ method: "GET", path: "/api/v1/launchers/import", query: "machine=laptop" });
    expect(text()).toContain(t("settings-providerimport.title"));
    expect(q('[data-import="cc_team"]').getAttribute("aria-checked")).toBe("true");
    expect(q('[data-import="cc_relay"]').getAttribute("aria-checked")).toBe("true");
    expect(radio("cc_team", true).getAttribute("aria-checked")).toBe("true");
    expect(radio("cc_team", false).getAttribute("aria-checked")).toBe("false");
    // only models differing: no choice, one provider
    expect(document.body.querySelector(`[aria-label="${t("settings-providerimport.choice_label", { name: "cc_relay" })}"]`)).toBeNull();
    expect(q("[data-import-hide]").getAttribute("aria-checked")).toBe("true");
    // a split group counts each alias: 2 + 1
    expect(importButton().textContent).toBe(t("settings-providerimport.import_count", { count: 3 }));
    // what the split creates, each with its own variables
    const variants = q(`[aria-label="${t("settings-providerimport.variants_label", { name: "cc_team" })}"]`);
    expect(variants.textContent).toContain(t("lib-providers.merge_into", { name: "team_gw" }));
    expect(variants.textContent).toContain(t("lib-providers.brings", { names: "DISABLE_TELEMETRY" }));
    expect(q('[data-import="cc_team"]').textContent).toContain(t("lib-providers.split_count", { count: 2 }));
  });

  it("names what gets created by the name it will be saved under, and posts the names it was given", async () => {
    await draw();
    // an alias Persona already lists is saved as <alias>-2: the row of a provider made from it, and a split's variant
    const relay = q('[data-import="cc_relay"]');
    expect(relay.querySelector(".font-mono")!.textContent).toBe("cc_relay-2");
    const variants = [...q(`[aria-label="${t("settings-providerimport.variants_label", { name: "cc_team" })}"]`).querySelectorAll("li")];
    expect(variants.map((li) => li.querySelector(".font-mono")!.textContent)).toEqual(["cc_team-2", "cc_team_small"]);
    // a split group's row keeps the group's name
    expect(q('[data-import="cc_team"]').querySelector(".font-mono")!.textContent).toBe("cc_team");
    await click(importButton());
    expect(posted()).toMatchObject({ providers: ["cc_team", "cc_relay"], split: ["cc_team"] });
  });

  it("names the file, what a provider carries over and leaves out, and what differs", async () => {
    await draw();
    expect(q('[data-import="cc_relay"]').textContent).toContain(t("lib-providers.from_file", { aliases: "cc_relay", file: "~/.bashrc" }));
    const relay = q('[data-import-detail="cc_relay"]').textContent;
    expect(relay).toContain("CLAUDE_CODE_MAX_OUTPUT_TOKENS");
    expect(relay).toContain(t("lib-providers.left_out_credential", { names: "RELAY_TOKEN" }));
    expect(relay).toContain(t("lib-providers.left_out_shell", { names: "WORKDIR" }));
    expect(q('[data-import-detail="cc_team"]').textContent).toContain(t("lib-providers.differs", { what: "各档模型和默认思考档位" }));
  });

  it("posts the split groups and hide_aliases, then sums up created, merged, hidden and kept", async () => {
    await draw();
    await click(importButton());
    expect(posted()).toEqual({ machine: "laptop", providers: ["cc_team", "cc_relay"], split: ["cc_team"], hide_aliases: true });
    expect(toast).toHaveBeenCalledWith(t("lib-providers.import_summary_hide", { created: 2, updated: 1, hidden: 1, kept: 2 }), "ok");
    expect(imported).toHaveBeenCalledOnce();
    expect(closed).toHaveBeenCalledOnce();
  });

  it("imports a group as one provider and keeps the aliases when asked", async () => {
    await draw();
    await click(radio("cc_team", false));
    expect(radio("cc_team", false).getAttribute("aria-checked")).toBe("true");
    expect(document.body.querySelector(`[aria-label="${t("settings-providerimport.variants_label", { name: "cc_team" })}"]`)).toBeNull();
    expect(q('[data-import-detail="cc_team"]').textContent).toContain(t("settings-providerimport.merge_hint", { alias: "cc_team" }));
    expect(importButton().textContent).toBe(t("settings-providerimport.import_count", { count: 2 }));
    await click(q("[data-import-hide]"));
    expect(q("[data-import-hide]").getAttribute("aria-checked")).toBe("false");
    expect(document.body.querySelector("[data-import-stays]")).toBeNull();
    answer = () => json({ created: ["cc_team", "cc_relay"], updated: [], hidden: [], kept: [] });
    await click(importButton());
    expect(posted()).toEqual({ machine: "laptop", providers: ["cc_team", "cc_relay"], split: [], hide_aliases: false });
    expect(toast).toHaveBeenCalledWith(t("lib-providers.import_summary", { created: 2, updated: 0 }), "ok");
  });

  it("says merging only adds models only when the import will merge something", async () => {
    const note = () => text().includes(t("settings-providerimport.merge_note"));
    await draw();
    // split by default: cc_team_small's provider merges into team_gw
    expect(note()).toBe(true);
    // one provider: cc_team is created, so nothing merges
    await click(radio("cc_team", false));
    expect(note()).toBe(false);
    await click(radio("cc_team", true));
    expect(note()).toBe(true);
    // the group that would merge is not picked
    await click(q('[data-import="cc_team"]'));
    expect(note()).toBe(false);
  });

  it("moves between the two choices with the arrow keys", async () => {
    await draw();
    const group = q(`[role="radiogroup"][aria-label="${t("settings-providerimport.choice_label", { name: "cc_team" })}"]`);
    expect(radio("cc_team", true).tabIndex).toBe(0);
    expect(radio("cc_team", false).tabIndex).toBe(-1);
    act(() => group.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true })));
    await flush();
    expect(radio("cc_team", false).getAttribute("aria-checked")).toBe("true");
    expect(document.activeElement).toBe(radio("cc_team", false));
  });

  it("says which aliases are hidden and why the others stay", async () => {
    await draw();
    const stays = q("[data-import-stays]").textContent;
    expect(stays).toContain(t("settings-providerimport.hide_which", { aliases: "cc_team" }));
    expect(stays).toContain(t("lib-providers.stays_in_use", { alias: "cc_team_small", uses: "Main chat" }));
    expect(stays).toContain(t("lib-providers.stays_elsewhere", { alias: "cc_relay", computers: "Desk" }));
    // unpicking a group drops its aliases from the hide row; with none left that could change, the row goes away
    await click(q('[data-import="cc_relay"]'));
    expect(q("[data-import-stays]").textContent).not.toContain("cc_relay");
    await click(q('[data-import="cc_team"]'));
    expect(document.body.querySelector("[data-import-hide]")).toBeNull();
    expect(importButton().disabled).toBe(true);
  });

  it("leaves the hide row out and sends hide_aliases false when nothing could be hidden", async () => {
    preview = (machine) => json({ machine, providers: [{ ...PREVIEW[1], sources: [{ alias: "cc_relay", file: "~/.zshrc", hide: "none" }] }] });
    await draw();
    expect(document.body.querySelector("[data-import-hide]")).toBeNull();
    await click(importButton());
    expect(posted()).toEqual({ machine: "laptop", providers: ["cc_relay"], split: [], hide_aliases: false });
  });

  it("explains instead of showing a picker when there is no computer", async () => {
    await draw([]);
    expect(text()).toContain(t("settings-providerimport.no_computers"));
    expect(document.body.querySelector("select")).toBeNull();
    expect(calls).toEqual([]);
    expect(importButton().disabled).toBe(true);
  });

  it("keeps the outdated and offline computers' messages", async () => {
    await draw();
    const select = q<HTMLSelectElement>("select");
    act(() => {
      select.value = "old";
      select.dispatchEvent(new Event("change", { bubbles: true }));
    });
    await flush();
    expect(q('[role="alert"]').textContent).toBe(t("lib-providers.node_outdated"));
    act(() => {
      select.value = "desk";
      select.dispatchEvent(new Event("change", { bubbles: true }));
    });
    await flush();
    expect(q('[role="alert"]').textContent).toBe(t("lib-providers.offline"));
    expect(importButton().disabled).toBe(true);
  });
});
