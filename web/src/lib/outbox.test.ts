import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("./store", () => {
  const state = {
    outbox: {} as Record<string, import("./outbox").OutboxEntry[]>,
    assistant: null as null | { machine_id: string; main?: string; conversations: unknown[] },
    features: [] as string[], // a hub without start receipts: the start goes as it always did (lib/assistant-start.ts)
  };
  return {
    getState: () => state,
    setOutbox: (key: string, list: import("./outbox").OutboxEntry[]) => {
      if (list.length) state.outbox[key] = list;
      else delete state.outbox[key];
    },
    refreshAssistant: vi.fn(async () => {}),
  };
});
vi.mock("./router", () => ({ navigate: vi.fn() }));

import { acceptOutgoing, arrived, forgetOutgoing, askPersona, discardOutbox, inSendOrder, keepFailed, keepOutgoing, noteSent, noteWritten, outboxMessage, placeFailed, resetOutboxClaimsForTests, resetRestoredForTests, restoreOutbox, retryOutbox, sendToSession, settle, settleOutbox, SENT_GRACE_MS, type OutboxEntry } from "./outbox";
import type { ChatLine } from "./turns";
import { ApiError } from "./api";
import { pickLauncher } from "./assistant-chat";
import { navigate } from "./router";
import { getState } from "./store";

function response(body: unknown, status = 200) {
  return { ok: status < 400, status, text: async () => JSON.stringify(body) };
}
const outbox = () => getState().outbox as Record<string, OutboxEntry[]>;
const entry = (patch: Partial<OutboxEntry> = {}): OutboxEntry => ({ id: "e1", text: "你好", images: [], at: 1000, state: "sent", ...patch });

beforeEach(() => {
  vi.stubGlobal("localStorage", { getItem: () => "test" });
  for (const k of Object.keys(outbox())) delete outbox()[k];
  (getState() as { assistant: unknown }).assistant = null;
  vi.clearAllMocks();
});
afterEach(() => vi.unstubAllGlobals());

describe("which bubbles stay", () => {
  it("goes once the transcript says the same, with as many images", () => {
    expect(arrived(entry(), [{ text: "你好", images: [] }])).toBe(true);
    expect(arrived(entry({ images: [{ mime: "image/png", data: "x" }] }), [{ text: "你好", images: [] }])).toBe(false);
    expect(arrived(entry({ text: '<persona-context kind="context" title="t" url="">x</persona-context>\n\n说说' }), [{ text: "说说", images: [] }])).toBe(true);
  });

  it("keeps failed ones, and sent ones only for a while", () => {
    const users = [{ text: "别的", images: [] }];
    const list = [entry({ id: "a" }), entry({ id: "b", state: "failed" }), entry({ id: "c", state: "sending" }), entry({ id: "d", at: 1000 - SENT_GRACE_MS })];
    expect(settle(list, users, 1000).map((e) => e.id)).toEqual(["a", "b", "c"]);
    const copies = ["u1", "u2", "u3"].map((id) => ({ id, text: "你好", images: [] }));
    expect(settle(list, copies, 1000).map((e) => e.id)).toEqual(["b"]);
  });

  it("drops a failed one the hub tied a copy to (it went in after all), and only that one", () => {
    const list = [entry({ id: "a", state: "failed" }), entry({ id: "b", state: "failed" }), entry({ id: "w", state: "failed", wordsOnly: true })];
    expect(settle(list, [{ id: "u1", client_msg_id: "a", text: "改过的字", images: [] }], 1000).map((e) => e.id)).toEqual(["b", "w"]);
    expect(settle(list, [{ id: "u1", text: "你好", images: [] }], 1000).map((e) => e.id)).toEqual(["a", "b", "w"]); // words alone: not it
  });

  it("takes one bubble per copy, never an older message's copy, and a copy only once", () => {
    const list = [entry({ id: "a", at: 1_000_000 }), entry({ id: "b", at: 1_001_000 })];
    const older = { id: "u0", text: "你好", images: [], ts: new Date(1_000_000 - 600_000).toISOString() };
    const copy = { id: "u1", text: "你好", images: [], ts: new Date(1_000_500).toISOString() };
    expect(settle(list, [older], 1_002_000).map((e) => e.id)).toEqual(["a", "b"]);
    const claimed = new Set<string>();
    expect(settle(list, [older, copy], 1_002_000, claimed).map((e) => e.id)).toEqual(["b"]);
    expect(settle(list.slice(1), [older, copy], 1_003_000, claimed).map((e) => e.id)).toEqual(["b"]);
  });

  it("goes when the hub ties an item to its id, whatever the item says; a tie to another bubble leaves it", () => {
    expect(arrived(entry({ id: "a", text: "/compact" }), [{ id: "n1", client_msg_id: "a", text: "压缩上下文", images: [] }])).toBe(true);
    expect(arrived(entry({ id: "a" }), [{ id: "u1", client_msg_id: "b", text: "你好", images: [] }])).toBe(false);
    const list = [entry({ id: "a", text: "继续" }), entry({ id: "b", text: "继续", at: 2000 })];
    const claimed = new Set<string>();
    expect(settle(list, [{ id: "u2", client_msg_id: "b", text: "继续", images: [] }], 3000, claimed).map((e) => e.id)).toEqual(["a"]);
    expect(settle(list, [{ id: "u1", text: "继续", images: [] }], 3000, claimed).map((e) => e.id)).toEqual(["b"]);
  });

  it("finds the prompt that started a conversation by its words (it went in without the bubble's id)", () => {
    noteSent("m", "s", { text: "帮我看看" });
    settleOutbox("m", "s", [{ id: "u1", client_msg_id: "hub-made", text: "帮我看看", images: [] }]);
    expect(outbox()["m/s"]).toBeUndefined();
  });

  it("shows contexts as cards and images as images", () => {
    const m = outboxMessage(entry({ text: '<persona-context kind="context" title="项目" url="">档案</persona-context>\n\n问题', images: [{ mime: "image/png", data: "AQID" }] }));
    expect(m.text).toBe("问题");
    expect(m.contexts).toEqual([{ kind: "context", title: "项目", url: "", text: "档案" }]);
    expect(m.images[0]).toMatchObject({ kind: "image", role: "user", image: { mime: "image/png", data: "AQID" } });
  });
});

