// @vitest-environment jsdom
// Machine detail's Launchers (#5029): a Persona-managed provider by its display name with its startup ID beside it in small
// mono; an rc alias without one by its ID in mono, as before.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Machine } from "../../lib/types";

vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
  const kept = new Map<string, string>();
  g.localStorage = { getItem: (k: string) => kept.get(k) ?? null, setItem: (k: string, v: string) => void kept.set(k, v), removeItem: (k: string) => void kept.delete(k) };
});

const fake = vi.hoisted(() => ({ machines: [] as Machine[] }));
vi.mock("../../lib/store", async (importOriginal) => {
  const real = await importOriginal<typeof import("../../lib/store")>();
  return { ...real, useStore: <T,>(select: (s: ReturnType<typeof real.getState>) => T) => select({ ...real.getState(), loaded: true, machines: fake.machines }) };
});

const { MachineDetail } = await import("./MachineDetail");

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 5; i++) await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
};
const json = (v: unknown, status = 200) => new Response(JSON.stringify(v), { status, headers: { "Content-Type": "application/json" } });
const LAUNCHERS = [
  { name: "relay-example-com", display_name: "我的中转 站", model: "glm-5" },
  { name: "cc_glm", agent: "claude" },
];

beforeEach(() => {
  fake.machines = [{ id: "mac", name: "Mac", online: true, os: "darwin" } as Machine];
  vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL) => Promise.resolve(
    String(input).endsWith("/machines/mac/launchers") ? json({ launchers: LAUNCHERS }) : json({ error: "not found" }, 404),
  )));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
});

describe("MachineDetail launchers", () => {
  it("names a provider by its display name with its ID beside it, an rc alias by its ID", async () => {
    act(() => root.render(<MachineDetail mid="mac" onToggleSidebar={() => {}} />));
    await flush();
    const row = (key: string) => [...host.querySelectorAll<HTMLElement>(".divide-y > div")].find((r) => r.textContent?.includes(key))!;
    const [title, id, model] = [...row("我的中转 站").querySelectorAll("span")].filter((s) => s.textContent);
    expect([title.textContent, title.className.includes("font-mono")]).toEqual(["我的中转 站", false]);
    expect([id.textContent, id.className.includes("font-mono"), id.className.includes("text-ink-3")]).toEqual(["relay-example-com", true, true]);
    expect(model.textContent).toBe("glm-5");
    const alias = [...row("cc_glm").querySelectorAll("span")].filter((s) => s.textContent);
    expect(alias.map((s) => [s.textContent, s.className.includes("font-mono")])).toEqual([["cc_glm", true]]);
  });
});
