// @vitest-environment jsdom
// Claude Code's ultracode switch on the web (#3428): the row in the model panel's flyout of the model that runs (#5032), the
// line above the composer after a resume, the workflow mark on the model pill and on agent rows; nothing of it on an older hub.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { getState, upsertSession } from "../../lib/store";
import type { Session } from "../../lib/types";
import { resetUltracodeAsks } from "../../lib/ultracode";

vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
});

// the panel draws what it is given for the current model's flyout (ui/model-panel has its own tests)
vi.mock("../../ui/model-panel/ModelPanel", () => ({ ModelPanel: ({ currentExtra }: { currentExtra?: React.ReactNode }) => <div data-testid="panel">{currentExtra}</div> }));

const { AgentModelPill } = await import("./ModelPill");
const { UltracodeLostNote, UltracodeStartRow, UltracodeSwitchRow } = await import("./Ultracode");
const { SessionRow } = await import("../Sidebar");

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const base: Session = { machine_id: "mac", session_id: "s1", title: "整理发布说明", cwd: "/code", status: "idle", state: "idle", agent: "claude", effort: "high", pane_id: "p1" } as Session;
const LOST = "2026-11-06T08:00:00Z";

type Call = { url: string; body: unknown };
let calls: Call[];
let answer: (url: string) => Response;
let host: HTMLDivElement;
let root: Root;
const flush = () => act(async () => { for (let i = 0; i < 4; i++) await Promise.resolve(); });
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

function hub(features: string[]) {
  Object.assign(getState(), { features, sessions: {} });
}

function render(node: React.ReactNode) {
  act(() => root.render(node));
}

beforeEach(() => {
  resetUltracodeAsks();
  hub(["ultracode"]);
  calls = [];
  answer = (url) => (url.includes("/usage/windows") ? json({ accounts: [] }) : json({ ok: true }));
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    calls.push({ url, body: init?.body ? JSON.parse(String(init.body)) : undefined });
    return answer(url);
  }));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
});

const modes = () => calls.filter((c) => c.url.endsWith("/modes"));
const theSwitch = () => document.body.querySelector<HTMLButtonElement>('[role="switch"]');

describe("the panel's ultracode row", () => {
  const open = async (s: Session) => {
    render(<AgentModelPill session={s} />);
    await flush();
    await act(async () => host.querySelector<HTMLButtonElement>("button[data-model-pill]")!.click());
    await act(async () => vi.dynamicImportSettled());
    await flush();
  };

  it("is under the model that runs on a hub that has it, and turning it on sends {ultracode: true} at once", async () => {
    const s = { ...base, ultracode: false };
    upsertSession(s);
    await open(s);
    const row = document.body.querySelector('[data-testid="ultracode-row"]')!;
    expect(row.textContent).toContain("ultracode");
    expect(row.textContent).toContain("每个任务都编排多 Agent workflow · 很费额度 · 只对本会话");
    expect(theSwitch()!.getAttribute("aria-checked")).toBe("false");
    await act(async () => theSwitch()!.click());
    await flush();
    expect(modes()).toEqual([{ url: expect.stringMatching(/\/sessions\/mac\/s1\/modes$/), body: { ultracode: true } }]);
    // it stands where it was put until the node's push says
    expect(theSwitch()!.getAttribute("aria-checked")).toBe("true");
  });

  it("is not there on an older hub", async () => {
    hub([]);
    await open(base);
    expect(document.body.querySelector('[data-testid="panel"]')).not.toBeNull();
    expect(document.body.querySelector('[data-testid="ultracode-row"]')).toBeNull();
    expect(theSwitch()).toBeNull();
  });
});

