import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const hub = vi.hoisted(() => ({ features: [] as string[] }));
vi.mock("./store", () => ({ getState: () => ({ features: hub.features }), isOpen: () => true, onHubEvent: () => () => {} }));
const shown = vi.hoisted(() => ({ toasts: [] as unknown[][], navigate: [] as string[] }));
vi.mock("../ui/atoms", () => ({ toast: (...a: unknown[]) => shown.toasts.push(a) }));
vi.mock("./router", () => ({ navigate: (to: string) => shown.navigate.push(to) }));

const { startConversation, START_OPS_FEATURE } = await import("./assistant-start");
const { OP_HEADER } = await import("./ops");

const OP = "0b6c1c9e-4f1a-4c3b-9d2e-7a5f8e1d2c3b";
const SESSION = { machine_id: "m1", session_id: "s1" };

function response(body: unknown, status = 200) {
  return { ok: status < 400, status, text: async () => JSON.stringify(body) };
}

/** The op id each POST went with ("" none). */
function opsOf(fetch: ReturnType<typeof vi.fn>): string[] {
  return fetch.mock.calls.map(([, init]) => new Headers((init as RequestInit).headers).get(OP_HEADER) ?? "");
}

beforeEach(() => {
  vi.useFakeTimers();
  vi.stubGlobal("localStorage", { getItem: () => "tok" });
});
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  hub.features = [];
});

describe("starting a conversation (#1382)", () => {
  it("a hub with start receipts: one op id, and a try lost on the way goes again with it", async () => {
    hub.features = [START_OPS_FEATURE];
    const fetch = vi.fn().mockRejectedValueOnce(new TypeError("Failed to fetch")).mockResolvedValueOnce(response({ session: SESSION }));
    vi.stubGlobal("fetch", fetch);
    const started = startConversation({ prompt: "Plan a weekend trip.", main: true }, undefined, OP);
    await vi.advanceTimersByTimeAsync(1_000);
    await expect(started).resolves.toEqual({ session: SESSION });
    expect(opsOf(fetch)).toEqual([OP, OP]);
    expect(fetch.mock.calls.every(([url, init]) => String(url).endsWith("/api/v1/assistant/conversations") && (init as RequestInit).method === "POST")).toBe(true);
  });

  it("each start without one given gets an op id of its own", async () => {
    hub.features = [START_OPS_FEATURE];
    const fetch = vi.fn().mockImplementation(async () => response({ session: SESSION }));
    vi.stubGlobal("fetch", fetch);
    await startConversation({ prompt: "one" });
    await startConversation({ prompt: "two" });
    const [a, b] = opsOf(fetch);
    expect(a).not.toBe("");
    expect(b).not.toBe("");
    expect(a).not.toBe(b);
  });

  it("a refusal is the answer: not tried again", async () => {
    hub.features = [START_OPS_FEATURE];
    const fetch = vi.fn().mockResolvedValue(response({ error: "not installed", code: "launcher_missing" }, 409));
    vi.stubGlobal("fetch", fetch);
    await expect(startConversation({ prompt: "x" }, undefined, OP)).rejects.toMatchObject({ status: 409 });
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it("an older hub: no op id and no retry, which would start a second conversation", async () => {
    const fetch = vi.fn().mockRejectedValue(new TypeError("Failed to fetch"));
    vi.stubGlobal("fetch", fetch);
    await expect(startConversation({ prompt: "x" }, undefined, OP)).rejects.toBeInstanceOf(TypeError);
    expect(opsOf(fetch)).toEqual([""]);
  });
});

describe("the main chat on another launcher (#4659)", () => {
  it("says so once per conversation, with the way to Model configuration", async () => {
    const note = "wrap can't run assistant chats on box, so this runs on claude";
    vi.stubGlobal("fetch", vi.fn().mockImplementation(async () => response({ session: SESSION, launcher_note: note })));
    await startConversation({ prompt: "one", main: true });
    await startConversation({ prompt: "two", main: true });
    expect(shown.toasts.length).toBe(1);
    const [text, tone, action] = shown.toasts[0] as [string, string, { run: () => void }];
    expect([text, tone]).toEqual([note, "info"]);
    action.run();
    expect(shown.navigate).toEqual(["/settings?tab=models"]);
  });
});
