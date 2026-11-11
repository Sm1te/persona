import { afterEach, describe, expect, it, vi } from "vitest";
import { api, ApiError, attachSession } from "./api";
import { isStoppedAgent, resumeCommand, sessionCan, shellPath, sessionDeletable, sessionMenuActions, sourceSubtitle } from "./format";
import type { Session, SessionCapabilities } from "./types";

const legacy: Session = { machine_id: "m", session_id: "raw-thread", title: "", cwd: "/tmp/project", status: "working" };
const operations: (keyof SessionCapabilities)[] = ["read", "send", "interrupt", "approve", "terminal"];
const history: Session = { ...legacy, source_kind: "codex_history", capabilities: { read: true }, read_only_reason: "外部进程正在使用此会话" };
const live: Session = { ...legacy, source_kind: "codex_app_server", source_thread_id: "real-thread", source_instance_id: "instance", parent_thread_id: "parent", capabilities: { read: true, send: true, attach: true } };

afterEach(() => vi.unstubAllGlobals());

describe("session capability contract", () => {
  it("keeps legacy behavior without guessing from IDs or missing metadata", () => {
    for (const op of operations) expect(sessionCan(legacy, op)).toBe(true);
    expect(sessionCan({ ...legacy, session_id: "aabbccddeeff" }, "send")).toBe(true);
    expect(sessionCan({ ...legacy, source_kind: "" }, "send")).toBe(true);
    expect(sessionCan(undefined, "send")).toBe(false);
    expect(sessionCan(legacy, "attach")).toBe(false);
    expect(sourceSubtitle(legacy)).toBe("");
  });

  it("denies new sources without capabilities and denies omitted flags", () => {
    for (const op of operations) expect(sessionCan({ ...live, capabilities: undefined }, op)).toBe(false);
    expect(sessionCan({ ...live, capabilities: undefined }, "attach")).toBe(false);
    expect(sessionCan(history, "read")).toBe(true);
    for (const op of operations.filter((op) => op !== "read")) expect(sessionCan(history, op)).toBe(false);
    expect(sourceSubtitle(history)).toContain("仅查看：外部进程正在使用此会话");
    expect(sourceSubtitle({ ...history, source_kind: "claude_bg", read_only_reason: "回复请在电脑上打开" })).toBe("Claude 后台 · 仅查看：回复请在电脑上打开");
  });

  it("names Claude sessions outside Persona's terminals and lets them send only when the node says so", () => {
    const desktop: Session = { ...legacy, agent: "claude", source_kind: "claude_desktop", capabilities: { read: true, send: true } };
    expect(sourceSubtitle(desktop)).toBe("Claude 桌面版");
    expect(sessionCan(desktop, "send")).toBe(true);
    for (const op of ["interrupt", "approve", "terminal"] as const) expect(sessionCan(desktop, op)).toBe(false);
    expect(sessionMenuActions(desktop)).toEqual([]);
    const terminal: Session = { ...desktop, source_kind: "claude_cli", capabilities: { read: true }, read_only_reason: "回复请在电脑上" };
    expect(sourceSubtitle(terminal)).toBe("终端里的 Claude · 仅查看：回复请在电脑上");
    expect(sessionCan(terminal, "send")).toBe(false);
  });

  it("offers attach but not terminal, close or interrupt until explicitly granted", () => {
    expect(sessionMenuActions(history)).toEqual([]);
    expect(sessionMenuActions(live)).toEqual(["attach"]);
    expect(sessionMenuActions({ ...live, attached: true })).toEqual([]);
    expect(sessionMenuActions({ ...live, capabilities: { ...live.capabilities, interrupt: true } })).toEqual(["attach", "stop"]);
    expect(sessionMenuActions({ ...live, status: "idle", capabilities: { ...live.capabilities, interrupt: true } })).toEqual(["attach"]);
    expect(sessionMenuActions(legacy)).toEqual(["stop", "resume", "close"]);
    expect(resumeCommand(live)).toBe("cd /tmp/project && codex resume real-thread");
  });

  it("offers runtime close independently of terminal access and respects an explicit refusal", () => {
    const runtime: Session = { ...legacy, source_kind: "runtime", capabilities: { read: true, interrupt: true, close: true } };
    expect(sessionCan(runtime, "terminal")).toBe(false);
    expect(sessionCan(runtime, "close")).toBe(true);
    expect(sessionMenuActions(runtime)).toEqual(["stop", "close"]);
    expect(sessionMenuActions({ ...runtime, status: "offline" })).toEqual([]);
    expect(sessionMenuActions({ ...legacy, capabilities: { terminal: true, interrupt: true, close: false } })).toEqual(["stop", "resume"]);
    expect(sessionCan({ ...runtime, capabilities: { terminal: true } }, "close")).toBe(true);
    expect(sessionCan(undefined, "close")).toBe(false);
  });

  it("quotes the folder in the resume command, and gives only the command without one", () => {
    expect(resumeCommand({ ...legacy, cwd: "/Users/me/My Project" })).toBe("cd '/Users/me/My Project' && claude --resume raw-thread");
    expect(resumeCommand({ ...legacy, cwd: "/tmp/it's; $(rm -rf ~) & x" })).toBe(`cd '/tmp/it'\\''s; $(rm -rf ~) & x' && claude --resume raw-thread`);
    expect(resumeCommand({ ...legacy, cwd: "~/My Project" })).toBe("cd ~/'My Project' && claude --resume raw-thread");
    expect(resumeCommand({ ...legacy, cwd: "" })).toBe("claude --resume raw-thread");
    expect(resumeCommand({ ...legacy, agent: "traex", cwd: "" })).toBe("traex resume raw-thread");
  });

  it("keeps plain paths as they are and a leading ~ outside the quotes", () => {
    expect(shellPath("/home/me/work-2/a_b.c")).toBe("/home/me/work-2/a_b.c");
    expect(shellPath("~")).toBe("~");
    expect(shellPath("~/.persona/assistant")).toBe("~/.persona/assistant");
    expect(shellPath("~/项目")).toBe("~/'项目'");
    expect(shellPath("~user/x y")).toBe("'~user/x y'");
    expect(shellPath("a|b>c")).toBe("'a|b>c'");
  });

  it("names a runtime's session by its agent_label and offers only what it declared", () => {
    const dsh: Session = { ...legacy, agent: "dsh", agent_label: "DSH", source_kind: "runtime", source_instance_id: "dsh",
      capabilities: { read: true, send: true, interrupt: true, approve: true } };
    expect(sourceSubtitle(dsh)).toBe("DSH");
    expect(sessionMenuActions(dsh)).toEqual(["stop"]);
    expect(sourceSubtitle({ ...dsh, capabilities: { read: true }, read_only_reason: "DSH 没有开放从手机发消息" })).toBe("DSH · 仅查看：DSH 没有开放从手机发消息");
  });
});