describe("the switch row", () => {
  it("the node's reason in place of the line, in orange; the switch stays usable and a click asks the node again", async () => {
    const note = "需要先在 Claude Code 设置中开启 dynamic workflows（工作流）";
    const s = { ...base, ultracode: false, ultracode_note: note };
    upsertSession(s);
    render(<UltracodeSwitchRow session={s} />);
    expect(theSwitch()!.disabled).toBe(false);
    expect(host.querySelector("[data-ultracode-line]")!.textContent).toBe(note);
    expect(host.querySelector("[data-ultracode-line]")!.className).toContain("text-orange");
    // turned on in Claude Code's settings since: trying again is one click
    await act(async () => theSwitch()!.click());
    await flush();
    expect(modes().map((c) => c.body)).toEqual([{ ultracode: true }]);
  });

  it("a failure that only repeats the node's reason is not said twice", async () => {
    const note = "请升级到 Claude Code 2.1.284 或更新版本，旧版不支持独立的 ultracode 开关";
    answer = (url) => (url.endsWith("/modes") ? json({ error: note }, 502) : json({ accounts: [] }));
    const s = { ...base, ultracode: false, ultracode_note: note };
    upsertSession(s);
    render(<UltracodeSwitchRow session={s} />);
    await act(async () => theSwitch()!.click());
    await flush();
    expect(host.querySelector('[role="alert"]')).toBeNull();
    expect(theSwitch()!.getAttribute("aria-checked")).toBe("false");
    expect(host.querySelector("[data-ultracode-line]")!.textContent).toBe(note);
  });

  it("says \"Applies next turn\" while its own request waits for the turn", async () => {
    const busy = { ...base, status: "working", state: "working" as const, ultracode: false };
    upsertSession(busy);
    render(<UltracodeSwitchRow session={busy} />);
    await act(async () => theSwitch()!.click());
    await flush();
    render(<UltracodeSwitchRow session={{ ...busy, ultracode_pending: true }} />);
    expect(host.querySelector("[data-ultracode-line]")!.textContent).toBe("下一轮生效");
    expect(theSwitch()!.getAttribute("aria-checked")).toBe("true");
    expect(theSwitch()!.disabled).toBe(false);
  });

  it("a change queued elsewhere says a change waits, not tied to where the switch stands", () => {
    render(<UltracodeSwitchRow session={{ ...base, status: "working", state: "working", ultracode: false, ultracode_pending: true }} />);
    expect(host.querySelector("[data-ultracode-line]")!.textContent).toBe("有一次切换在排队，这一轮结束后生效");
    expect(theSwitch()!.getAttribute("aria-checked")).toBe("false");
    expect(theSwitch()!.disabled).toBe(false);
  });

  it("an old node's refusal says the hub's words under the row, and the switch is not left gray", async () => {
    answer = (url) => (url.endsWith("/modes") ? json({ code: "node_outdated", error: "这台机器上的 personad 太旧，还不支持 ultracode，需要更新 node" }, 409) : json({ accounts: [] }));
    upsertSession(base);
    render(<UltracodeSwitchRow session={base} />);
    await act(async () => theSwitch()!.click());
    await flush();
    expect(host.querySelector('[role="alert"]')!.textContent).toBe("ultracode 没有切换成：这台机器上的 personad 太旧，还不支持 ultracode，需要更新 node");
    expect(theSwitch()!.getAttribute("aria-checked")).toBe("false");
    // the node updated meanwhile: the next click goes to the hub
    expect(theSwitch()!.disabled).toBe(false);
    answer = (url) => (url.endsWith("/modes") ? json({ ok: true }) : json({ accounts: [] }));
    await act(async () => theSwitch()!.click());
    await flush();
    expect(modes()).toHaveLength(2);
    expect(host.querySelector('[role="alert"]')).toBeNull();
  });

  it("another failure goes back and says why, and can be tried again", async () => {
    answer = (url) => (url.endsWith("/modes") ? json({ error: "未能确认 ultracode 状态，请在终端里确认" }, 502) : json({ accounts: [] }));
    upsertSession(base);
    render(<UltracodeSwitchRow session={base} />);
    await act(async () => theSwitch()!.click());
    await flush();
    expect(host.querySelector('[role="alert"]')!.textContent).toBe("ultracode 没有切换成：未能确认 ultracode 状态，请在终端里确认");
    expect(theSwitch()!.getAttribute("aria-checked")).toBe("false");
    expect(theSwitch()!.disabled).toBe(false);
  });

  it("the Claude quota under it, with a warning past 80%; nothing without data", async () => {
    const at = new Date(Date.now() + 3600_000).toISOString();
    answer = () => json({ accounts: [{ id: "c", provider: "claude", name: "Claude", sampled_at: new Date().toISOString(), local: true,
      windows: [{ label: "5h", window_minutes: 300, used_percent: 86, resets_at: at }, { label: "7d", window_minutes: 10080, used_percent: 41, resets_at: at }] }] });
    render(<UltracodeSwitchRow session={base} />);
    await flush();
    const quota = host.querySelector("[data-ultracode-quota]")!;
    expect(quota.textContent).toContain("Claude 额度：5 小时 86% · 每周 41%");
    expect(quota.textContent).toContain("额度快用完了");
    // under a reason it can't be turned on for, only the numbers: one orange line, the one that blocks it
    render(<UltracodeSwitchRow session={{ ...base, ultracode_note: "请升级到 Claude Code 2.1.284 或更新版本，旧版不支持独立的 ultracode 开关" }} />);
    await flush();
    expect(host.querySelector("[data-ultracode-quota]")!.textContent).toContain("5 小时 86%");
    expect(host.querySelector("[data-ultracode-quota-warn]")).toBeNull();
    act(() => root.unmount());
    root = createRoot(host);
    answer = () => json({ accounts: [] });
    render(<UltracodeSwitchRow session={base} />);
    await flush();
    expect(host.querySelector("[data-ultracode-quota]")).toBeNull();
  });
});