describe("sending from another page", () => {
  it("shows the message at once and marks it sent", async () => {
    let resolve!: (v: unknown) => void;
    const fetcher = vi.fn().mockReturnValueOnce(new Promise((r) => (resolve = r)));
    vi.stubGlobal("fetch", fetcher);
    const job = sendToSession("m", "s", { text: "看一下" }, { launcher: "codex" });
    expect(outbox()["m/s"]).toHaveLength(1);
    expect(outbox()["m/s"][0].state).toBe("sending");
    const body = JSON.parse(fetcher.mock.calls[0][1].body);
    expect(fetcher.mock.calls[0][0]).toBe("/api/v1/sessions/m/s/messages");
    expect(body).toMatchObject({ text: "看一下", launcher: "codex", client_msg_id: outbox()["m/s"][0].id });
    resolve(response({ ok: true }));
    expect(await job).toBe(true);
    expect(outbox()["m/s"][0].state).toBe("sent");
    settleOutbox("m", "s", [{ text: "看一下", images: [] }]);
    expect(outbox()["m/s"]).toBeUndefined();
  });

  it("keeps a failure with why, retries as another operation (the hub keeps its answer for the id), and can drop it", async () => {
    const fetcher = vi.fn()
      .mockResolvedValueOnce(response({ code: "input_draft", error: "draft", clearable: "old" }, 409))
      .mockResolvedValueOnce(response({ ok: true }));
    vi.stubGlobal("fetch", fetcher);
    expect(await sendToSession("m", "s", { text: "看一下" })).toBe(false);
    const failed = outbox()["m/s"][0];
    expect(failed).toMatchObject({ state: "failed", error: "电脑上的输入框里有没发出去的字", clearable: "old" });
    expect(await retryOutbox("m", "s", failed.id, failed.clearable)).toBe(true);
    const again = JSON.parse(fetcher.mock.calls[1][1].body);
    expect(again.client_msg_id).not.toBe(failed.id);
    expect(fetcher.mock.calls[1][1].headers.get("X-Persona-Op-Id")).toBe(again.client_msg_id);
    expect(again.clear_draft).toBe("old");
    discardOutbox("m", "s", again.client_msg_id);
    expect(outbox()["m/s"]).toBeUndefined();
  });

  it("retries as a new message when the hub could not tell whether it went in", async () => {
    const fetcher = vi.fn().mockResolvedValueOnce(response({ error: "timeout", delivered: "unknown" }, 504)).mockResolvedValueOnce(response({ ok: true }));
    vi.stubGlobal("fetch", fetcher);
    await sendToSession("m", "s", { text: "x" });
    const id = outbox()["m/s"][0].id;
    await retryOutbox("m", "s", id);
    expect(JSON.parse(fetcher.mock.calls[1][1].body).client_msg_id).not.toBe(id);
  });

  it("notes a prompt that started a conversation as sent", () => {
    noteSent("m", "new", { text: "开始", images: [{ mime: "image/png", data: "x" }] });
    expect(outbox()["m/new"][0]).toMatchObject({ state: "sent", text: "开始" });
  });
});