describe("a stopped Agent (#4754)", () => {
  const stopped: Session = { ...legacy, agent: "claude", status: "offline", capabilities: { read: true, terminal: true }, read_only_reason: "Agent 已停止" };

  it("holds a native row that carries capabilities to them: read and terminal, nothing else", () => {
    expect(sessionCan(stopped, "read")).toBe(true);
    expect(sessionCan(stopped, "terminal")).toBe(true);
    for (const op of ["send", "interrupt", "approve", "model", "modes"] as const) expect(sessionCan(stopped, op)).toBe(false);
    expect(sessionCan(stopped, "attach")).toBe(false); // attach is a source's, never a native row's
    expect(sessionCan(stopped, "close")).toBe(true); // close follows terminal on a row that does not say
    // its node's own words never show as a source line
    expect(sourceSubtitle(stopped)).toBe("");
  });

  it("is a native, non-assistant row that is offline or interrupted with read and terminal and no send", () => {
    expect(isStoppedAgent(stopped)).toBe(true);
    expect(isStoppedAgent({ ...stopped, status: "interrupted" })).toBe(true);
    expect(isStoppedAgent({ ...stopped, status: "idle" })).toBe(false); // a live tab on Claude's agents list says the same, but runs
    expect(isStoppedAgent({ ...stopped, capabilities: { read: true, terminal: true, send: true } })).toBe(false);
    expect(isStoppedAgent({ ...stopped, capabilities: { read: true } })).toBe(false); // an archive's snapshot: no terminal
    expect(isStoppedAgent({ ...stopped, source_kind: "claude_cli" })).toBe(false);
    expect(isStoppedAgent({ ...stopped, assistant: true })).toBe(false);
    expect(isStoppedAgent(undefined)).toBe(false);
  });

  it("keeps an older node's offline row as it was: no capabilities, so it may still be sent to (a message resumes it there)", () => {
    const old: Session = { ...legacy, status: "offline" };
    expect(isStoppedAgent(old)).toBe(false);
    for (const op of operations) expect(sessionCan(old, op)).toBe(true);
  });

  it("is left out of Home's Continue, which offers only rows that take messages", () => {
    expect(sessionCan(stopped, "send")).toBe(false);
    expect(sessionCan({ ...stopped, capabilities: { read: true, terminal: true, send: true }, status: "idle" }, "send")).toBe(true);
  });
});