describe("after a resume turned it off", () => {
  const lost = { ...base, ultracode: false, ultracode_lost_at: LOST };

  it("one line with \"Turn back on\", which sends {ultracode: true}", async () => {
    upsertSession(lost);
    render(<UltracodeLostNote session={lost} />);
    const note = host.querySelector('[data-testid="ultracode-lost"]')!;
    expect(note.textContent).toContain("恢复后 ultracode 已关");
    // a narrow screen may cut it: the whole of it on hover
    expect(note.querySelector('[title="恢复后 ultracode 已关"]')).not.toBeNull();
    const reopen = host.querySelector<HTMLButtonElement>('[data-testid="ultracode-reopen"]')!;
    expect(reopen.className).toContain("whitespace-nowrap");
    await act(async () => reopen.click());
    await flush();
    expect(modes().map((c) => c.body)).toEqual([{ ultracode: true }]);
  });

  it("× dismisses it: {ultracode_dismiss_lost: true}", async () => {
    upsertSession(lost);
    render(<UltracodeLostNote session={lost} />);
    await act(async () => host.querySelector<HTMLButtonElement>('button[aria-label="关掉这条提示"]')!.click());
    await flush();
    expect(modes().map((c) => c.body)).toEqual([{ ultracode_dismiss_lost: true }]);
    expect(getState().sessions.mac?.[0]?.ultracode_lost_at).toBeUndefined();
  });

  it("while turning it back on waits for the turn, \"Applies next turn\" takes the button's place", async () => {
    const busy = { ...lost, status: "working", state: "working" as const };
    upsertSession(busy);
    render(<UltracodeLostNote session={busy} />);
    await act(async () => host.querySelector<HTMLButtonElement>('[data-testid="ultracode-reopen"]')!.click());
    await flush();
    render(<UltracodeLostNote session={{ ...busy, ultracode_pending: true }} />);
    expect(host.querySelector('[data-testid="ultracode-reopen"]')).toBeNull();
    expect(host.textContent).toContain("下一轮生效");
  });

  it("is not there on an older hub, in a Persona conversation, or once it is back on", () => {
    hub([]);
    render(<UltracodeLostNote session={lost} />);
    expect(host.innerHTML).toBe("");
    hub(["ultracode"]);
    render(<UltracodeLostNote session={{ ...lost, assistant: true }} />);
    expect(host.innerHTML).toBe("");
    render(<UltracodeLostNote session={{ ...lost, ultracode: true }} />);
    expect(host.innerHTML).toBe("");
  });
});

describe("New agent's switch", () => {
  it("grayed with why this computer can't, the quota's warning left out, the grayed track still drawn", async () => {
    const at = new Date(Date.now() + 3600_000).toISOString();
    answer = () => json({ accounts: [{ id: "c", provider: "claude", name: "Claude", sampled_at: new Date().toISOString(), local: true,
      windows: [{ label: "5h", window_minutes: 300, used_percent: 86, resets_at: at }] }] });
    render(<UltracodeStartRow on={false} onChange={() => {}} blocked="这台电脑的 Claude Code 是 2.1.250，要 2.1.284 或更新才能开 ultracode" />);
    await flush();
    expect(theSwitch()!.disabled).toBe(true);
    expect(theSwitch()!.className).toContain("disabled:shadow-[inset_0_0_0_1px_var(--ink-3)]");
    expect(host.querySelector("[data-ultracode-line]")!.textContent).toContain("2.1.250");
    expect(host.querySelector("[data-ultracode-quota]")!.textContent).toContain("5 小时 86%");
    expect(host.querySelector("[data-ultracode-quota-warn]")).toBeNull();
    render(<UltracodeStartRow on={false} onChange={() => {}} blocked="" />);
    await flush();
    expect(host.querySelector("[data-ultracode-quota-warn]")).not.toBeNull();
  });

  it("an out-of-date quota fades and says how old it is, as the usage bars do (#1906)", async () => {
    const at = new Date(Date.now() + 3 * 86400_000).toISOString();
    answer = () => json({ accounts: [{ id: "c", provider: "claude", name: "Claude", sampled_at: new Date(Date.now() - 2 * 3600_000).toISOString(), local: true, stale: true,
      windows: [{ label: "7d", window_minutes: 10080, used_percent: 62, resets_at: at }] }] });
    render(<UltracodeStartRow on={false} onChange={() => {}} blocked="" />);
    await flush();
    const line = host.querySelector("[data-ultracode-quota]")!;
    expect(line.textContent).toContain("每周 62%");
    expect(line.firstElementChild!.className).toContain("opacity-70");
    const stale = host.querySelector("[data-ultracode-quota-stale]")!;
    expect(stale.textContent).toBe("已过期 · 2小时前"); // its own line: the age is never cut off with the numbers
    expect(stale.className).toContain("opacity-70");
  });
});