describe("问 Persona", () => {
  it("opens the main conversation at once and sends there, with its picked launcher", async () => {
    (getState() as { assistant: unknown }).assistant = { machine_id: "hub", main: "main1", conversations: [] };
    pickLauncher("hub/main1", "codex");
    const fetcher = vi.fn().mockResolvedValueOnce(response({ ok: true }));
    vi.stubGlobal("fetch", fetcher);
    await askPersona("说说进展");
    expect(navigate).toHaveBeenCalledWith({ name: "persona", sid: "main1" });
    expect(outbox()["hub/main1"][0].text).toBe("说说进展");
    expect(JSON.parse(fetcher.mock.calls[0][1].body)).toMatchObject({ text: "说说进展", launcher: "codex" });
    pickLauncher("hub/main1", undefined);
  });

  it("starts the main conversation when there is none, and shows the prompt there", async () => {
    const fetcher = vi.fn().mockResolvedValueOnce(response({ session: { machine_id: "hub", session_id: "new1" } }));
    vi.stubGlobal("fetch", fetcher);
    await askPersona("说说进展");
    expect(fetcher.mock.calls[0][0]).toBe("/api/v1/assistant/conversations");
    expect(JSON.parse(fetcher.mock.calls[0][1].body)).toEqual({ prompt: "说说进展", main: true });
    expect(outbox()["hub/new1"][0]).toMatchObject({ state: "sent", text: "说说进展" });
    expect(navigate).toHaveBeenCalledWith({ name: "persona", sid: "new1" });
  });
});

describe("where a failed message goes (#1122)", () => {
  // an exchange whose message was sent from here (`cmid`, written at `at`) or typed elsewhere (`ts` only)
  const line = (id: string, user: { cmid?: string; ts?: number }): ChatLine =>
    ({ kind: "segment", id, live: false, seg: { id, steps: [], tools: 0, thoughts: 0, replies: 0, folds: [], user: { id: `${id}u`, text: "x", images: [], contexts: [], client_msg_id: user.cmid, ts: user.ts === undefined ? undefined : new Date(user.ts).toISOString() } } }) as ChatLine;

  it("goes right before the first message written after it; with none after, and while sending, at the end", () => {
    noteWritten("later", 5_000_000);
    const lines = [line("l1", { ts: 1_000_000 }), line("l2", { cmid: "later" }), line("l3", { cmid: "later2" })];
    const failed = entry({ id: "f", state: "failed", at: 4_000_000 });
    const last = entry({ id: "g", state: "failed", at: 9_000_000 });
    const sending = entry({ id: "s", state: "sending", at: 2_000_000 });
    const { before, rest } = placeFailed(lines, [last, failed, sending]);
    expect([...before.entries()].map(([id, es]) => [id, es.map((e) => e.id)])).toEqual([["l2", ["f"]]]);
    expect(rest.map((e) => e.id)).toEqual(["s", "g"]);
  });

  it("goes by the computer's time for a message typed there, with two minutes' slack for its clock", () => {
    const failed = entry({ id: "f", state: "failed", at: 10_000_000 });
    expect(placeFailed([line("l1", { ts: 10_000_000 + 60_000 })], [failed]).rest.map((e) => e.id)).toEqual(["f"]);
    expect([...placeFailed([line("l1", { ts: 10_000_000 + 180_000 })], [failed]).before.keys()]).toEqual(["l1"]);
  });

  it("keeps failed real messages outside a quiet supervision wrapper at its original input time", () => {
    const quiet: ChatLine = { kind: "supervision", id: "quiet", live: false, lines: [line("child", { ts: 10_000_000 + 180_000 })] };
    const failed = entry({ id: "f", state: "failed", at: 10_000_000 });
    const placed = placeFailed([quiet], [failed]);
    expect([...placed.before.keys()]).toEqual(["quiet"]);
    expect(placed.rest).toEqual([]);
  });

  it("keepFailed puts the composer's message in the outbox as failed, with why", () => {
    keepFailed("m", "s", { id: "k1", text: "你好", images: [], at: 1000 }, new Error("断了"));
    expect(outbox()["m/s"]).toEqual([expect.objectContaining({ id: "k1", state: "failed", error: "断了", text: "你好" })]);
  });
});