describe("delete a conversation", () => {
  it("offers it for what the hub keeps out of the lists, not the assistant's or one still starting", () => {
    const offline: Session = { ...legacy, status: "offline" };
    expect(sessionDeletable(offline)).toBe(true);
    expect(sessionDeletable({ ...offline, start_error: "claude did not become ready" })).toBe(true);
    expect(sessionDeletable({ ...offline, assistant: true })).toBe(false);
    expect(sessionDeletable({ ...offline, starting: true })).toBe(false);
  });

  it("takes finished, unknown and interrupted sessions as the hub, iOS and Android do (#801, #1461, #5974)", () => {
    for (const status of ["done", "unknown", "interrupted"]) {
      expect(sessionDeletable({ ...legacy, status })).toBe(true);
      expect(sessionDeletable({ ...live, status })).toBe(true); // a Codex App thread: no terminal to close it from
      expect(sessionDeletable({ ...history, status })).toBe(true);
      expect(sessionDeletable({ ...legacy, status, assistant: true })).toBe(false);
    }
  });

  it("takes idle (finished and seen) ones too, a Codex App thread the App still lists included: the hub keeps them hidden", () => {
    expect(sessionDeletable({ ...legacy, status: "idle" })).toBe(true);
    expect(sessionDeletable({ ...live, status: "idle" })).toBe(true);
  });

  it("leaves out running ones: the hub answers 409", () => {
    for (const status of ["working", "blocked"]) expect(sessionDeletable({ ...legacy, status })).toBe(false);
  });
});

describe("attach API", () => {
  it("posts once to the encoded session attach route", async () => {
    vi.stubGlobal("localStorage", { getItem: () => "device-token" });
    const fetch = vi.fn().mockResolvedValue(new Response("{}", { status: 200 }));
    vi.stubGlobal("fetch", fetch);
    await attachSession("machine/id", "opaque-id");
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(fetch.mock.calls[0][0]).toBe("/api/v1/sessions/machine%2Fid/opaque-id/attach");
    expect(fetch.mock.calls[0][1].method).toBe("POST");
  });

  it("preserves unknown delivery failures without automatically resending", async () => {
    vi.stubGlobal("localStorage", { getItem: () => "device-token" });
    const fetch = vi.fn().mockResolvedValue(new Response('{"error":"may have arrived","delivered":"unknown"}', { status: 502 }));
    vi.stubGlobal("fetch", fetch);
    const error = await api("/sessions/m/opaque-id/messages", { method: "POST", json: { text: "继续", client_msg_id: "message-id" } }).catch((e: unknown) => e);
    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).body.delivered).toBe("unknown");
    expect(fetch).toHaveBeenCalledTimes(1);
  });
});
