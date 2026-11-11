import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "./api";
import { clearResume, resumeAgent, resumeState, subscribeResume } from "./agent-resume";

afterEach(() => {
  clearResume("m1", "s1");
  vi.unstubAllGlobals();
});

describe("Resume a stopped Agent (#4754)", () => {
  it("posts the session's id and nothing else, and says resuming, then done", async () => {
    let finish: () => void = () => {};
    const post = vi.fn(() => new Promise<unknown>((resolve) => (finish = () => resolve({ session: {} }))));
    const seen: string[] = [];
    const off = subscribeResume(() => seen.push(resumeState("m1", "s1").phase));
    const going = resumeAgent("m1", "s1", post);
    expect(resumeState("m1", "s1").phase).toBe("resuming");
    expect(post).toHaveBeenCalledWith("/sessions", { method: "POST", json: { machine_id: "m1", resume_session_id: "s1" } });
    finish();
    expect(await going).toBe(true);
    off();
    expect(resumeState("m1", "s1").phase).toBe("done");
    expect(seen).toEqual(["resuming", "done"]);
  });

  it("does not send a second Resume while one is out", async () => {
    let finish: () => void = () => {};
    const post = vi.fn(() => new Promise<unknown>((resolve) => (finish = () => resolve({}))));
    const first = resumeAgent("m1", "s1", post);
    expect(await resumeAgent("m1", "s1", post)).toBe(false);
    expect(post).toHaveBeenCalledTimes(1);
    finish();
    await first;
  });

  it("keeps the hub's refusal in words for both places that show it, and lets it be tried again", async () => {
    const post = vi.fn().mockRejectedValueOnce(new ApiError(503, "offline", { code: "node_unreachable", delivered: "none" })).mockResolvedValueOnce({});
    expect(await resumeAgent("m1", "s1", post)).toBe(false);
    const failed = resumeState("m1", "s1");
    expect(failed.phase).toBe("failed");
    expect(failed.phase === "failed" && failed.error).toBeTruthy();
    expect(await resumeAgent("m1", "s1", post)).toBe(true);
    expect(resumeState("m1", "s1").phase).toBe("done");
  });

  it("goes through the hub's start endpoint by default", async () => {
    vi.stubGlobal("localStorage", { getItem: () => "device-token" });
    const fetch = vi.fn().mockResolvedValue(new Response('{"session":{}}', { status: 200 }));
    vi.stubGlobal("fetch", fetch);
    expect(await resumeAgent("m1", "s1")).toBe(true);
    expect(fetch.mock.calls[0][0]).toBe("/api/v1/sessions");
    expect(fetch.mock.calls[0][1].method).toBe("POST");
    expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual({ machine_id: "m1", resume_session_id: "s1" });
  });
});
