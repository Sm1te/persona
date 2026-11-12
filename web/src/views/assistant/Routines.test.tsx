// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { forgetProviderNames } from "../../lib/provider-names";
import type { Run, Task } from "../../lib/routines";
import { Routines } from "./Routines";

vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
});

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const task: Task = { id: "task-1", title: "Daily summary", prompt: "Summarize the day", repeat: "daily", time: "09:00", enabled: true };
const run: Run = { id: "run-1", kind: "task", ref_id: task.id, title: "Completed summary", due: "2026-11-05T09:00:00Z", at: "2026-11-05T09:00:00Z", status: "ok" };
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const failure = () => json({ error: "run history unavailable" }, 500);
// errorText explains a 5xx and keeps the hub's own text after it (#3728)
const failedText = /读不到运行记录：.*run history unavailable/;

let runsReply: () => Response | Promise<Response>;
let tasksReply: () => Response;
let providers: unknown[];
// the hub is unreachable: every request, a change or a read, answers 502
let down: boolean;
let calls: string[];
let host: HTMLDivElement;
let root: Root;

beforeEach(() => {
  calls = [];
  down = false;
  runsReply = failure;
  tasksReply = () => json({ tasks: [task] });
  providers = [];
  forgetProviderNames();
  vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL) => {
    const path = String(input);
    calls.push(path);
    if (down) return Promise.resolve(json({ error: "hub unreachable" }, 502));
    if (path === "/api/v1/assistant/tasks") return Promise.resolve(tasksReply());
    if (path === "/api/v1/assistant/runs") return Promise.resolve(runsReply());
    if (path === "/api/v1/model-config") return Promise.resolve(json({ providers }));
    throw new Error(`Unexpected request: ${path}`);
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

const render = () => act(async () => root.render(<Routines onToggleSidebar={() => {}} />));
const history = () => host.querySelector("aside")!;
const retry = () => [...history().querySelectorAll("button")].find((b) => b.textContent === "重试");
const refresh = () => act(async () => host.querySelector<HTMLButtonElement>('button[aria-label="刷新"]')!.click());
// lets a refused change and the reload after it finish (each reply's body is read on a later task)
const flush = () => act(async () => {
  for (let i = 0; i < 10; i++) await new Promise((resolve) => setTimeout(resolve, 0));
});

describe("routine run history loading", () => {
  it("uses green success marks for completed runs without coloring failed-run surfaces", async () => {
    runsReply = () => json({ runs: [run, { ...run, id: "failed-run", title: "Failed summary", status: "failed" }] });
    await render();
    const marks = [...history().querySelectorAll('[role="img"]')];
    expect(marks.some((mark) => mark.classList.contains("bg-green-tint") && mark.classList.contains("text-green"))).toBe(true);
    expect(marks.some((mark) => mark.classList.contains("bg-field") && mark.classList.contains("text-red"))).toBe(true);
  });

  it("shows a failed request's reason and retry while keeping the tasks visible", async () => {
    await render();

    expect(host.textContent).toContain(task.title);
    expect(history().textContent).toMatch(failedText);
    expect(history().textContent).not.toContain("加载中");
    expect(history().textContent).not.toContain("还没有运行过");
    expect(retry()).toBeDefined();
  });

  it("shows successful history independently of a failed task request", async () => {
    tasksReply = () => json({ error: "tasks unavailable" }, 500);
    runsReply = () => json({ runs: [run] });
    await render();

    expect(host.textContent).toContain("读不到定时任务");
    expect(host.textContent).toContain("tasks unavailable");
    expect(history().textContent).toContain(run.title);
    expect(history().textContent).not.toContain("读不到运行记录");
    expect(retry()).toBeUndefined();
  });

  it.each([
    { name: "nonempty", runs: [run], expected: run.title },
    { name: "empty", runs: [] as Run[], expected: "还没有运行过" },
  ])("retries both requests and recovers to $name history", async ({ runs, expected }) => {
    await render();
    expect(retry()).toBeDefined();
    runsReply = () => json({ runs });

    await act(async () => retry()!.click());

    expect(calls.filter((path) => path === "/api/v1/assistant/tasks")).toHaveLength(2);
    expect(calls.filter((path) => path === "/api/v1/assistant/runs")).toHaveLength(2);
    expect(host.textContent).toContain(task.title);
    expect(history().textContent).toContain(expected);
    expect(history().textContent).not.toMatch(failedText);
    expect(history().textContent).not.toContain("加载中");
    expect(retry()).toBeUndefined();
  });

  it("shows a refresh failure after earlier success and clears it when refreshed successfully", async () => {
    runsReply = () => json({ runs: [run] });
    await render();
    expect(history().textContent).toContain(run.title);

    runsReply = failure;
    await refresh();

    expect(host.textContent).toContain(task.title);
    expect(history().textContent).toMatch(failedText);
    expect(history().textContent).not.toContain(run.title);
    expect(history().textContent).not.toContain("加载中");
    expect(retry()).toBeDefined();

    const recovered = { ...run, id: "run-2", title: "Latest summary" };
    runsReply = () => json({ runs: [recovered] });
    await refresh();

    expect(history().textContent).toContain(recovered.title);
    expect(history().textContent).not.toMatch(failedText);
    expect(retry()).toBeUndefined();
  });

  it("ignores an older request's failure after a newer refresh succeeded", async () => {
    let finish!: (response: Response) => void;
    const pending = new Promise<Response>((resolve) => { finish = resolve; });
    runsReply = () => pending;
    await render();
    expect(history().textContent).toContain("加载中");

    runsReply = () => json({ runs: [run] });
    await refresh();
    expect(history().textContent).toContain(run.title);

    await act(async () => finish(failure()));

    expect(history().textContent).toContain(run.title);
    expect(history().textContent).not.toMatch(failedText);
    expect(retry()).toBeUndefined();
  });
});

describe("a change the hub refuses while the list cannot be read again", () => {
  const switchOf = () => host.querySelector<HTMLButtonElement>('[role="switch"]')!;
  const rowOf = (title: string) => [...host.querySelectorAll<HTMLElement>('[role="button"]')].find((el) => el.getAttribute("aria-label")?.includes(title));

  it.each([
    { name: "off", enabled: true },
    { name: "on", enabled: false },
  ])("puts the switch back when turning a routine $name fails", async ({ enabled }) => {
    tasksReply = () => json({ tasks: [{ ...task, enabled }] });
    runsReply = () => json({ runs: [] });
    await render();
    expect(switchOf().getAttribute("aria-checked")).toBe(String(enabled));

    down = true;
    await act(async () => switchOf().click());
    await flush();

    expect(calls).toContain(`/api/v1/assistant/tasks/${task.id}`);
    // the reload after the failure ran and failed too
    expect(calls.filter((path) => path === "/api/v1/assistant/tasks")).toHaveLength(2);
    expect(history().textContent).toContain("读不到运行记录");
    expect(switchOf().getAttribute("aria-checked")).toBe(String(enabled));
  });

  it("brings a one-off back when running it now fails", async () => {
    const once: Task = { id: "once-1", title: "Call the bank", prompt: "Remind me to call the bank", repeat: "once", at: "2099-01-01T09:00:00Z", enabled: true };
    tasksReply = () => json({ tasks: [task, once] });
    runsReply = () => json({ runs: [] });
    await render();
    const row = rowOf(once.title)!;
    expect(row).toBeDefined();

    down = true;
    act(() => void row.dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 10, clientY: 10 })));
    const run = [...document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')].find((el) => el.textContent?.includes("立即运行"));
    expect(run).toBeDefined();
    await act(async () => run!.click());
    await flush();

    expect(calls).toContain(`/api/v1/assistant/tasks/${once.id}/run`);
    expect(calls.filter((path) => path === "/api/v1/assistant/tasks")).toHaveLength(2);
    expect(history().textContent).toContain("读不到运行记录");
    expect(rowOf(once.title)).toBeDefined();
  });
});

describe("what a task runs with (#5029)", () => {
  it("names its launcher by the provider's display name with the ID in the tooltip; an rc alias by its ID", async () => {
    providers = [{ id: "launcher.relay-example-com", name: "我的中转 站", kind: "launcher" }];
    tasksReply = () => json({ tasks: [{ ...task, launcher: "relay-example-com" }, { ...task, id: "task-2", title: "Weekly review", launcher: "cc_glm" }] });
    runsReply = () => json({ runs: [] });
    await render();
    await flush();
    const line = (title: string) => [...host.querySelectorAll<HTMLElement>('[role="button"]')].find((el) => el.textContent?.includes(title))!.querySelector<HTMLElement>("div.truncate > span.truncate")!;
    expect([line("Daily summary").textContent, line("Daily summary").title]).toEqual(["我的中转 站", "relay-example-com"]);
    expect([line("Weekly review").textContent, line("Weekly review").hasAttribute("title")]).toEqual(["cc_glm", false]);
  });
});
