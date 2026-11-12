import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("./store", () => {
  const state = { launches: {} as Record<string, import("./new-agent").AgentLaunch> };
  return { getState: () => state, putLaunch: (launch: import("./new-agent").AgentLaunch) => { state.launches[launch.id] = launch; }, upsertSession: vi.fn(), isOpen: () => true, onHubEvent: () => () => {} };
});
vi.mock("./router", () => ({ href: (r: { mid: string; sid: string }) => `/s/${r.mid}/${r.sid}`, navigate: vi.fn() }));

import { acknowledgeLaunch, deliverLaunch, launchAgent, onOpenNewAgent, openNewAgent, presetPrompt, startKeys } from "./new-agent";
import { getState, upsertSession } from "./store";
import { navigate } from "./router";
import { pickAttachment } from "./attachments";
import { OP_RETRY_MS } from "./ops";
import { platformOf } from "./shortcuts";

const session = { machine_id: "desktop", session_id: "remote-id", title: "示例", cwd: "/workspace/project", status: "idle" };
const input = { machineId: "desktop", cwd: "/workspace/project", launcher: "", text: "看一下附件", attachments: [] };
function response(body: unknown, status = 200) {
  return { ok: status < 400, status, text: async () => JSON.stringify(body) };
}

beforeEach(() => {
  vi.useFakeTimers();
  vi.stubGlobal("localStorage", { getItem: () => "test" });
  vi.stubGlobal("location", { pathname: "/other" });
  vi.stubGlobal("URL", { createObjectURL: vi.fn(() => "blob:preview"), revokeObjectURL: vi.fn() });
  getState().launches = {};
  vi.clearAllMocks();
});
afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); });

