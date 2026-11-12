// @vitest-environment jsdom
// On a hub that folds child sessions (#5120) the paged Agents list leaves them out of every read it makes: the root's
// pages and counts, a search's own list, and the scoped counts its chips show.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { t } from "../../i18n";
import { api } from "../../lib/api";
import { SessionList, type ListCounts } from "../../lib/session-list";
import { STATE_LABEL } from "../../lib/state-labels";
import { SESSION_LIST_METRICS } from "../../ui/list-metrics";
import { PagedSessionList } from "./PagedSessionList";

vi.mock("../../lib/api", async (original) => ({ ...await original<typeof import("../../lib/api")>(), api: vi.fn() }));
vi.mock("../../lib/store", () => ({ onHubEvent: () => () => {} }));
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

type Row = { id: string };
const identity = (row: Row) => row.id;
const compare = (a: Row, b: Row) => a.id.localeCompare(b.id);
const counts: ListCounts = { total: 3, state: { needs_you: 1, working: 2 }, machine: { m: 3 }, project: {}, group: {}, revision: "r" };

let host: HTMLDivElement;
let root: Root;
const reads = () => vi.mocked(api).mock.calls.map(([path]) => new URL(path, "https://example.com").searchParams);

beforeEach(() => {
  vi.useFakeTimers();
  vi.mocked(api).mockReset().mockImplementation((path) => {
    const params = new URL(path, "https://example.com").searchParams;
    return Promise.resolve(params.has("counts") ? counts : { sessions: [{ id: "lead" }], remove: [], has_more: false, total: 1, revision: "r" }) as ReturnType<typeof api>;
  });
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.useRealTimers();
});

async function show(list: SessionList<Row>) {
  await list.load();
  await act(async () => root.render(<PagedSessionList root={list} path="/sessions" identity={identity} compare={compare} event="sessions_list" renderRow={(row) => <span>{row.id}</span>} />));
}

async function search() {
  const input = host.querySelector<HTMLInputElement>(`input[aria-label="${t("paged-agents.search")}"]`)!;
  act(() => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, "tests");
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await act(async () => { await vi.advanceTimersByTimeAsync(SESSION_LIST_METRICS.searchDelay); });
}

describe("the paged list on a folding hub (#5120)", () => {
  it("sends roots=1 with every read, the search's and the chips' counts too", async () => {
    await show(new SessionList<Row>("/sessions", identity, compare, undefined, undefined, { roots: true }));
    await search();
    const working = [...host.querySelectorAll<HTMLButtonElement>(`[aria-label="${t("sidebar.quick_filters")}"] [role="radio"]`)]
      .find((chip) => chip.getAttribute("aria-label")?.startsWith(STATE_LABEL.working))!;
    await act(async () => { working.click(); await vi.advanceTimersByTimeAsync(0); });
    const all = reads();
    expect(all.some((p) => p.get("q") === "tests" && p.has("counts"))).toBe(true);
    expect(all.some((p) => p.get("q") === "tests" && p.get("state") === "working" && !p.has("counts"))).toBe(true);
    expect(all.every((p) => p.get("roots") === "1")).toBe(true);
  });

  it("sends nothing new to a hub that does not fold", async () => {
    await show(new SessionList<Row>("/sessions", identity, compare));
    await search();
    expect(reads().some((p) => p.get("q") === "tests")).toBe(true);
    expect(reads().every((p) => !p.has("roots"))).toBe(true);
  });
});
