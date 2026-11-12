// @vitest-environment jsdom
// Secret references on the web (docs/extensions/secret-providers.md): a secret that holds a provider's reference says
// whose and what it names, and its dialog saves a reference where the hub takes one ("Use a reference"); a provider's
// page lists the settings that hold its references and its Always rules for hand-offs, each removed after a confirm.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { HubExtensionDetail } from "../../lib/extensions";

const hub = vi.hoisted(() => ({ detail: null as unknown }));
vi.mock("../../lib/extensions", async (original) => ({
  ...(await original<typeof import("../../lib/extensions")>()),
  extensionDetail: vi.fn(() => Promise.resolve(hub.detail)),
  setExtensionSecret: vi.fn(() => Promise.resolve(hub.detail)),
  setExtensionSecretReference: vi.fn(() => Promise.resolve(hub.detail)),
  removeHandoffRule: vi.fn(() => Promise.resolve({ ...(hub.detail as HubExtensionDetail), handoff_rules: [] })),
}));
vi.mock("../../ui/atoms", async (original) => ({ ...(await original<typeof import("../../ui/atoms")>()), toast: vi.fn() }));
import { removeHandoffRule, setExtensionSecret, setExtensionSecretReference } from "../../lib/extensions";
import { getState } from "../../lib/store";
import { Extensions } from "./Extensions";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 6; i++) await act(async () => { await Promise.resolve(); });
};
const typeInto = (input: HTMLInputElement, value: string) => {
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, value);
  input.dispatchEvent(new Event("input", { bubbles: true }));
};
const button = (label: string) => [...document.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent?.trim() === label);

const notes = (): HubExtensionDetail => ({
  id: "example-notes", name: "Example notes", kind: "process", status: "running", source: "installed", enabled: true,
  secrets: [{ name: "api_key", title: "API key", set: true,
    reference: { provider: "onepassword", provider_name: "1Password", reference: "op://Dev/Notes/credential", title: "Notes › credential" } }],
});
const vault = (): HubExtensionDetail => ({
  id: "onepassword", name: "1Password", kind: "process", status: "running", source: "installed", enabled: true,
  secret_users: [
    { kind: "extension", extension: "example-notes", extension_name: "Example notes", secret: "api_key", secret_title: "API key", reference: "op://Dev/Notes/credential" },
    { kind: "launcher", launcher: "gateway", launcher_name: "Gateway", machine: "box", reference: "op://Dev/Gateway/key" },
  ],
  handoff_rules: [{ id: "0123456789abcdef", reference: "op://Dev/GitHub/credential", launcher: "claude", machine: "box", by: "device:d1", at: "2026-11-12T08:00:00Z" }],
});

beforeEach(() => {
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  Object.assign(getState(), { features: ["extension_secret_references"], machines: [{ id: "box", name: "MacBook" }] });
  vi.mocked(setExtensionSecret).mockClear();
  vi.mocked(setExtensionSecretReference).mockClear();
  vi.mocked(removeHandoffRule).mockClear();
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

const open = async (id: string) => {
  act(() => root.render(<Extensions open={id} onOpen={() => {}} />));
  await flush();
};

describe("a secret that holds a reference", () => {
  it("says whose and what it names, and its dialog saves a reference", async () => {
    hub.detail = notes();
    await open("example-notes");
    expect(host.textContent).toContain("来自 1Password");
    expect(host.textContent).toContain("Notes › credential");
    const row = [...host.querySelectorAll<HTMLElement>("button, [role=button]")].find((b) => b.textContent?.includes("API key"));
    await act(async () => row!.click());
    const input = document.querySelector<HTMLInputElement>('input[aria-label="API key"]')!;
    expect(input.type).toBe("text");
    expect(input.value).toBe("op://Dev/Notes/credential");
    act(() => typeInto(input, "op://Dev/Notes/new"));
    await act(async () => button("保存")!.click());
    await flush();
    expect(setExtensionSecretReference).toHaveBeenCalledWith("example-notes", "api_key", "op://Dev/Notes/new");
    expect(setExtensionSecret).not.toHaveBeenCalled();
  });

  it("offers no reference where the hub takes none", async () => {
    Object.assign(getState(), { features: [] });
    hub.detail = { ...notes(), secrets: [{ name: "api_key", title: "API key", set: false }] };
    await open("example-notes");
    const row = [...host.querySelectorAll<HTMLElement>("button, [role=button]")].find((b) => b.textContent?.includes("API key"));
    await act(async () => row!.click());
    expect(document.body.textContent).not.toContain("使用引用");
    expect(document.querySelector<HTMLInputElement>('input[aria-label="API key"]')!.type).toBe("password");
  });
});

describe("a secret provider's page", () => {
  it("lists who uses its references and its Always rules, and removes a rule after a confirm", async () => {
    hub.detail = vault();
    await open("onepassword");
    const users = host.querySelector("[data-secret-users]")!;
    expect(users.textContent).toContain("Example notes › API key");
    expect(users.textContent).toContain("MacBook 上「Gateway」的 Key");
    const rules = host.querySelector("[data-handoff-rules]")!;
    expect(rules.textContent).toContain("MacBook 上的「claude」");
    expect(rules.textContent).toContain("op://Dev/GitHub/credential");
    await act(async () => [...rules.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent === "移除")!.click());
    expect(removeHandoffRule).not.toHaveBeenCalled(); // asked first
    expect(document.body.textContent).toContain("移除这条规则？");
    const confirm = [...document.querySelectorAll<HTMLButtonElement>("[role=dialog] button, dialog button")].find((b) => b.textContent === "移除");
    await act(async () => confirm!.click());
    await flush();
    expect(removeHandoffRule).toHaveBeenCalledWith("onepassword", "0123456789abcdef");
    expect(host.querySelector("[data-handoff-rules]")).toBeNull();
  });
});