describe("本地优先启动", () => {
  it("opens the local conversation before any request and submits the prompt only once", async () => {
    let resolveCreate!: (value: unknown) => void;
    const fetcher = vi.fn().mockReturnValueOnce(new Promise((resolve) => { resolveCreate = resolve; })).mockResolvedValueOnce(response({ ok: true }));
    vi.stubGlobal("fetch", fetcher);
    const id = launchAgent(input);
    expect(navigate).toHaveBeenCalledWith({ name: "session", mid: "desktop", sid: id });
    expect(fetcher).not.toHaveBeenCalled();
    expect(getState().launches[id].text).toBe(input.text);
    vi.advanceTimersByTime(0);
    expect(JSON.parse(fetcher.mock.calls[0][1].body)).toEqual({ machine_id: "desktop", cwd: input.cwd, async: true, name_hint: input.text });
    const job = deliverLaunch(id);
    await job;
    expect(fetcher).toHaveBeenCalledTimes(1);
    resolveCreate(response({ session }));
    await vi.advanceTimersByTimeAsync(0);
    expect(upsertSession).toHaveBeenCalledWith(session);
    expect(fetcher.mock.calls[1][0]).toBe("/api/v1/sessions/desktop/remote-id/messages");
    expect(JSON.parse(fetcher.mock.calls[1][1].body)).toEqual({ text: input.text, images: [], client_msg_id: getState().launches[id].messageId });
    expect(getState().launches[id].phase).toBe("ready");
    expect(navigate).toHaveBeenCalledTimes(1);
  });

  it("sends images and uploaded file paths together after creation", async () => {
    const image = pickAttachment(new File([new Uint8Array([1, 2, 3])], "figure.png", { type: "image/png" }));
    const file = pickAttachment(new File(["report"], "report.txt", { type: "text/plain" }));
    const fetcher = vi.fn().mockResolvedValueOnce(response({ session })).mockResolvedValueOnce(response({ path: "/uploads/my report.txt" })).mockResolvedValueOnce(response({}));
    vi.stubGlobal("fetch", fetcher);
    const id = launchAgent({ ...input, attachments: [image, file] });
    await vi.advanceTimersByTimeAsync(0);
    expect(getState().launches[id].phase).toBe("ready");
    expect(fetcher.mock.calls[1][0]).toBe("/api/v1/machines/desktop/files?name=report.txt");
    expect(JSON.parse(fetcher.mock.calls[2][1].body)).toEqual({ text: "看一下附件\n'/uploads/my report.txt'", images: [{ mime: "image/png", data: "AQID" }], client_msg_id: getState().launches[id].messageId });
    expect(URL.revokeObjectURL).toHaveBeenCalledWith("blob:preview");
  });

  it("keeps the real session and attachments on a refused send, and retries under a new id", async () => {
    const image = pickAttachment(new File(["image"], "figure.png", { type: "image/png" }));
    const fetcher = vi.fn().mockResolvedValueOnce(response({ session })).mockResolvedValueOnce(response({ code: "input_draft", error: "draft", delivered: "none", clearable: "existing" }, 409)).mockResolvedValueOnce(response({}));
    vi.stubGlobal("fetch", fetcher);
    const id = launchAgent({ ...input, attachments: [image] });
    await vi.advanceTimersByTimeAsync(0);
    expect(getState().launches[id]).toMatchObject({ phase: "failed", session, clearable: "existing", uncertain: false });
    expect(getState().launches[id].attachments).toEqual([image]);
    expect(URL.revokeObjectURL).not.toHaveBeenCalled();
    const refused = fetcher.mock.calls[1][1].headers.get("X-Persona-Op-Id");
    await deliverLaunch(id, "existing");
    expect(fetcher.mock.calls.filter((c) => c[0] === "/api/v1/sessions")).toHaveLength(1);
    expect(fetcher.mock.calls[2][1].headers.get("X-Persona-Op-Id")).not.toBe(refused);
    expect(JSON.parse(fetcher.mock.calls[2][1].body)).toMatchObject({ client_msg_id: fetcher.mock.calls[2][1].headers.get("X-Persona-Op-Id"), clear_draft: "existing" });
  });

  it("never navigates away from another conversation when startup finishes", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValueOnce(response({ session })).mockResolvedValueOnce(response({})));
    const id = launchAgent(input);
    await vi.advanceTimersByTimeAsync(0);
    expect(getState().launches[id].phase).toBe("ready");
    expect(navigate).toHaveBeenCalledTimes(1);
  });

  it("sends a create and a first message that died on the way again under the same op ids", async () => {
    const fetcher = vi.fn().mockRejectedValueOnce(new TypeError("network")).mockResolvedValueOnce(response({ session })).mockRejectedValueOnce(new TypeError("network")).mockResolvedValueOnce(response({}));
    vi.stubGlobal("fetch", fetcher);
    const id = launchAgent(input);
    await vi.advanceTimersByTimeAsync(2000);
    expect(getState().launches[id]).toMatchObject({ phase: "ready", uncertain: false });
    const ops = fetcher.mock.calls.map((c) => c[1].headers.get("X-Persona-Op-Id"));
    expect(fetcher).toHaveBeenCalledTimes(4);
    expect(ops[1]).toBe(ops[0]);
    expect(ops[3]).toBe(ops[2]);
    expect(ops[2]).toBe(getState().launches[id].messageId);
  });

  it("leaves a start that never got an answer retryable, unchanged and under its op id, without asking", async () => {
    const fetcher = vi.fn().mockRejectedValue(new TypeError("network"));
    vi.stubGlobal("fetch", fetcher);
    const id = launchAgent(input);
    const startOpId = getState().launches[id].startOpId;
    await vi.advanceTimersByTimeAsync(OP_RETRY_MS + 5000);
    expect(getState().launches[id]).toMatchObject({ phase: "failed", uncertain: false, repeatOnly: true, startOpId });
  });

  it("asks only when the hub says it doesn't know, and a try after that is a new operation", async () => {
    const fetcher = vi.fn().mockResolvedValueOnce(response({ state: "pending" }, 202)).mockResolvedValueOnce(response({ state: "unknown" }, 202));
    vi.stubGlobal("fetch", fetcher);
    const id = launchAgent(input);
    const startOpId = getState().launches[id].startOpId;
    await vi.advanceTimersByTimeAsync(0);
    expect(fetcher).toHaveBeenCalledTimes(2);
    expect(getState().launches[id]).toMatchObject({ phase: "failed", uncertain: true });
    expect(getState().launches[id].startOpId).not.toBe(startOpId);
  });

  it("clears optimistic images only after matching transcript content arrives", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValueOnce(response({ session })).mockResolvedValueOnce(response({})));
    const id = launchAgent({ ...input, attachments: [pickAttachment(new File(["image"], "figure.png", { type: "image/png" }))] });
    await vi.advanceTimersByTimeAsync(0);
    acknowledgeLaunch("desktop", "remote-id", { text: input.text, images: [] });
    expect(getState().launches[id].outgoing).toBeDefined();
    acknowledgeLaunch("desktop", "remote-id", { text: input.text, images: [{}] });
    expect(getState().launches[id].outgoing).toBeUndefined();
  });

  it("clears the first message when the hub ties an item to its id, and not for a tie to another message", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValueOnce(response({ session })).mockResolvedValueOnce(response({})));
    const id = launchAgent(input);
    await vi.advanceTimersByTimeAsync(0);
    acknowledgeLaunch("desktop", "remote-id", { id: "u1", client_msg_id: "someone-else", text: input.text, images: [] });
    expect(getState().launches[id].outgoing).toBeDefined();
    acknowledgeLaunch("desktop", "remote-id", { id: "u2", client_msg_id: getState().launches[id].messageId, text: "改过的字", images: [] });
    expect(getState().launches[id].outgoing).toBeUndefined();
  });

  it("starts without a first message and never calls messages", async () => {
    const fetcher = vi.fn().mockResolvedValueOnce(response({ session }));
    vi.stubGlobal("fetch", fetcher);
    const id = launchAgent({ ...input, text: "" });
    await vi.advanceTimersByTimeAsync(0);
    expect(getState().launches[id].phase).toBe("ready");
    expect(fetcher).toHaveBeenCalledTimes(1);
  });

  it("preserves Codex text startup with no duplicate first message", async () => {
    const fetcher = vi.fn().mockResolvedValueOnce(response({ error: "needs prompt", code: "codex_needs_prompt" }, 409)).mockResolvedValueOnce(response({ session: { ...session, agent: "codex" } }));
    vi.stubGlobal("fetch", fetcher);
    const id = launchAgent(input);
    await vi.advanceTimersByTimeAsync(0);
    expect(getState().launches[id].phase).toBe("ready");
    expect(fetcher).toHaveBeenCalledTimes(2);
    expect(JSON.parse(fetcher.mock.calls[1][1].body)).toMatchObject({ prompt: input.text });
  });

  it("opens a Codex with no first message where the computer can, and sends nothing after it", async () => {
    const waiting = { ...session, session_id: "c0de0000-1111-4222-8333-444455556666", agent: "codex", awaiting_first_message: true };
    const fetcher = vi.fn().mockResolvedValueOnce(response({ error: "needs prompt", code: "codex_needs_prompt" }, 409)).mockResolvedValueOnce(response({ session: waiting }));
    vi.stubGlobal("fetch", fetcher);
    vi.stubGlobal("location", { pathname: "/other" });
    const id = launchAgent({ ...input, launcher: "codex", text: "" });
    await vi.advanceTimersByTimeAsync(0);
    expect(getState().launches[id]).toMatchObject({ phase: "ready", session: waiting });
    expect(fetcher).toHaveBeenCalledTimes(2);
    const body = JSON.parse(fetcher.mock.calls[1][1].body);
    expect(body).toMatchObject({ launcher: "codex" });
    expect(body).not.toHaveProperty("prompt");
    expect(body).not.toHaveProperty("async");
  });

  it("asks for the first message when an older computer starts a Codex only with one", async () => {
    const fetcher = vi.fn()
      .mockResolvedValueOnce(response({ error: "needs prompt", code: "codex_needs_prompt" }, 409))
      .mockResolvedValueOnce(response({ error: "needs prompt", code: "codex_needs_prompt" }, 409));
    vi.stubGlobal("fetch", fetcher);
    const id = launchAgent({ ...input, launcher: "codex", text: "" });
    await vi.advanceTimersByTimeAsync(0);
    expect(getState().launches[id]).toMatchObject({ phase: "failed" });
    expect(getState().launches[id].error).toContain("需要第一条消息");
    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  it("sends the picked model and effort with the start, and only the model to a Codex", async () => {
    const fetcher = vi.fn().mockResolvedValueOnce(response({ session })).mockResolvedValueOnce(response({}));
    vi.stubGlobal("fetch", fetcher);
    launchAgent({ ...input, launcher: "claude", model: "opus", effort: "high" });
    await vi.advanceTimersByTimeAsync(0);
    expect(JSON.parse(fetcher.mock.calls[0][1].body)).toMatchObject({ launcher: "claude", model: "opus", effort: "high" });

    const codex = vi.fn().mockResolvedValueOnce(response({ error: "needs prompt", code: "codex_needs_prompt" }, 409)).mockResolvedValueOnce(response({ session: { ...session, agent: "codex" } }));
    vi.stubGlobal("fetch", codex);
    launchAgent({ ...input, launcher: "codex", model: "gpt-5.5-mini", effort: "high" });
    await vi.advanceTimersByTimeAsync(0);
    const body = JSON.parse(codex.mock.calls[1][1].body);
    expect(body).toMatchObject({ launcher: "codex", model: "gpt-5.5-mini", prompt: input.text });
    expect(body).not.toHaveProperty("effort");
  });

  it("keeps exact host/workspace IDs through retries and the legacy Codex prompt fallback", async () => {
    const fetcher = vi.fn().mockRejectedValueOnce(new TypeError("network"))
      .mockResolvedValueOnce(response({ code: "codex_needs_prompt", error: "needs prompt" }, 409))
      .mockResolvedValueOnce(response({ session }));
    vi.stubGlobal("fetch", fetcher);
    const id = launchAgent({ ...input, workspaceId: "empty-nested", host: "orca", launcher: "custom-codex", model: "typed-model", effort: "high" });
    await vi.advanceTimersByTimeAsync(2000);
    expect(getState().launches[id]).toMatchObject({ phase: "ready", workspaceId: "empty-nested", host: "orca" });
    expect(fetcher).toHaveBeenCalledTimes(3);
    const bodies = fetcher.mock.calls.map((c) => JSON.parse(c[1].body));
    expect(bodies[0]).toEqual(bodies[1]);
    for (const body of bodies) {
      expect(body).toMatchObject({ workspace_id: "empty-nested", host: "orca", cwd: input.cwd, launcher: "custom-codex", model: "typed-model" });
      for (const field of ["permission_mode", "approval_policy", "approvals_reviewer", "sandbox_mode", "network_access", "permissions"]) expect(body).not.toHaveProperty(field);
    }
    expect(bodies[2]).toMatchObject({ prompt: input.text });
    expect(bodies[2]).not.toHaveProperty("effort");
    expect(fetcher.mock.calls[0][1].headers.get("X-Persona-Op-Id")).toBe(fetcher.mock.calls[1][1].headers.get("X-Persona-Op-Id"));
    expect(fetcher.mock.calls[2][1].headers.get("X-Persona-Op-Id")).not.toBe(fetcher.mock.calls[0][1].headers.get("X-Persona-Op-Id"));
  });

  it("sends the picked permission mode with the start, and none when following the launcher", async () => {
    const fetcher = vi.fn().mockResolvedValueOnce(response({ session })).mockResolvedValueOnce(response({}));
    vi.stubGlobal("fetch", fetcher);
    launchAgent({ ...input, launcher: "claude", permissionMode: "plan" });
    await vi.advanceTimersByTimeAsync(0);
    expect(JSON.parse(fetcher.mock.calls[0][1].body)).toMatchObject({ launcher: "claude", permission_mode: "plan" });

    const plain = vi.fn().mockResolvedValueOnce(response({ session })).mockResolvedValueOnce(response({}));
    vi.stubGlobal("fetch", plain);
    launchAgent({ ...input, launcher: "claude" });
    await vi.advanceTimersByTimeAsync(0);
    expect(JSON.parse(plain.mock.calls[0][1].body)).not.toHaveProperty("permission_mode");
  });

  it("sends ultracode only when it was turned on, never false (#3428), and not to a Codex", async () => {
    const on = vi.fn().mockResolvedValueOnce(response({ session })).mockResolvedValueOnce(response({}));
    vi.stubGlobal("fetch", on);
    launchAgent({ ...input, launcher: "claude", ultracode: true });
    await vi.advanceTimersByTimeAsync(0);
    expect(JSON.parse(on.mock.calls[0][1].body)).toMatchObject({ launcher: "claude", ultracode: true });

    for (const ultracode of [false, undefined]) {
      const off = vi.fn().mockResolvedValueOnce(response({ session })).mockResolvedValueOnce(response({}));
      vi.stubGlobal("fetch", off);
      launchAgent({ ...input, launcher: "claude", ultracode });
      await vi.advanceTimersByTimeAsync(0);
      expect(JSON.parse(off.mock.calls[0][1].body)).not.toHaveProperty("ultracode");
    }

    const codex = vi.fn().mockResolvedValueOnce(response({ error: "needs prompt", code: "codex_needs_prompt" }, 409)).mockResolvedValueOnce(response({ session: { ...session, agent: "codex" } }));
    vi.stubGlobal("fetch", codex);
    launchAgent({ ...input, launcher: "codex", ultracode: true });
    await vi.advanceTimersByTimeAsync(0);
    expect(JSON.parse(codex.mock.calls[1][1].body)).not.toHaveProperty("ultracode");
  });

  it("a start the hub refuses with ultracode retries without it, and the card says so (#3428)", async () => {
    for (const code of ["node_outdated", "ultracode_unsupported"]) {
      const fetcher = vi.fn()
        .mockResolvedValueOnce(response({ code, error: "这台电脑的 Claude Code 太旧，不能开 ultracode" }, 409))
        .mockResolvedValueOnce(response({ session }))
        .mockResolvedValueOnce(response({}));
      vi.stubGlobal("fetch", fetcher);
      const id = launchAgent({ ...input, launcher: "claude", ultracode: true });
      await vi.advanceTimersByTimeAsync(0);
      const failed = getState().launches[id];
      expect(failed.phase).toBe("failed");
      expect(failed.ultracode).toBeUndefined();
      expect(failed.error).toBe("没能带 ultracode 开始：这台电脑的 Claude Code 太旧，不能开 ultracode。重试会不带 ultracode");
      await deliverLaunch(id);
      await vi.advanceTimersByTimeAsync(0);
      expect(JSON.parse(fetcher.mock.calls[1][1].body)).not.toHaveProperty("ultracode");
      expect(getState().launches[id].phase).toBe("ready");
    }
    // another failure keeps it: the retry asks again
    const other = vi.fn().mockResolvedValueOnce(response({ error: "busy" }, 503)).mockResolvedValueOnce(response({ session })).mockResolvedValueOnce(response({}));
    vi.stubGlobal("fetch", other);
    const id = launchAgent({ ...input, launcher: "claude", ultracode: true });
    await vi.advanceTimersByTimeAsync(0);
    expect(getState().launches[id].ultracode).toBe(true);
  });

  it("does not replace a Codex multimodal first message with a fake initialization prompt", async () => {
    const fetcher = vi.fn().mockResolvedValue(response({ error: "needs prompt", code: "codex_needs_prompt" }, 409));
    vi.stubGlobal("fetch", fetcher);
    const id = launchAgent({ ...input, attachments: [pickAttachment(new File(["image"], "figure.png", { type: "image/png" }))] });
    await vi.advanceTimersByTimeAsync(0);
    expect(getState().launches[id]).toMatchObject({ phase: "failed", uncertain: false });
    expect(getState().launches[id].error).toContain("第一条消息不能带附件");
    expect(fetcher).toHaveBeenCalledTimes(1);
  });
});

