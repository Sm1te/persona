import { afterEach, describe, expect, it, vi } from "vitest";
import { closeFinished, finishedCount, parseListView, previewFinished, readListView, rememberListView, resolveListView } from "./terminal-list";
import { parse, routePath } from "./router";
import { buildTerminalTree, type PaneHostDTO } from "./terminals";
import { standaloneShells } from "../views/terminal/work-terminal-layout";
import type { Session } from "./types";

afterEach(() => vi.unstubAllGlobals());

function memory() {
  const m = new Map<string, string>();
  return { getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => void m.set(k, v) };
}

const host: PaneHostDTO = {
  kind: "herdr", stream: true, control: true, new_tab: true,
  workspaces: [{ id: "w1", label: "app" }],
  tabs: [{ id: "t1", workspace_id: "w1", label: "agent-1" }, { id: "t2", workspace_id: "w1", label: "shell" }],
  panes: [
    { pane: "w1:p1", terminal: "pane:w1:p1", tab_id: "t1", workspace_id: "w1", agent: "claude", session_id: "live" },
    { pane: "w1:p2", terminal: "pane:w1:p2", tab_id: "t1", workspace_id: "w1", agent_session: "stopped-1", foreground: "zsh", cwd: "/home/me/app" },
    { pane: "w1:p3", terminal: "pane:w1:p3", tab_id: "t2", workspace_id: "w1", title: "build", foreground: "npm", cwd: "/home/me/app" },
  ],
};
const sessions: Session[] = [
  { machine_id: "m1", session_id: "live", title: "Fix the login", cwd: "/home/me/app", status: "working" },
  { machine_id: "m1", session_id: "stopped-1", title: "Write the docs", cwd: "/home/me/app", status: "offline", capabilities: { read: true, terminal: true } },
];

describe("the Sessions | Terminals switch", () => {
  it("takes the address's, then this browser's last, then the sessions", () => {
    expect(resolveListView("terminals", "sessions", { panes: true, session: false })).toBe("terminals");
    expect(resolveListView(undefined, "terminals", { panes: true, session: false })).toBe("terminals");
    expect(resolveListView("sessions", "terminals", { panes: true, session: false })).toBe("sessions");
    const store = memory();
    expect(readListView(store)).toBe("sessions");
    rememberListView("terminals", store);
    expect(readListView(store)).toBe("terminals");
  });

  it("shows the sessions beside an open session and where the hub cannot list terminals", () => {
    expect(resolveListView("terminals", "terminals", { panes: true, session: true })).toBe("sessions");
    expect(resolveListView("terminals", "terminals", { panes: false, session: false })).toBe("sessions");
  });

  it("is deep-linkable as /terminal?view=terminals, an open terminal kept", () => {
    expect(parse("/terminal", "?view=terminals")).toEqual({ name: "terminal", view: "terminals" });
    expect(parse("/terminal/m1/pane%3Aw1%3Ap3", "?view=terminals")).toEqual({ name: "terminal", mid: "m1", id: "pane:w1:p3", view: "terminals" });
    expect(parse("/terminal", "?view=other")).toEqual({ name: "terminal" });
    expect(routePath({ name: "terminal", view: "terminals" })).toBe("/terminal?view=terminals");
    expect(routePath({ name: "terminal", mid: "m1", id: "pane:w1:p3", view: "sessions" })).toBe("/terminal/m1/pane%3Aw1%3Ap3?view=sessions");
    expect(parseListView("terminals")).toBe("terminals");
    expect(parseListView(null)).toBeUndefined();
  });
});

describe("the Terminals list", () => {
  const tree = buildTerminalTree({ hosts: [host] }, sessions);

  it("lists plain terminals and the ones stopped Agents left, never a running Agent's pane", () => {
    const shells = standaloneShells(tree);
    expect(shells.flatMap((sp) => sp.tabs.flatMap((t) => t.panes.map((p) => p.pane)))).toEqual(["w1:p2", "w1:p3"]);
  });

  it("names a stopped Agent's terminal after it and says what runs in a plain one", () => {
    const panes = tree.flatMap((sp) => sp.tabs.flatMap((t) => t.panes));
    const finished = panes.find((p) => p.pane === "w1:p2")!;
    expect(finished.title).toBe("已结束：Write the docs");
    expect(finished.detail).toBe("zsh · ~/app");
    expect(finished.agentSession).toBe("stopped-1");
    const plain = panes.find((p) => p.pane === "w1:p3")!;
    expect(plain).toMatchObject({ title: "build", detail: "npm · ~/app", agentSession: "" });
    // a stopped Agent the computer no longer lists: a short id
    const unknown = buildTerminalTree({ hosts: [host] }, []).flatMap((sp) => sp.tabs.flatMap((t) => t.panes)).find((p) => p.pane === "w1:p2")!;
    expect(unknown.title).toBe("已结束：stopped-");
    expect(finishedCount(tree)).toBe(1);
    expect(finishedCount(buildTerminalTree({ hosts: [{ ...host, panes: host.panes.filter((p) => !p.agent_session) }] }, sessions))).toBe(0);
  });
});

describe("Close finished terminals", () => {
  it("previews first, then closes exactly the panes the user confirmed", async () => {
    vi.stubGlobal("localStorage", { getItem: () => "device-token" });
    const replies = [
      { closed: [{ pane: "w1:p2", session_id: "stopped-1", tab_id: "t1", workspace_id: "w1" }], kept: [{ pane: "w1:p9", session_id: "s9", why: "vim" }], preview: true },
      { closed: [{ pane: "w1:p2", session_id: "stopped-1", tab_id: "t1", workspace_id: "w1" }], kept: [] },
    ];
    const fetch = vi.fn(async () => new Response(JSON.stringify(replies.shift()), { status: 200 }));
    vi.stubGlobal("fetch", fetch);

    const preview = await previewFinished("m 1");
    expect(preview.closed.map((c) => c.pane)).toEqual(["w1:p2"]);
    expect(preview.kept).toHaveLength(1);
    expect(preview.preview).toBe(true);
    const done = await closeFinished("m 1", preview.closed.map((c) => c.pane));
    expect(done).toMatchObject({ closed: [{ pane: "w1:p2" }], kept: [], preview: false });

    const calls = fetch.mock.calls as unknown as [string, RequestInit][];
    expect(calls.map(([url]) => url)).toEqual(["/api/v1/machines/m%201/panes/close_finished", "/api/v1/machines/m%201/panes/close_finished"]);
    expect(calls.map(([, init]) => init.method)).toEqual(["POST", "POST"]);
    expect(calls.map(([, init]) => JSON.parse(String(init.body)))).toEqual([{ preview: true }, { panes: "w1:p2" }]);
  });

  it("reads an answer without lists as nothing to close", async () => {
    const result = await previewFinished("m1", async () => ({}));
    expect(result).toEqual({ closed: [], kept: [], preview: false });
  });
});
