// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
vi.hoisted(() => { window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {} })) as typeof window.matchMedia; });
vi.mock("../../ui/Markdown", () => ({ Markdown: () => null }));
vi.mock("../../lib/managers", () => ({
  useRoster: () => ({ default: "assistant", managers: [{ id: "assistant", name: "Personal" }] }),
  managerOrder: (r: { managers: unknown[] }) => r.managers,
}));
vi.mock("../assistant/Persona", () => ({ usePersona: () => ({ name: "Persona" }), PersonaAvatar: () => null }));
vi.mock("./Skills", () => ({ SkillsGroup: () => null }));
vi.mock("../../lib/store", () => ({ useStore: () => [], refreshAssistant: vi.fn() }));
vi.mock("../../ui/atoms", async (original) => ({ ...await original<typeof import("../../ui/atoms")>(), toast: vi.fn() }));
import { initI18n, t } from "../../i18n";
import { resetManagerChoice, setRosterDefault } from "../../lib/manager-choice";
import { toast } from "../../ui/atoms";
import { AssistantSettings } from "./AssistantSettings";

// Settings › Assistant › Agent follow-up: "The main chat supervises its own tasks" (#5082), the background
// supervisor's off switch, and the link to its model.

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let root: Root;
let host: HTMLDivElement;
/** The hub's assistant settings; a field left out is one an older hub does not have. */
let stored: Record<string, unknown>;
let writes: Record<string, unknown>[];
/** What a PUT answers instead of saving, when set. */
let answer: (() => Response) | undefined;
const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status });
const flush = async () => { for (let i = 0; i < 8; i++) await act(async () => { await Promise.resolve(); }); };
const row = () => host.querySelector<HTMLButtonElement>(`[role="switch"][aria-label="${t("settings-assistantsettings.main_supervises")}"]`);
const modelLink = () => [...host.querySelectorAll("a")].find((a) => a.textContent === t("settings-assistantsettings.background_supervisor_model"));
beforeEach(async () => {
  await initI18n("en");
  history.replaceState(null, "", "/settings?tab=persona");
  resetManagerChoice(); setRosterDefault("assistant");
  vi.mocked(toast).mockClear();
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  stored = { manager: "assistant", notes: "", agent_tasks: false, inbox_signals: false, main_supervises_own_tasks: false, background_supervisor: "bg" }; writes = []; answer = undefined;
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input).split("?")[0];
    if (path.endsWith("/assistant")) {
      if (init?.method === "PUT") {
        const body = JSON.parse(String(init.body)); writes.push(body);
        if (answer) return answer();
        stored = { ...stored, ...body };
      }
      return json(stored);
    }
    if (path.endsWith("/tasks")) return json({ tasks: [] });
    if (path.endsWith("/memory")) return json({ files: [] });
    throw new Error(`Unexpected path: ${path}`);
  }));
});
afterEach(async () => { act(() => root.unmount()); host.remove(); vi.unstubAllGlobals(); });
async function render() { await act(async () => root.render(<AssistantSettings />)); await flush(); }
async function click(node: HTMLElement) { await act(async () => { node.click(); }); await flush(); }

it("shows the hub's value, off by default, with the sentence saying what it does", async () => {
  await render();
  expect(row()!.getAttribute("aria-checked")).toBe("false");
  expect(host.textContent).toContain("Off by default: a built-in background supervisor follows up and reviews the tasks Persona hands out");
  expect(host.textContent).toContain("turn this on and the main chat does that itself");
  expect(modelLink()?.getAttribute("href")).toContain("assistant.supervise");
});

it("saves only its own field, both ways, and shows what the hub saved", async () => {
  await render();
  await click(row()!);
  expect(writes).toEqual([{ main_supervises_own_tasks: true }]);
  expect(row()!.getAttribute("aria-checked")).toBe("true");
  await click(row()!);
  expect(writes).toEqual([{ main_supervises_own_tasks: true }, { main_supervises_own_tasks: false }]);
  expect(row()!.getAttribute("aria-checked")).toBe("false");
  expect(toast).not.toHaveBeenCalled();
});

it("goes back with the hub's reason when the change is refused", async () => {
  answer = () => json({ error: "Synthetic refusal" }, 403);
  await render();
  await click(row()!);
  expect(toast).toHaveBeenCalledWith(expect.stringContaining("Synthetic refusal"), "error");
  expect(row()!.getAttribute("aria-checked")).toBe("false");
});

it("has neither the row nor the model link on a hub before it", async () => {
  delete stored.main_supervises_own_tasks;
  delete stored.background_supervisor;
  await render();
  expect(row()).toBeNull();
  expect(modelLink()).toBeUndefined();
  expect(host.textContent).toContain("Agent follow-up"); // the group's other rows stay
});