describe("opened from another page", () => {
  it("exports an exact workspace preset to consumers", () => {
    const got: unknown[] = [];
    const off = onOpenNewAgent((p) => got.push(p));
    const workspace = { machineId: "desktop", workspaceId: "child", cwd: input.cwd, host: "orca", owner: "opaque-owner", manager: "assistant" };
    openNewAgent({ workspace });
    off();
    expect(got).toEqual([{ workspace }]);
  });
  it("puts the preset first and keeps what was being written", () => {
    expect(presetPrompt("<ctx/>\n\n", "")).toBe("<ctx/>\n\n");
    expect(presetPrompt("<ctx/>\n\n", "  修一下  ")).toBe("<ctx/>\n\n修一下");
    expect(presetPrompt("<ctx/>\n\n", "<ctx/>\n\n修一下")).toBe("<ctx/>\n\n修一下");
    expect(presetPrompt(undefined, "草稿")).toBe("草稿");
  });

  it("reaches the shell's listener", () => {
    const got: unknown[] = [];
    const off = onOpenNewAgent((p) => got.push(p));
    openNewAgent({ prompt: "x", machineId: "m", cwd: "/a" });
    off();
    openNewAgent({ prompt: "y" });
    expect(got).toEqual([{ prompt: "x", machineId: "m", cwd: "/a" }]);
  });
});

