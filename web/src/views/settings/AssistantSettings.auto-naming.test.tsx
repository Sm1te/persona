// @vitest-environment jsdom
import { act, StrictMode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
vi.hoisted(() => { window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {} })) as typeof window.matchMedia; });
vi.mock("../../ui/Markdown", () => ({ Markdown: () => null }));
vi.mock("../../lib/managers", () => ({
  useRoster: () => ({ default: "assistant", managers: [{ id: "assistant", name: "Personal" }, { id: "work", name: "Work" }] }),
  managerOrder: (r: { managers: unknown[] }) => r.managers,
}));
vi.mock("../assistant/Persona", () => ({ usePersona: () => ({ name: "Persona" }), PersonaAvatar: () => null }));
vi.mock("./Skills", () => ({ SkillsGroup: () => null }));
vi.mock("./StandingAuthorizations", () => ({ StandingAuthorizations: () => null }));
vi.mock("../../lib/store", () => ({ useStore: () => [], refreshAssistant: vi.fn() }));
import { initI18n, t } from "../../i18n";
import { chooseManager, MANAGER_HEADER, resetManagerChoice, setRosterDefault } from "../../lib/manager-choice";
import { setToken } from "../../lib/api";
import { AssistantSettings } from "./AssistantSettings";
import type { AgentAutoNamingState } from "./AgentAutoNaming";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let root: Root;
let host: HTMLDivElement;
let profiles: Record<string, AgentAutoNamingState>;
let writes: { manager: string; token: string | null; body: Record<string, unknown> }[];
let reply: ((manager: string, body: Record<string, unknown>) => Promise<Response>) | undefined;
const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status });
const flush = async () => { for (let i = 0; i < 8; i++) await act(async () => { await Promise.resolve(); }); };
const key = (name: string) => t(`settings-assistantsettings.auto_naming${name ? `_${name}` : ""}`);
const control = <T extends HTMLElement>(name: string) => host.querySelector<T>(`[aria-label="${key(name)}"]`)!;
async function click(name: string) { await act(async () => control<HTMLButtonElement>(name).click()); await flush(); }
function input(name: string, value: string) {
  const node = control<HTMLInputElement>(name);
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(node, value);
  node.dispatchEvent(new Event("input", { bubbles: true }));
}
async function commit(name: string, value: string) {
  act(() => input(name, value));
  act(() => control<HTMLInputElement>(name).dispatchEvent(new FocusEvent("focusout", { bubbles: true })));
  await flush();
}
async function render() { await act(async () => root.render(<StrictMode><AssistantSettings bare /></StrictMode>)); await flush(); }
beforeEach(async () => {
  await initI18n("en"); history.replaceState(null, "", "/settings?tab=assistant");
  resetManagerChoice(); setRosterDefault("assistant"); setToken("fixture-token-a");
  profiles = {
    assistant: { manager: "assistant", agent_auto_naming: true, agent_auto_naming_max: 1, agent_auto_naming_interval_hours: 0 },
    work: { manager: "work", agent_auto_naming: true, agent_auto_naming_max: 5, agent_auto_naming_interval_hours: 48 },
  };
  writes = []; reply = undefined;
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input).split("?")[0];
    const headers = new Headers(init?.headers);
    const manager = headers.get(MANAGER_HEADER)!;
    expect(["assistant", "work"]).toContain(manager);
    if (path.endsWith("/assistant")) {
      if (init?.method === "PUT") {
        const body = JSON.parse(String(init.body)); writes.push({ manager, token: headers.get("Authorization"), body });
        if (reply) return reply(manager, body);
        profiles[manager] = { ...profiles[manager], ...body };
      }
      return json(profiles[manager]);
    }
    if (path.endsWith("/tasks")) return json({ tasks: [] });
    if (path.endsWith("/memory")) return json({ files: [] });
    throw new Error(`Unexpected path: ${path}`);
  }));
});
afterEach(() => { act(() => root.unmount()); host.remove(); vi.unstubAllGlobals(); });

