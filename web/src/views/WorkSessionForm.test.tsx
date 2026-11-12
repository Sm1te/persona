// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { Machine } from "../lib/types";

const state = vi.hoisted(() => ({ machines: [] as Machine[], launch: vi.fn() }));
vi.mock("../lib/current-assistant", () => ({ useScopedMachines: () => state.machines }));
vi.mock("../lib/store", () => ({ getState: () => state, hubHas: () => false }));
vi.mock("../lib/new-agent", () => ({ launchAgent: state.launch }));
vi.mock("./machines/AddComputerEntry", () => ({ useAddComputer: () => ({ dialog: null, open: () => {} }) }));
vi.mock("react-dom", async (original) => ({ ...(await original<typeof import("react-dom")>()), createPortal: (children: React.ReactNode) => children }));

import { WorkSessionForm } from "./WorkSessionForm";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
const machines = [{ id: "a", name: "A", online: true }, { id: "b", name: "B", online: true }];
const LAUNCHERS = [{ name: "claude-team", agent_type: "claude" }, { name: "codex-team", agent_type: "codex" }];
let launcher: { name: string; display_name?: string; agent_type: string }[] = LAUNCHERS;
const available = (id: string) => ({ agent_type: "claude", model_mode: "selectable", models: [{ id, name: id }], attachments: true });
const mockResponse = (v: unknown) => new Response(JSON.stringify(v));
let host: HTMLDivElement, root: Root;
let pending: { url: string; resolve: (value: unknown) => void }[];
const draw = () => act(async () => root.render(<WorkSessionForm open onClose={() => {}} />));
const fields = () => [...host.querySelectorAll("select")];
const choose = (i: number, value: string) => act(async () => {
  fields()[i].value = value;
  fields()[i].dispatchEvent(new Event("change", { bubbles: true }));
});
const reply = (i: number, data: unknown) => act(async () => pending[i].resolve(data));
const start = () => [...host.querySelectorAll("button")].find((b) => b.textContent === "启动")!;
const draft = () => host.querySelector("textarea")!;
const fill = (value: string) => act(async () => {
  Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(draft(), value);
  draft().dispatchEvent(new Event("input", { bubbles: true }));
});

beforeEach(async () => {
  state.machines = machines;
  launcher = LAUNCHERS;
  state.launch.mockClear();
  pending = [];
  vi.stubGlobal("matchMedia", () => ({ matches: false, addEventListener() {}, removeEventListener() {} }));
  vi.stubGlobal("fetch", vi.fn(async (resource: RequestInfo | URL) => {
    const url = new URL(String(resource), "https://example.test");
    const mid = url.pathname.split("/")[4];
    if (url.pathname.endsWith("/dirs")) {
      return mockResponse({ path: url.searchParams.get("path") || "/projects", entries: [{ name: mid, path: `/projects/${mid}`, is_git: true }] });
    }
    if (url.pathname.endsWith("/launchers")) return mockResponse({ launchers: launcher });
    // Ignore abort here to simulate a response arriving after the selection changed.
    return new Promise<Response>((done) => pending.push({ url: url.pathname, resolve: (v) => done(mockResponse(v)) }));
  }));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  await draw();
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
});

it("discards late device and launcher options while keeping the first-message draft", async () => {
  await choose(0, "a");
  await choose(1, "/projects/a");
  await choose(2, "claude");
  await fill("draft");
  expect(pending[0].url).toContain("/a/launchers/claude-team/");
  await choose(0, "b");
  expect(fields()[1].value).toBe("");
  expect(fields()[2].value).toBe("");
  expect(fields()[4].value).toBe("");
  expect(draft().value).toBe("draft");
  await choose(1, "/projects/b");
  await choose(2, "claude");
  await choose(2, "codex");
  expect(pending[2].url).toContain("/b/launchers/codex-team/");
  await reply(2, { agent_type: "codex", model_mode: "fixed", fixed_model: "fixed-id", prompt_required: true, inline_prompt: true });
  await reply(1, available("wrong-harness"));
  await reply(0, available("wrong-device"));
  expect(fields()[4].textContent).toContain("固定：fixed-id");
  expect(fields()[4].textContent).not.toMatch(/wrong-/);
  expect(fields()[4].disabled).toBe(true);
  await act(async () => start().click());
  expect(state.launch).toHaveBeenCalledWith({ machineId: "b", cwd: "/projects/b", launcher: "codex-team", agentType: "codex", workSession: true, model: undefined, inlinePrompt: true, text: "draft", attachments: [] });
});

it("cannot submit offline or before reconnect validation, and does not substitute a removed model", async () => {
  await choose(0, "a");
  await choose(1, "/projects/a");
  await choose(2, "claude");
  await reply(0, available("model-old"));
  await choose(4, "model-old");
  await fill("draft kept");
  expect(start().disabled).toBe(false);
  state.machines = [{ ...machines[0], online: false }, machines[1]];
  await draw();
  expect(start().disabled).toBe(true);
  state.machines = machines;
  await draw();
  expect(start().disabled).toBe(true);
  expect(draft().value).toBe("draft kept");
  await reply(1, available("model-new"));
  expect(start().disabled).toBe(true);
  await choose(4, "model-new");
  await act(async () => start().click());
  expect(state.launch).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ model: "model-new", text: "draft kept", launcher: "claude-team" }));
});

it("names a provider by its display name and ID, an rc alias by its ID (#5029)", async () => {
  launcher = [{ name: "relay-example-com", display_name: "我的中转 站", agent_type: "claude" }, { name: "cc_glm", agent_type: "claude" }];
  await choose(0, "a");
  await choose(1, "/projects/a");
  await choose(2, "claude");
  const options = [...fields()[3].options].slice(1);
  expect(options.map((o) => [o.value, o.textContent])).toEqual([["relay-example-com", "我的中转 站 (relay-example-com)"], ["cc_glm", "cc_glm"]]);
});