describe("startKeys", () => {
  it("names ⌘ ↵ on Apple, Ctrl ↵ elsewhere, nothing on a touch-only device", () => {
    expect(startKeys(platformOf({ platform: "MacIntel" }), false)).toBe("⌘ ↵");
    expect(startKeys(platformOf({ platform: "Win32" }), false)).toBe("Ctrl ↵");
    expect(startKeys(platformOf({ platform: "Linux x86_64" }), false)).toBe("Ctrl ↵");
    expect(startKeys("mac", true)).toBeNull();
    expect(startKeys("other", true)).toBeNull();
  });
});

describe("work-session delivery", () => {
  const work = { ...input, launcher: "codex-team", agentType: "codex", model: "model-from-catalog", workSession: true };
  const target = { machine_id: work.machineId, cwd: work.cwd, launcher: work.launcher, agent_type: work.agentType, model: work.model, work_session: true };

  it("sends the same target with Codex's first message and retries a lost reply under the same operation", async () => {
    const fetcher = vi.fn()
      .mockResolvedValueOnce(response({ code: "codex_needs_prompt" }, 409))
      .mockRejectedValueOnce(new TypeError("lost response"))
      .mockResolvedValueOnce(response({ session }));
    vi.stubGlobal("fetch", fetcher);
    const id = launchAgent(work);
    vi.stubGlobal("location", { pathname: `/s/desktop/${id}` });
    await vi.advanceTimersByTimeAsync(2000);
    expect(fetcher.mock.calls.map((c) => [c[0], JSON.parse(c[1].body)])).toEqual([
      ["/api/v1/sessions", { ...target, async: true, name_hint: work.text }],
      ["/api/v1/sessions", { ...target, prompt: work.text }],
      ["/api/v1/sessions", { ...target, prompt: work.text }],
    ]);
    expect(fetcher.mock.calls[1][1].headers.get("X-Persona-Op-Id")).toBe(fetcher.mock.calls[2][1].headers.get("X-Persona-Op-Id"));
    expect(navigate).toHaveBeenLastCalledWith({ name: "session", mid: "desktop", sid: "remote-id" }, true);
  });

  it("stops after a Codex creation reports an unknown outcome", async () => {
    const fetcher = vi.fn().mockResolvedValueOnce(response({ state: "unknown" }, 202));
    vi.stubGlobal("fetch", fetcher);
    const id = launchAgent({ ...work, inlinePrompt: true });
    await vi.advanceTimersByTimeAsync(0);
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(JSON.parse(fetcher.mock.calls[0][1].body)).toEqual({ ...target, prompt: work.text });
    expect(getState().launches[id]).toMatchObject({ phase: "failed", uncertain: true, repeatOnly: true, model: work.model });
  });
});
