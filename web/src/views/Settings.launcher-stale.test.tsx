// @vitest-environment jsdom
// The provider detail dialog stays mounted from one launcher to the next (#5374): an answer for the launcher it showed
// before, arriving last, used to replace the one it shows, so Delete went to the other launcher under this one's title.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = (q: string) => ({ matches: false, media: q, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} });
});

type Pending = { path: string; method: string; resolve: (v: unknown) => void };
const pending: Pending[] = [];

vi.mock("../lib/api", async (orig) => ({
  ...(await orig<typeof import("../lib/api")>()),
  api: vi.fn((path: string, init?: { method?: string }) => new Promise((resolve) => pending.push({ path, method: init?.method ?? "GET", resolve }))),
}));

const { LauncherDetailDialog } = await import("./Settings");

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const launcher = (name: string) => ({ launcher: { name, source: "persona", kind: "api", model: `model-${name}`, base_url: "https://api.example.com" } });
const answer = async (path: string, method: string, v: unknown) => {
  const i = pending.findIndex((p) => p.path === path && p.method === method);
  expect(i).toBeGreaterThanOrEqual(0);
  const [p] = pending.splice(i, 1);
  await act(async () => p.resolve(v));
};
const button = (text: string) => [...document.querySelectorAll("button")].find((b) => b.textContent?.includes(text))!;

let root: Root;
const show = (name: string | null) => act(() => root.render(<LauncherDetailDialog name={name} onClose={() => {}} onChanged={() => {}} onEdit={() => {}} />));

beforeEach(() => {
  pending.length = 0;
  const host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  document.body.innerHTML = "";
});

describe("the provider detail dialog", () => {
  it("keeps the launcher it shows when the one before answers last, and deletes that one", async () => {
    show("a");
    show(null);
    show("b");
    await answer("/launchers/b", "GET", launcher("b"));
    await answer("/launchers/a", "GET", launcher("a")); // the slow one, last
    expect(document.body.textContent).toContain("model-b");
    expect(document.body.textContent).not.toContain("model-a");

    act(() => button("删除供应商").click());
    act(() => button("确认删除").click());
    expect(pending.map((p) => `${p.method} ${p.path}`)).toContain("DELETE /launchers/b");
    expect(pending.map((p) => `${p.method} ${p.path}`)).not.toContain("DELETE /launchers/a");
  });

  it("drops a test result that comes after switching to another launcher", async () => {
    show("a");
    await answer("/launchers/a", "GET", launcher("a"));
    act(() => button("测试").click());
    show("b");
    await answer("/launchers/b", "GET", launcher("b"));
    await answer("/launchers/a/check", "POST", { check: { ok: false, millis: 5, error: "check of a failed" } });
    expect(document.body.textContent).toContain("model-b");
    expect(document.body.textContent).not.toContain("check of a failed");
  });

  it("lists what still names an rc-file launcher when its hide is confirmed (#4661)", async () => {
    show("wrap");
    await answer("/launchers/wrap", "GET", { launcher: { name: "wrap", source: "zshrc", model: "" } });
    act(() => button("在 Persona 里隐藏").click());
    await answer("/launchers/wrap/uses", "GET", { uses: ["主对话", "档位 review"] });
    expect(document.body.textContent).toContain("仍在使用它的有：主对话, 档位 review");
    expect(button("确认隐藏")).toBeTruthy();
  });
});