it("shows one correction and no periodic updates by default, without an extra read", async () => {
  await render();
  expect(control("").getAttribute("aria-checked")).toBe("true");
  expect(control<HTMLInputElement>("max").value).toBe("1");
  expect(control("periodic").getAttribute("aria-checked")).toBe("false");
  expect(control("interval")).toBeNull();
  expect(host.textContent).toContain("Names you set yourself are never replaced");
  expect(writes).toEqual([]);
  // StrictMode rehearses the existing page read twice; the child does not add one.
  expect(vi.mocked(fetch).mock.calls.filter(([path]) => String(path).endsWith("/assistant"))).toHaveLength(2);
});
it("disables correction and restores it without changing the other preferences", async () => {
  await render(); await click("");
  expect(control("").getAttribute("aria-checked")).toBe("false");
  expect(control("max")).toBeNull();
  await click("");
  expect(control<HTMLInputElement>("max").value).toBe("1");
  expect(writes.map((w) => w.body)).toEqual([{ agent_auto_naming: false }, { agent_auto_naming: true }]);
});
it("saves total changes and hours with the explicit owner, including disabling periodic updates", async () => {
  await render(); await click("periodic");
  expect(control<HTMLInputElement>("interval").value).toBe("24");
  expect(host.textContent).toContain(key("raise_max"));
  await commit("max", "1000"); await commit("interval", "8760");
  expect(host.textContent).not.toContain(key("raise_max"));
  await click("periodic");
  expect(writes).toEqual([
    { manager: "assistant", token: "Bearer fixture-token-a", body: { agent_auto_naming_interval_hours: 24 } },
    { manager: "assistant", token: "Bearer fixture-token-a", body: { agent_auto_naming_max: 1000 } },
    { manager: "assistant", token: "Bearer fixture-token-a", body: { agent_auto_naming_interval_hours: 8760 } },
    { manager: "assistant", token: "Bearer fixture-token-a", body: { agent_auto_naming_interval_hours: 0 } },
  ]);
});
it("rejects empty, fractional, negative and out-of-range values without writing", async () => {
  await render();
  for (const value of ["", "0", "-1", "1.5", "1001"]) { await commit("max", value); expect(control<HTMLInputElement>("max").value).toBe("1"); }
  await click("periodic"); writes = [];
  for (const value of ["", "0", "-1", "1.5", "8761"]) { await commit("interval", value); expect(control<HTMLInputElement>("interval").value).toBe("24"); }
  expect(writes).toEqual([]);
});
it("rolls failed saves back and permits an explicit retry", async () => {
  await render(); reply = async () => json({ error: "refused" }, 500);
  await commit("max", "7");
  expect(control<HTMLInputElement>("max").value).toBe("1");
  expect(host.querySelector('[role="alert"]')!.textContent).toBe(key("save_failed"));
  await click(""); expect(control("").getAttribute("aria-checked")).toBe("true");
  reply = undefined; await commit("max", "7");
  expect(control<HTMLInputElement>("max").value).toBe("7");
  expect(host.querySelector('[role="alert"]')).toBeNull();
});
it.each(["wrong-owner", "missing-fields", "wrong-echo"])("does not accept a successful status with %s", async (kind) => {
  await render();
  reply = async () => json(kind === "wrong-owner" ? { ...profiles.assistant, manager: "work", agent_auto_naming_max: 7 }
    : kind === "missing-fields" ? { manager: "assistant", agent_auto_naming_max: 7 } : profiles.assistant);
  await commit("max", "7");
  expect(control<HTMLInputElement>("max").value).toBe("1"); expect(host.querySelector('[role="alert"]')).not.toBeNull();
});
it("admits only one mixed change before React renders busy, and waits for the echo", async () => {
  await render(); let finish!: (response: Response) => void;
  reply = () => new Promise((resolve) => { finish = resolve; });
  act(() => { control<HTMLButtonElement>("").click(); control<HTMLButtonElement>("periodic").click(); }); await flush();
  expect(writes).toHaveLength(1);
  expect(control("").getAttribute("aria-checked")).toBe("true");
  await act(async () => finish(json({ ...profiles.assistant, agent_auto_naming: false }))); await flush();
  expect(control("").getAttribute("aria-checked")).toBe("false");
});
it("keeps assistants independent and fences a late save after switching", async () => {
  await render(); let finish!: (response: Response) => void;
  reply = () => new Promise((resolve) => { finish = resolve; }); await click("");
  await act(async () => chooseManager("work")); await flush();
  expect(control<HTMLInputElement>("max").value).toBe("5");
  await act(async () => finish(json({ ...profiles.assistant, agent_auto_naming: false }))); await flush();
  expect(control("").getAttribute("aria-checked")).toBe("true"); expect(host.querySelector('[role="alert"]')).toBeNull();
  reply = undefined; await commit("max", "6");
  expect(writes[1].manager).toBe("work"); expect(profiles.assistant.agent_auto_naming_max).toBe(1);
});
it("re-reads a replacement credential and ignores the retired credential's late failure", async () => {
  await render(); let finish!: (response: Response) => void;
  reply = () => new Promise((resolve) => { finish = resolve; }); await click("");
  profiles.assistant.agent_auto_naming_max = 9;
  await act(async () => setToken("fixture-token-b")); await flush();
  expect(control<HTMLInputElement>("max").value).toBe("9");
  await act(async () => finish(json({ error: "retired" }, 500))); await flush();
  expect(host.querySelector('[role="alert"]')).toBeNull();
  reply = undefined; await commit("max", "10");
  expect(writes[1].token).toBe("Bearer fixture-token-b");
});
it.each([{}, { agent_auto_naming: true }, { agent_auto_naming: true, agent_auto_naming_max: 1, agent_auto_naming_interval_hours: 0, manager: "work" }])("hides unsupported fields or disables a mismatched scope", async (info) => {
  profiles.assistant = info; await render();
  if (info.manager) expect(control<HTMLButtonElement>("").disabled).toBe(true);
  else expect(control("")).toBeNull();
  expect(writes).toEqual([]);
});
