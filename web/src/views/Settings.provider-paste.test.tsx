// @vitest-environment jsdom
// Settings › Model providers wires #5193 together: the wizard gets the rc-file aliases found in the list, picking one
// closes it and opens the import for that alias on its computer, and a paste that goes on with a stored provider swaps
// the wizard to editing it with what the paste brought (dropped again when the wizard closes).
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { EditDraft, FoundAlias } from "../lib/provider-paste";

vi.hoisted(() => {
  window.matchMedia = ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
const MACHINES = [{ id: "laptop", name: "Laptop", online: true }];
vi.mock("../lib/store", async (original) => ({
  ...(await original<typeof import("../lib/store")>()),
  useStore: (select: (state: { features: string[]; machines: unknown[]; conn: string; link: { networkUp: boolean; restarting: boolean }; hubName: string; mesh: unknown[] }) => unknown) => select({ features: [], machines: MACHINES, conn: "online", link: { networkUp: true, restarting: false }, hubName: "", mesh: [] }),
}));
type WizardProps = { existing: unknown; draft?: EditDraft | null; found?: FoundAlias[]; onClose: () => void; onImportAlias?: (a: FoundAlias) => void; onEditWith?: (name: string, d: EditDraft) => void };
type ImportProps = { open: boolean; machine?: string; alias?: string };
const seen = vi.hoisted(() => ({ wizard: null as unknown, import: null as unknown }));
vi.mock("./settings/ModelConfiguration", () => ({ ModelConfiguration: () => null }));
vi.mock("./settings/ProviderWizard", () => ({ ProviderWizard: (p: unknown) => ((seen.wizard = p), null) }));
vi.mock("./settings/ProviderImport", () => ({ ProviderImport: (p: unknown) => ((seen.import = p), null) }));
vi.mock("../lib/pwa", async (original) => ({ ...(await original<typeof import("../lib/pwa")>()), useInstall: () => "installed" }));
const GW = { name: "gw_full", display_name: "Team gateway", source: "persona", base_url: "https://gw.example.com" };
const IMPORTED = { name: "relay", source: "persona", base_url: "https://relay.example.org", origin: "zshrc:cc_relay@laptop" };
const LIST = vi.hoisted(() => ({ launchers: [] as unknown[] }));
vi.mock("../lib/api", async (original) => ({
  ...(await original<typeof import("../lib/api")>()),
  api: (path: string) => Promise.resolve(path === "/launchers" ? { launchers: LIST.launchers } : {}),
}));
const toasts = vi.hoisted(() => [] as string[]);
vi.mock("../ui/atoms", async (original) => ({ ...(await original<typeof import("../ui/atoms")>()), toast: (text: string) => void toasts.push(text) }));

import { t } from "../i18n";
import { Settings } from "./Settings";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let host: HTMLDivElement;
let root: Root;
const flush = async () => { for (let i = 0; i < 6; i++) await act(async () => { await Promise.resolve(); }); };
const wizard = () => seen.wizard as WizardProps;
const importer = () => seen.import as ImportProps;
const DRAFT: EditDraft = { step: "models", models: [{ id: "example-5-air" }] };

beforeEach(async () => {
  toasts.length = 0;
  LIST.launchers = [
    GW,
    IMPORTED,
    { name: "cc_team", source: "zshrc", base_url: "https://gw.example.com", machines: ["laptop"] },
    { name: "cc_relay", source: "zshrc", base_url: "https://relay.example.org", machines: ["laptop"] },
    { name: "cc_plain", source: "zshrc", machines: ["laptop"] },
  ];
  history.replaceState(null, "", "/settings?tab=models");
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  await act(async () => root.render(<Settings onToggleSidebar={() => {}} />));
  await flush();
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  document.body.innerHTML = "";
});

describe("model providers: paste and found aliases", () => {
  it("gives the wizard the aliases not imported yet, and opens the import for a picked one", async () => {
    expect(wizard().found).toEqual([{ name: "cc_team", base_url: "https://gw.example.com", machine: "laptop", computer: "Laptop", online: true }]);
    act(() => wizard().onImportAlias!(wizard().found![0]!));
    await flush();
    expect(wizard().existing).toBeNull();
    expect(importer()).toMatchObject({ open: true, machine: "laptop", alias: "cc_team" });
  });

  it("swaps the wizard to the stored provider with the paste's draft, and drops it on closing", async () => {
    act(() => wizard().onEditWith!("gw_full", DRAFT));
    await flush();
    expect(wizard().existing).toMatchObject({ name: "gw_full" });
    expect(wizard().draft).toEqual(DRAFT);
    act(() => wizard().onClose());
    await flush();
    expect(wizard()).toMatchObject({ existing: null, draft: null });
  });

  it("finds a provider added since the list loaded, and says so when it is gone", async () => {
    LIST.launchers = [...LIST.launchers, { name: "gw_new", source: "persona", base_url: "https://new.example.com" }];
    await act(async () => wizard().onEditWith!("gw_new", DRAFT));
    await flush();
    expect(wizard().existing).toMatchObject({ name: "gw_new" });
    act(() => wizard().onClose());
    await act(async () => wizard().onEditWith!("gw_gone", DRAFT));
    await flush();
    expect(wizard().existing).toBeNull();
    expect(toasts).toEqual([t("settings.provider_missing", { name: "gw_gone" })]);
  });
});