describe("the workflow mark", () => {
  const marks = (html: string) => html.match(/data-ultracode-mark/g)?.length ?? 0;

  it("on the model pill while it is on, an icon and no words", () => {
    hub(["ultracode"]);
    const on = renderToStaticMarkup(<AgentModelPill session={{ ...base, ultracode: true }} />);
    expect(marks(on)).toBe(1);
    expect(on).toContain('aria-label="ultracode 开"');
    expect(marks(renderToStaticMarkup(<AgentModelPill session={{ ...base, ultracode: false }} />))).toBe(0);
    expect(marks(renderToStaticMarkup(<AgentModelPill session={base} />))).toBe(0);
  });

  it("on a Codex thread's pill at its \"ultra\" effort, the same mark", () => {
    const codex = { ...base, agent: "codex", source_kind: "codex_app_server", capabilities: { send: true }, model: "gpt-5.5", effort: "ultra", efforts: ["low", "medium", "high", "ultra"] } as Session;
    const html = renderToStaticMarkup(<AgentModelPill session={codex} />);
    expect(marks(html)).toBe(1);
    expect(html).toContain("很费额度");
    expect(marks(renderToStaticMarkup(<AgentModelPill session={{ ...codex, effort: "high" }} />))).toBe(0);
  });

  it("beside an agent row's title in the AI colour; a row without it is as before", () => {
    const props = { k: "s:mac/s1", reg: () => () => {}, hover: () => {}, active: false, to: { name: "session", mid: "mac", sid: "s1" } as const, go: () => () => {} };
    const on = renderToStaticMarkup(<SessionRow {...props} s={{ ...base, ultracode: true }} />);
    expect(marks(on)).toBe(1);
    expect(on).toMatch(/data-ultracode-mark[^>]*class="[^"]*text-ai/);
    const off = renderToStaticMarkup(<SessionRow {...props} s={{ ...base, ultracode: false }} />);
    expect(marks(off)).toBe(0);
    expect(off).toBe(renderToStaticMarkup(<SessionRow {...props} s={base} />));
  });

  it("not on an offline row: the process that had it is gone", () => {
    const props = { k: "s:mac/s1", reg: () => () => {}, hover: () => {}, active: false, to: { name: "session", mid: "mac", sid: "s1" } as const, go: () => () => {} };
    const offline = { ...base, status: "offline", state: "offline" as const, ultracode: true };
    expect(marks(renderToStaticMarkup(<SessionRow {...props} s={offline} />))).toBe(0);
  });

  it("stays the same element while a workflow line comes and goes under the title: it does not fade in again", () => {
    const props = { k: "s:mac/s1", reg: () => () => {}, hover: () => {}, active: false, to: { name: "session", mid: "mac", sid: "s1" } as const, go: () => () => {} };
    const on = { ...base, status: "working", state: "working" as const, ultracode: true };
    const flow = { ...on, workflows: [{ task_id: "w1", run_id: "r1", name: "audit", status: "running", running: true, phases: ["Audit"], phase: "Audit", phase_index: 1, agents_started: 2, agents_done: 0, agents_running: 2, started_at: new Date().toISOString(), agents: [] }] } as unknown as Session;
    render(<SessionRow {...props} s={on} />);
    const mark = host.querySelector("[data-ultracode-mark]");
    expect(mark).not.toBeNull();
    render(<SessionRow {...props} s={flow} />);
    expect(host.querySelector("[data-workflow-line]")).not.toBeNull();
    expect(host.querySelector("[data-ultracode-mark]")).toBe(mark);
    render(<SessionRow {...props} s={{ ...on, rev: 9 } as Session} />);
    expect(host.querySelector("[data-workflow-line]")).toBeNull();
    expect(host.querySelector("[data-ultracode-mark]")).toBe(mark);
  });
});
