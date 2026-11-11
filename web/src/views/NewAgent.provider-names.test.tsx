// @vitest-environment jsdom
// "New agent" names a model provider by its display name, its startup ID as secondary text (#5029): the launcher chips
// and the "Default: …" chip in their tooltips, the blocked list and its help beside the name, the presets' tooltip in
// "Title (id)", a recent setup's card in its tooltip. An ID this computer's list doesn't have is looked up in the model
// configuration's providers. An rc alias has no display name and reads as before (its ID, in mono).
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Launcher, Machine } from "../lib/types";
import { recentSetupsKey, type RecentSetup } from "../lib/recent-setups";

const state = vi.hoisted(() => ({ loaded: true, features: [] as string[], machines: [] as Machine[] }));
// one assistant: its computers are the store's (lib/current-assistant.ts reads the roster, which this store stand-in has none of)
vi.mock("../lib/current-assistant", async () => {
  const store = (await import("../lib/store")) as unknown as { useStore: <T>(select: (s: { machines: unknown[] }) => T) => T };
  return { useScopedMachines: () => store.useStore((s) => s.machines) };
});
vi.mock("../lib/store", () => ({
  getState: () => state,
  useStore: (selector: (value: typeof state) => unknown) => selector(state),
  hubHas: (features: string[], feature: string, unknown = false) => (features.length ? features.includes(feature) : unknown),
}));
vi.mock("react-dom", async (original) => ({ ...(await original<typeof import("react-dom")>()), createPortal: (children: React.ReactNode) => children }));

import { NewAgent } from "./NewAgent";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const LAUNCHERS: Launcher[] = [
  { name: "relay-example-com", display_name: "我的中转 站", agent: "claude", model: "glm-5" },
  { name: "cc_glm", agent: "claude" },
  { name: "team-relay", display_name: "Team relay", health: { state: "blocked", kind: "not_in_terminal", checked_at: "2026-11-09T08:00:00Z" } },
];
const PURPOSE = {
  purpose: { id: "agent.new", effective: { provider: "launcher.relay-example-com" } },
  providers: [
    { id: "launcher.relay-example-com", name: "我的中转 站", kind: "launcher" },
    { id: "launcher.elsewhere", name: "Elsewhere relay", kind: "launcher" },
  ],
};
const setup = (s: Partial<RecentSetup>): RecentSetup => ({ machine: "mac", cwd: "/code/app", launcher: "", model: "", agent_type: "claude", mode: "", effort: "", at: "2026-11-09T03:00:00Z", ...s });

let host: HTMLDivElement;
let root: Root;
const flush = () => act(async () => { for (let i = 0; i < 12; i++) await Promise.resolve(); });
const q = (sel: string) => document.body.querySelector<HTMLElement>(sel);
const chip = (text: string) => [...document.body.querySelectorAll<HTMLButtonElement>("button[aria-pressed]:not([data-recent-setup])")].find((b) => b.textContent?.trim() === text);
const radio = (text: string) => [...document.body.querySelectorAll<HTMLButtonElement>('[role="radio"]')].find((b) => b.textContent?.trim() === text)!;
const card = (text: string) => [...document.body.querySelectorAll<HTMLButtonElement>("[data-recent-setup]")].find((c) => c.textContent?.includes(text))!;

beforeEach(() => {
  state.machines = [{ id: "mac", name: "Mac", online: true }];
  localStorage.clear();
  localStorage.setItem("new.machine", "mac");
  localStorage.setItem("new.cwd.mac", "/code/app");
  localStorage.setItem(recentSetupsKey(), JSON.stringify([setup({ launcher: "elsewhere" }), setup({ cwd: "/code/web", launcher: "cc_glm" })]));
  vi.stubGlobal("matchMedia", () => ({ matches: false, addEventListener() {}, removeEventListener() {} }));
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.includes("/model-config/purposes/")) return json(PURPOSE);
    if (url.includes("/launchers/") && url.includes("/models")) return json({ models: [] });
    if (url.includes("/launchers")) {
      return json({
        launchers: LAUNCHERS,
        default: { provider: "relay-example-com", model: "glm-5" },
        tiers: [{ name: "quick", launcher: "relay-example-com", model: "glm-5" }, { name: "alias", launcher: "cc_glm" }],
        tiers_profile: { id: "p", name: "Daily" },
      });
    }
    return json({ path: "/code", entries: [] });
  }));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  act(() => root.render(<NewAgent open onClose={() => {}} />));
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
});

describe("New agent: provider display names (#5029)", () => {
  it("titles a launcher chip by its display name with the ID in its tooltip; an rc alias keeps its ID in mono", async () => {
    await flush();
    const relay = chip("我的中转 站")!;
    expect(relay.className).not.toContain("font-mono");
    expect(relay.title).toBe("relay-example-com\nglm-5");
    const alias = chip("cc_glm")!;
    expect(alias.className).toContain("font-mono");
    expect(alias.hasAttribute("title")).toBe(false);
    expect(chip("relay-example-com")).toBeUndefined();
  });

  it("titles the default's provider, its ID in the tooltip", async () => {
    await flush();
    const def = chip("默认：我的中转 站 · glm-5")!;
    expect(def.title.split("\n")[0]).toBe("relay-example-com");
  });

  it("names a blocked launcher by its title, its ID beside it in mono, in the list and its help", async () => {
    await flush();
    const row = q('[data-testid="new-agent-blocked"] li')!;
    const [title, id] = [...row.querySelectorAll("p > span")].map((s) => s as HTMLElement);
    expect([title.textContent, title.className.includes("font-mono")]).toEqual(["Team relay", false]);
    expect([id.textContent, id.className.includes("font-mono")]).toEqual(["team-relay", true]);
    const fix = row.querySelector("button")!;
    expect(fix.getAttribute("aria-label")).toBe("怎么修好 Team relay");
    act(() => fix.click());
    const help = q('[data-testid="launcher-blocked-help"]')!;
    expect(help.textContent).toContain("Team relay");
    expect(help.textContent).toContain("team-relay");
    expect(help.textContent).toContain("「Team relay」");
  });

  it("says a preset's launcher as \"Title (id)\", an rc alias's as its ID", async () => {
    await flush();
    expect(radio("quick").title).toBe("我的中转 站 (relay-example-com) · glm-5");
    expect(radio("alias").title).toBe("cc_glm");
    act(() => radio("quick").click());
    await flush();
    expect(q('[data-testid="new-agent-preset-hint"]')!.textContent).toBe("我的中转 站 (relay-example-com) · glm-5");
  });

  it("titles a recent setup's launcher from the model configuration when this computer doesn't have it", async () => {
    await flush();
    const away = card("Elsewhere relay");
    expect(away.title).toBe("/code/app\nelsewhere");
    const alias = card("cc_glm");
    expect(alias.title).toBe("/code/web");
    expect([...alias.querySelectorAll(".font-mono")].map((s) => s.textContent)).toEqual(["cc_glm"]);
  });
});