describe("messages kept across a reload", () => {
  const store = new Map<string, string>();
  const fakeStorage = {
    get length() { return store.size; },
    key: (i: number) => [...store.keys()][i] ?? null,
    getItem: (k: string) => store.get(k) ?? null,
    setItem: (k: string, v: string) => void store.set(k, v),
    removeItem: (k: string) => void store.delete(k),
  };
  const reload = () => {
    delete outbox()["m/s"];
    resetOutboxClaimsForTests();
    resetRestoredForTests();
  };
  beforeEach(() => {
    store.clear();
    delete outbox()["m/s"];
    resetOutboxClaimsForTests();
    resetRestoredForTests();
    vi.stubGlobal("localStorage", fakeStorage);
  });

  it("comes back as a failed bubble, and Retry sends the same id", async () => {
    keepFailed("m", "s", { id: "a1", text: "还在吗", images: [], at: 1000 }, new TypeError("network"));
    expect(store.size).toBe(1);
    reload();
    restoreOutbox("m", "s");
    expect(outbox()["m/s"]).toMatchObject([{ id: "a1", text: "还在吗", state: "failed", restored: true }]);
    const fetcher = vi.fn().mockResolvedValueOnce(response({ ok: true }));
    vi.stubGlobal("fetch", fetcher);
    expect(await retryOutbox("m", "s", "a1")).toBe(true);
    expect(JSON.parse(fetcher.mock.calls[0][1].body).client_msg_id).toBe("a1");
    expect(store.size).toBe(1); // accepted, still waiting for its transcript copy
    settleOutbox("m", "s", [{ id: "accepted-retry", client_msg_id: "a1", text: "accepted", images: [] }]);
    expect(store.size).toBe(0);
  });

  it("restores an accepted message as pending with its original id, never resends, and settles once", async () => {
    const fetcher = vi.fn().mockResolvedValue(response({ ok: true }));
    vi.stubGlobal("fetch", fetcher);
    await sendToSession("m", "s", { text: "Is the museum open?" });
    const { id, at } = outbox()["m/s"][0];
    expect(store.size).toBe(1);
    reload();
    restoreOutbox("m", "s");
    restoreOutbox("m", "s");
    expect(outbox()["m/s"]).toMatchObject([{ id, at, state: "sent", restored: true, error: undefined }]);
    expect(await retryOutbox("m", "s", id)).toBe(false);
    expect(fetcher).toHaveBeenCalledTimes(1);
    settleOutbox("m", "s", [{ id: "accepted-copy", client_msg_id: id, text: "Is the museum open?", images: [] }]);
    expect(outbox()["m/s"]).toBeUndefined();
    expect(store.size).toBe(0);
    reload();
    restoreOutbox("m", "s");
    expect(outbox()["m/s"]).toBeUndefined();
  });

  it.each(["sending", "failed", undefined] as const)("restores another tab's %s entry only on the first visit per page", (state) => {
    restoreOutbox("m", "s"); // this page opened before the other tab started its send
    keepOutgoing("m/s", entry({ id: "other-tab", text: "Still in the other tab", state, at: Date.now() }));
    restoreOutbox("m", "s");
    settleOutbox("m", "s", [{ id: "unrelated-copy", text: "A different message", images: [] }]);
    restoreOutbox("m", "s");
    expect(outbox()["m/s"]).toBeUndefined();
    expect(store.has("outbox-msg:m/s/other-tab")).toBe(true);

    // Another conversation still gets its own first restoration.
    keepOutgoing("m/other", entry({ id: "first-visit", state, at: Date.now() }));
    restoreOutbox("m", "other");
    expect(outbox()["m/other"]).toMatchObject([{ id: "first-visit", state: "failed", restored: true }]);

    reload();
    restoreOutbox("m", "s");
    restoreOutbox("m", "s");
    expect(outbox()["m/s"]).toMatchObject([{ id: "other-tab", state: "failed", restored: true }]);
  });

  it("restores a newly accepted entry after the first visit and again on navigation without resending", async () => {
    const fetcher = vi.fn();
    vi.stubGlobal("fetch", fetcher);
    restoreOutbox("m", "s");
    keepOutgoing("m/s", entry({ id: "later-accepted", text: "Accepted in another tab", state: "sending", at: Date.now() }));
    restoreOutbox("m", "s");
    expect(outbox()["m/s"]).toBeUndefined();
    acceptOutgoing("m/s", "later-accepted");
    restoreOutbox("m", "s");
    expect(outbox()["m/s"]).toMatchObject([{ id: "later-accepted", state: "sent" }]);
    delete outbox()["m/s"]; // revisit in the same page: the first-visit guard stays set
    restoreOutbox("m", "s");
    expect(outbox()["m/s"]).toMatchObject([{ id: "later-accepted", state: "sent" }]);
    expect(await retryOutbox("m", "s", "later-accepted")).toBe(false);
    expect(fetcher).not.toHaveBeenCalled();
  });

  it("matches an untagged copy after a slow acceptance and reload using the original send time", () => {
    const at = Date.now() - 180_000;
    keepOutgoing("m/s", entry({ id: "slow", text: "A slow reply", at, state: "sending" }));
    acceptOutgoing("m/s", "slow");
    restoreOutbox("m", "s");
    expect(outbox()["m/s"]).toMatchObject([{ id: "slow", at, state: "sent" }]);
    settleOutbox("m", "s", [{ id: "slow-copy", text: "A slow reply", images: [], ts: new Date(at).toISOString() }]);
    expect(outbox()["m/s"]).toBeUndefined();
    expect(store.size).toBe(0);
  });

  it("expires accepted copies without extending their grace on reload, leaving unconfirmed sends alone", () => {
    const clock = vi.spyOn(Date, "now").mockReturnValue(1000);
    try {
      keepOutgoing("m/s", entry({ id: "accepted", state: "sending" }));
      acceptOutgoing("m/s", "accepted");
      keepOutgoing("m/s", entry({ id: "unconfirmed", state: "sending" }));
      clock.mockReturnValue(1000 + SENT_GRACE_MS - 1);
      restoreOutbox("m", "s");
      expect(outbox()["m/s"]).toMatchObject([{ id: "accepted", at: 1000, state: "sent" }, { id: "unconfirmed", state: "failed" }]);
      reload();
      clock.mockReturnValue(1000 + SENT_GRACE_MS);
      restoreOutbox("m", "s");
      expect(outbox()["m/s"]).toMatchObject([{ id: "unconfirmed", state: "failed" }]);
      expect(store.has("outbox-msg:m/s/accepted")).toBe(false);
      expect(store.has("outbox-msg:m/s/unconfirmed")).toBe(true);
    } finally {
      clock.mockRestore();
    }
  });

  it("does not revive an echo settled before acceptance or overwrite another tab's cleanup", () => {
    keepOutgoing("m/s", entry({ id: "early", state: "sending" }));
    restoreOutbox("m", "s");
    settleOutbox("m", "s", [{ id: "early-copy", client_msg_id: "early", text: "copy", images: [] }]);
    acceptOutgoing("m/s", "early");
    expect(store.size).toBe(0);

    noteSent("m", "s", { text: "Accepted in this tab" });
    const accepted = outbox()["m/s"][0];
    forgetOutgoing("m/s", accepted.id); // another tab saw the transcript first
    keepFailed("m", "s", { id: "failed", text: "Another message", images: [], at: Date.now() }, new TypeError("network"));
    expect(store.has(`outbox-msg:m/s/${accepted.id}`)).toBe(false);
    expect(store.has("outbox-msg:m/s/failed")).toBe(true);
  });

  it("keeps sessions separate and restores prompts that must match by words", () => {
    const at = Date.now();
    keepOutgoing("m/other", entry({ id: "same", at, text: "Other conversation" }));
    keepOutgoing("m/s", entry({ id: "same", at, text: "This conversation" }));
    restoreOutbox("m", "s");
    settleOutbox("m", "s", [{ id: "session-copy", client_msg_id: "same", text: "This conversation", images: [] }]);
    expect(store.has("outbox-msg:m/other/same")).toBe(true);
    expect(store.has("outbox-msg:m/s/same")).toBe(false);

    noteSent("m", "s", { text: "Start a conversation" });
    reload();
    restoreOutbox("m", "s");
    expect(outbox()["m/s"]).toMatchObject([{ state: "sent", wordsOnly: true }]);
    settleOutbox("m", "s", [{ id: "start-copy", client_msg_id: "hub-generated", text: "Start a conversation", images: [] }]);
    expect(outbox()["m/s"]).toBeUndefined();
    expect(store.size).toBe(1); // only the other conversation
  });

  it("a message the hub answered keeps that reason, and its new id on Retry", () => {
    keepFailed("m", "s", { id: "a6", text: "到了吗", images: [], at: 1000 }, new ApiError(502, "x", { delivered: "unknown" }));
    const reason = outbox()["m/s"][0].error;
    reload();
    restoreOutbox("m", "s");
    expect(outbox()["m/s"]).toMatchObject([{ id: "a6", error: reason, newId: true }]);
  });

  it("a message still sending when the page closed comes back too", () => {
    keepOutgoing("m/s", { id: "a2", text: "发出去了吗", images: [], at: 2000 });
    restoreOutbox("m", "s");
    expect(outbox()["m/s"]).toMatchObject([{ id: "a2", state: "failed", restored: true }]);
  });

  it("does not show twice when the transcript has it, by its id or by its words", () => {
    keepOutgoing("m/s", { id: "a3", text: "第一条消息内容", images: [], at: 2000 });
    keepOutgoing("m/s", { id: "a4", text: "第二条消息内容", images: [], at: 2000 });
    restoreOutbox("m", "s");
    settleOutbox("m", "s", [
      { id: "u1", client_msg_id: "a3", text: "第一条消息内容", images: [] },
      { id: "u2", text: "第二条消息内容", images: [], ts: new Date(3000).toISOString() },
    ]);
    expect(outbox()["m/s"]).toBeUndefined();
    expect(store.size).toBe(0);
  });

  it("forgets a deleted message", () => {
    keepFailed("m", "s", { id: "a5", text: "别发了", images: [], at: 1000 }, new TypeError("network"));
    discardOutbox("m", "s", "a5");
    expect(store.size).toBe(0);
  });

  it("puts a deleted message back where it was on Undo, kept for reloads again (#4626)", () => {
    keepFailed("m", "s", { id: "a6", text: "第一条", images: [], at: 1000 }, new TypeError("network"));
    keepFailed("m", "s", { id: "a7", text: "第二条", images: [], at: 2000 }, new TypeError("network"));
    const undo = discardOutbox("m", "s", "a6")!;
    expect(outbox()["m/s"].map((e) => e.id)).toEqual(["a7"]);
    expect(store.size).toBe(1);
    undo();
    expect(outbox()["m/s"].map((e) => e.id)).toEqual(["a6", "a7"]);
    expect(outbox()["m/s"][0]).toMatchObject({ state: "failed", text: "第一条" });
    expect(store.size).toBe(2);
    undo(); // there already: no second copy
    expect(outbox()["m/s"]).toHaveLength(2);
    expect(discardOutbox("m", "s", "gone")).toBeUndefined();
  });
});

describe("inSendOrder", () => {
  it("stops waiting for a POST that hung", async () => {
    vi.useFakeTimers();
    try {
      void inSendOrder("hung", () => new Promise(() => {}));
      const next = vi.fn(async () => {});
      void inSendOrder("hung", next);
      await vi.advanceTimersByTimeAsync(5 * 60_000);
      expect(next).not.toHaveBeenCalled();
      await vi.advanceTimersByTimeAsync(60_000);
      expect(next).toHaveBeenCalledTimes(1);
    } finally {
      vi.useRealTimers();
    }
  });

  it("runs a conversation's sends one at a time, past a failure", async () => {
    let fail!: (e: Error) => void;
    const first = inSendOrder("k", () => new Promise((_, reject) => (fail = reject)));
    const second = vi.fn(async () => "two");
    const after = inSendOrder("k", second);
    await Promise.resolve();
    expect(second).not.toHaveBeenCalled();
    fail(new Error("no"));
    await expect(first).rejects.toThrow("no");
    await expect(after).resolves.toBe("two");
  });
});
