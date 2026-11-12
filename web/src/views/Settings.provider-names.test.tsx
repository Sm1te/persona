// @vitest-environment jsdom
// Model providers by their display names (#5029): the provider list's search finds one by its display name; the detail
// dialog's title is the display name with the startup ID after it in small mono, and its toasts name it the same way; an
// rc alias without one keeps its ID. Deleting, hiding or restoring one forgets the names other pages read.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.hoisted(() => {
  window.matchMedia = ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
vi.mock("../lib/store", async (original) => ({
  ...(await original<typeof import("../lib/store")>()),
  useStore: (select: (state: { features: string[]; machines: unknown[]; conn: string; link: { networkUp: boolean; restarting: boolean }; hubName: string; mesh: unknown[] }) => unknown) => select({ features: [], machines: [], conn: "online", link: { networkUp: true, restarting: false }, hubName: "", mesh: [] }),
}));
vi.mock("./settings/ModelConfiguration", () => ({ ModelConfiguration: () => null }));
vi.mock("./settings/ProviderImport", () => ({ ProviderImport: () => null }));
vi.mock("./settings/ProviderWizard", () => ({ ProviderWizard: () => null }));
vi.mock("../lib/pwa", async (original) => ({ ...(await original<typeof import("../lib/pwa")>()), useInstall: () => "installed" }));
const RELAY = { name: "relay-example-com", display_name: "我的中转 站", source: "persona", kind: "anthropic", model: "glm-5", base_url: "https://relay.example.com" };
const ALIAS = { name: "cc_glm", source: "zshrc", machines: [] as string[] };
const calls = vi.hoisted(() => [] as string[]);
vi.mock("../lib/api", async (original) => ({
  ...(await original<typeof import("../lib/api")>()),
  api: (path: string, init?: { method?: string }) => {
    calls.push(`${init?.method ?? "GET"} ${path}`);
    if (path === "/launchers") return Promise.resolve({ launchers: [RELAY, ALIAS] });
    if (path === "/launchers/relay-example-com") return Promise.resolve({ launcher: RELAY });
    if (path === "/launchers/cc_glm") return Promise.resolve({ launcher: { ...ALIAS, hidden: true } });
    return Promise.resolve({});
  },
}));
const toasts = vi.hoisted(() => [] as string[]);
vi.mock("../ui/atoms", async (original) => ({ ...(await original<typeof import("../ui/atoms")>()), toast: (text: string) => void toasts.push(text) }));
const forgotten = vi.hoisted(() => ({ n: 0 }));
vi.mock("../lib/provider-names", async (original) => ({ ...(await original<typeof import("../lib/provider-names")>()), forgetProviderNames: () => void forgotten.n++ }));

import { LauncherDetailDialog, Settings } from "./Settings";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let host: HTMLDivElement;
let root: Root;
const flush = async () => { for (let i = 0; i < 6; i++) await act(async () => { await Promise.resolve(); }); };
const button = (text: string) => [...document.querySelectorAll("button")].find((b) => b.textContent?.includes(text))!;
const title = () => document.querySelector('[role="dialog"] h2')!;
const show = async (name: string) => {
  act(() => root.render(<LauncherDetailDialog name={name} onClose={() => {}} onChanged={() => {}} onEdit={() => {}} />));
  await flush();
};

beforeEach(() => {
  calls.length = 0;
  toasts.length = 0;
  forgotten.n = 0;
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  document.body.innerHTML = "";
});

describe("model providers by their display names", () => {
  it("finds a provider by its display name", async () => {
    history.replaceState(null, "", "/settings?tab=models");
    await act(async () => root.render(<Settings onToggleSidebar={() => {}} />));
    await flush();
    const search = document.querySelector<HTMLInputElement>('input[aria-label="搜索供应商"]')!;
    expect(host.textContent).toContain("cc_glm");
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(search, "中转");
      search.dispatchEvent(new Event("input", { bubbles: true }));
    });
    expect(host.textContent).toContain("我的中转 站");
    expect(host.textContent).not.toContain("cc_glm");
  });

  it("titles the dialog by the display name with the ID after it, names it in the toast, and forgets the names", async () => {
    await show("relay-example-com");
    expect(title().textContent).toBe("我的中转 站 relay-example-com");
    expect(title().querySelector(".font-mono")!.textContent).toBe("relay-example-com");
    act(() => button("删除供应商").click());
    act(() => button("确认删除").click());
    await flush();
    expect(calls).toContain("DELETE /launchers/relay-example-com");
    expect(toasts).toEqual(["已删除 我的中转 站"]);
    expect(forgotten.n).toBe(1);
  });

  it("keeps an rc alias's ID as the title, and forgets the names on restoring it", async () => {
    await show("cc_glm");
    expect(title().innerHTML).toBe('<span class="font-mono">cc_glm</span>');
    act(() => button("恢复").click());
    await flush();
    expect(toasts).toEqual(["已恢复 cc_glm"]);
    expect(forgotten.n).toBe(1);
  });
});
