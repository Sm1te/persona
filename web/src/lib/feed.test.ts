import { afterEach, describe, expect, it, vi } from "vitest";
import {
  answeredItems,
  cardFiled,
  cardSent,
  dayLabel,
  displayLines,
  draftPlaceholder,
  field,
  loadedFrom,
  getFeedState,
  loadFeed,
  mergeInto,
  quickResult,
  receiptState,
  removeTask,
  retryKey,
  retryTask,
  sendMessage,
  sendRouted,
  setRoute,
  startFailure,
  type FeedItem,
  type TaskPlan,
} from "./feed";
import { conversationCards } from "./assistant-views";
import { refreshTodos } from "./store";
import type { Todo } from "./todos";
import type { ChatLine, Segment } from "./turns";
import type { Item } from "./types";

const ago = (min: number) => new Date(Date.now() - min * 60_000).toISOString();

function seg(id: string, userMin: number, steps: Segment["steps"] = []): ChatLine {
  const s = { id, user: { id: id + "u", text: "m", images: [], ts: ago(userMin), contexts: [] }, steps, start: ago(userMin), tools: 0, thoughts: 0, replies: 0, folds: [] } as Segment;
  return { kind: "segment", id, seg: s, live: false };
}

const feed = (id: string, min: number, kind: FeedItem["kind"] = "task"): FeedItem => ({ id, conversation: "c1", at: ago(min), kind, ref: "t" });

// Minutes ago of noon `n` days back, `m` minutes later: whatever the time now, never across midnight.
const noon = (n: number, m = 0) => {
  const d = new Date();
  d.setDate(d.getDate() - n);
  d.setHours(12, m, 0, 0);
  return (Date.now() - d.getTime()) / 60_000;
};

describe("the assistant's feed among the chat's lines (docs/assistant/unified-entry.md)", () => {
  afterEach(() => vi.unstubAllGlobals());
  it("retains task-card upserts across partial and empty HTTP pages", async () => {
    const at = "2026-11-10T08:00:00Z";
    const conversation = "c-card-upserts";
    const card = { id: "plan:metadata:0", plan_id: "metadata", index: 0, at, updated_at: at, state: "not_started" };
    const pages = [
      { items: [], task_cards: [card] },
      { items: [], task_cards: [{ ...card, todo_id: "metadata-todo", state: "working", updated_at: "2026-11-10T08:02:00Z" }] },
      { items: [], task_cards: [] },
      { items: [], task_cards: null },
      { items: [] },
    ];
    vi.stubGlobal("localStorage", { getItem: () => "jd_test" });
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify(pages.shift()), { headers: { "Content-Type": "application/json" } })));
    for (let i = 0; i < 5; i++) await loadFeed(conversation);
    expect(getFeedState().taskCards[conversation]).toEqual([{ ...card, todo_id: "metadata-todo", state: "working", updated_at: "2026-11-10T08:02:00Z" }]);
  });
  it("keeps one copy of each item, oldest first", () => {
    const a = feed("a", 5), b = feed("b", 10);
    expect(mergeInto([a], [b, a]).map((i) => i.id)).toEqual(["b", "a"]);
  });

  it("puts an item at the end of the exchange it happened in, the rest last, with a day line per day", () => {
    // Two days apart at noon: minutes before now crossed midnight between s2 and mid from 00:20 to 00:30 (#3290).
    const lines = [seg("s1", noon(2)), seg("s2", noon(1))];
    const out = displayLines(lines, [feed("early", noon(2, 1)), feed("mid", noon(1, 10))]);
    const ids = out.map((l) => l.id);
    expect(ids.filter((i) => i.startsWith("day-"))).toHaveLength(2);
    expect(ids.filter((i) => !i.startsWith("day-"))).toEqual(["s1", "feed-early", "s2", "feed-mid"]);
  });

  it("holds back feed items older than the loaded page until that page loads, so the newest page never reads as the start", () => {
    // The main conversation shows its newest page; the feed holds the whole history. Days-old cards and their day lines
    // used to stack on top of that page, as if the conversation began there with its messages gone.
    const lines = [seg("s1", noon(1, 10)), seg("s2", noon(1, 30))];
    const all = [feed("days-ago", noon(3)), feed("before-page", noon(1)), feed("in-page", noon(1, 20))];
    const partial = displayLines(lines, all, Date.parse(ago(noon(1, 10))));
    expect(partial.filter((l) => l.kind !== "day").map((l) => l.id)).toEqual(["s1", "feed-in-page", "s2"]);
    expect(partial.filter((l) => l.kind === "day")).toHaveLength(1);
    const complete = displayLines(lines, all);
    expect(complete.filter((l) => l.kind !== "day").map((l) => l.id)).toEqual(["feed-days-ago", "feed-before-page", "s1", "feed-in-page", "s2"]);
  });

  it("dates the loaded page by its oldest item that has a time", () => {
    const item = (seq: number, ts?: string) => ({ id: `i${seq}#0`, seq, role: "assistant", kind: "text", ts }) as Item;
    expect(loadedFrom([item(1), item(2, "2026-11-11T07:58:14Z"), item(3, "2026-11-11T08:00:00Z")])).toBe(Date.parse("2026-11-11T07:58:14Z"));
    expect(loadedFrom([item(1)])).toBeUndefined();
  });

  it("draws a reminder or an event the phone added after its exchange, not a read or a failure", () => {
    const use = (name: string, id: string) => ({ id, seq: 1, role: "assistant", kind: "tool_use", tool_name: name }) as Item;
    const res = (text: string, err = false) => ({ id: "r", seq: 2, role: "user", kind: "tool_result", text, is_error: err }) as Item;
    const ok = quickResult(use("mcp__persona__phone_reminders_create", "u1"), res(JSON.stringify({ id: "R1", title: "打电话", due: "2026-11-07T10:00:00+08:00", list: "提醒事项" })));
    expect(ok).toMatchObject({ kind: "reminder", title: "打电话", place: "提醒事项" });
    expect(quickResult(use("mcp__persona__phone_reminders_list", "u2"), res(JSON.stringify({ id: "x" })))).toBeUndefined();
    expect(quickResult(use("phone_calendar_create_event", "u3"), res("denied", true))).toBeUndefined();
    const step = { kind: "tool" as const, id: "u1", use: use("phone_reminders_create", "u1"), result: res(JSON.stringify({ id: "R1", title: "t" })), images: [] };
    const out = displayLines([seg("s1", 5, [step])], []).map((l) => l.id);
    expect(out.filter((i) => !i.startsWith("day-"))).toEqual(["s1", "quick-u1"]);
  });

  it("never puts a day line back in time, nor the same one twice, when the lines are out of order (#1996)", () => {
    // yesterday, then a line from two days ago (a clock that was behind), then yesterday again, then a day later
    const lines = [seg("s1", noon(2)), seg("s2", noon(3)), seg("s3", noon(2, 10)), seg("s4", noon(1))];
    const out = displayLines(lines, []);
    const days = out.filter((l) => l.kind === "day");
    expect(new Set(days.map((d) => d.id)).size).toBe(days.length);
    const dates = days.map((d) => (d.kind === "day" ? d.date.getTime() : 0));
    expect(dates).toEqual([...dates].sort((a, b) => a - b));
    expect(days).toHaveLength(2);
    expect(out.map((l) => l.id).filter((i) => !i.startsWith("day-"))).toEqual(["s1", "s2", "s3", "s4"]);
  });

  it("names today and yesterday", () => {
    const now = new Date();
    expect(dayLabel(now, now)).toMatch(/^今天 · /);
    const y = new Date(now);
    y.setDate(now.getDate() - 1);
    expect(dayLabel(y, now)).toMatch(/^昨天 · /);
  });
});

describe("a task whose agent did not start (#1077, #1142)", () => {
  const todo = (patch: Partial<Todo> = {}): Todo => ({
    id: "t1", title: "设置页按钮对齐", created_at: "2026-11-04T10:00:00Z", updated_at: "2026-11-04T10:00:00Z", origin: "c1", plan_id: "p1", agents: [], ...patch,
  });
  const plans: Record<string, TaskPlan> = {
    p1: { id: "p1", conversation: "c1", state: "started", tasks: [{ title: "设置页按钮对齐", prompt: "", machine: "box", cwd: "/code/app", todo_id: "t1", error: "rpc: connection closed" }] },
  };

  it("says why from the todo, else from its plan while it has no agent", () => {
    expect(startFailure(todo({ start_error: "launcher not found" }), {})).toBe("launcher not found");
    expect(startFailure(todo(), plans)).toBe("rpc: connection closed"); // a hub before #1077 left the todo open
    expect(startFailure(todo({ agents: [{ machine_id: "box", session_id: "s1", linked_at: "" }] }), plans)).toBeUndefined();
    expect(startFailure(todo(), {})).toBeUndefined(); // may still be starting
  });

  describe("removing it", () => {
    const ok = (body: unknown) => ({ ok: true, status: 200, text: async () => JSON.stringify(body) });
    afterEach(() => vi.unstubAllGlobals());
    const stub = () => {
      const fetcher = vi.fn().mockResolvedValue(ok({ ok: true, todos: [] }));
      vi.stubGlobal("localStorage", { getItem: () => "jd_test" });
      vi.stubGlobal("fetch", fetcher);
      return fetcher;
    };

    it("deletes a task that never had an agent at once, and drops its route", async () => {
      const fetcher = stub();
      setRoute("c1", "t1");
      const ask = vi.fn((_message: string) => true);
      expect(await removeTask(todo({ start_error: "x", done_at: "2026-11-04T10:00:05Z" }), ask)).toBe(true);
      expect(ask).not.toHaveBeenCalled();
      expect(fetcher.mock.calls.some(([url, init]) => String(url).endsWith("/todos/t1") && init?.method === "DELETE")).toBe(true);
      expect(getFeedState().routes.c1).toBeUndefined();
    });

    it("retries it in place: one POST however many taps, the todo the hub answers put in at once (#1396)", async () => {
      let answer: (v: unknown) => void = () => {};
      const fetcher = vi.fn((_url: string, init?: RequestInit) =>
        init?.method === "POST" ? new Promise((r) => (answer = r)) : Promise.resolve(ok({ todos: [] })),
      );
      vi.stubGlobal("localStorage", { getItem: () => "jd_test" });
      vi.stubGlobal("fetch", fetcher);
      const failed = todo({ start_error: "claude did not become ready within 1m0s", done_at: "2026-11-04T10:00:05Z" });
      const first = retryTask(failed);
      const second = retryTask(failed);
      expect(getFeedState().busy[retryKey("t1")]).toBe(true);
      answer(ok({ todo: { ...failed, start_error: "", done_at: null } }));
      await Promise.all([first, second]);
      const posts = fetcher.mock.calls.filter(([, init]) => init?.method === "POST");
      expect(posts).toHaveLength(1);
      expect(String(posts[0][0])).toMatch(/\/todos\/t1\/retry$/);
      expect(getFeedState().busy[retryKey("t1")]).toBe(false);
    });

    it("says why when the hub refuses the retry, and can be tapped again", async () => {
      vi.stubGlobal("localStorage", { getItem: () => "jd_test" });
      vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: false, status: 409, text: async () => JSON.stringify({ error: "「公司 Mac」现在离线，等它连上再重试" }) }));
      await expect(retryTask(todo({ start_error: "x", done_at: "2026-11-04T10:00:05Z" }))).rejects.toThrow("离线");
      expect(getFeedState().busy[retryKey("t1")]).toBe(false);
    });

    it("asks first when its agents ran, and keeps it when the user says no", async () => {
      const fetcher = stub();
      const ran = todo({ agents: [{ machine_id: "box", session_id: "s1", linked_at: "" }] });
      expect(await removeTask(ran, () => false)).toBe(false);
      expect(fetcher).not.toHaveBeenCalled();
      const ask = vi.fn((_message: string) => true);
      expect(await removeTask(ran, ask)).toBe(true);
      expect(ask.mock.calls[0][0]).toContain("Agents 页");
      expect(fetcher.mock.calls.some(([, init]) => init?.method === "DELETE")).toBe(true);
    });
  });
});

describe("routing the next message to a task", () => {
  const ok = () => ({ ok: true, status: 200, text: async () => "{}" });
  afterEach(() => vi.unstubAllGlobals());
  // A route is only ever set from a task's card, so the task is in the store.
  const tasks = async (...ids: string[]) => {
    const todos = ids.map((id) => ({ id, title: id, created_at: ago(1), updated_at: ago(1), origin: "c1", agents: [] }));
    vi.stubGlobal("fetch", vi.fn().mockResolvedValueOnce({ ok: true, status: 200, text: async () => JSON.stringify({ todos }) }));
    vi.stubGlobal("localStorage", { getItem: () => "test" });
    await refreshTodos();
  };

  it("keeps the route after a 503 so the retry still goes to the task", async () => {
    const fetcher = vi.fn()
      .mockResolvedValueOnce({ ok: false, status: 503, text: async () => JSON.stringify({ error: "temporarily unavailable" }) })
      .mockResolvedValueOnce(ok());
    await tasks("task-one");
    vi.stubGlobal("fetch", fetcher);
    setRoute("route-retry", "task-one");

    await expect(sendRouted("route-retry", "继续")).rejects.toThrow("temporarily unavailable");
    expect(getFeedState().routes["route-retry"]).toBe("task-one");
    expect(await sendRouted("route-retry", "继续")).toBe(true);
    expect(getFeedState().routes["route-retry"]).toBeUndefined();
    expect(fetcher.mock.calls.map(([url, init]) => [url, JSON.parse(init.body)])).toEqual([
      ["/api/v1/assistant/conversations/route-retry/route", { todo_id: "task-one", text: "继续" }],
      ["/api/v1/assistant/conversations/route-retry/route", { todo_id: "task-one", text: "继续" }],
    ]);
  });

  it("keeps a different route chosen while the previous message is being sent", async () => {
    let answer!: (value: ReturnType<typeof ok>) => void;
    const fetcher = vi.fn().mockReturnValueOnce(new Promise<ReturnType<typeof ok>>((resolve) => { answer = resolve; }));
    await tasks("task-one", "task-two");
    vi.stubGlobal("fetch", fetcher);
    setRoute("route-switch", "task-one");

    const sending = sendRouted("route-switch", "继续");
    setRoute("route-switch", "task-two");
    answer(ok());
    expect(await sending).toBe(true);
    expect(getFeedState().routes["route-switch"]).toBe("task-two");
    expect(JSON.parse(fetcher.mock.calls[0][1].body)).toEqual({ todo_id: "task-one", text: "继续" });
    setRoute("route-switch", null);
  });

  it("gives the conversation back to Persona once the kept task is gone", async () => {
    const fetcher = vi.fn()
      .mockResolvedValueOnce({ ok: false, status: 409, text: async () => JSON.stringify({ error: "这个任务没有在跑的 Agent" }) })
      .mockResolvedValueOnce({ ok: true, status: 200, text: async () => JSON.stringify({ todos: [] }) });
    await tasks("task-gone");
    vi.stubGlobal("fetch", fetcher);
    setRoute("route-gone", "task-gone");

    await expect(sendRouted("route-gone", "继续")).rejects.toThrow("这个任务没有在跑的 Agent");
    await refreshTodos(); // removed elsewhere: the hub's list no longer has it, so its chip is not drawn
    expect(await sendRouted("route-gone", "继续")).toBe(false);
    expect(getFeedState().routes["route-gone"]).toBeUndefined();
    expect(fetcher).toHaveBeenCalledTimes(2);
  });
});

describe("an answered record (#1231)", () => {
  const answered = (body: Record<string, unknown>): FeedItem => ({ id: "a", conversation: "c1", at: ago(1), kind: "answered", ref: "t", body });

  it("has every question with its answer in full, newlines kept", () => {
    const it = answered({
      question: "先补哪一块？",
      answer: "重试；假服务器",
      items: [{ question: "先补哪一块？", answer: "重试\n和超时" }, { question: "请求怎么处理？", answer: "假服务器" }, { question: "", answer: "" }, "x"],
    });
    expect(answeredItems(it)).toEqual([
      { question: "先补哪一块？", answer: "重试\n和超时" },
      { question: "请求怎么处理？", answer: "假服务器" },
    ]);
  });

  it("shows a record from before it by its line, and nothing for an empty one", () => {
    expect(answeredItems(answered({ question: "使用 Bash", answer: "批准了" }))).toEqual([{ question: "使用 Bash", answer: "批准了" }]);
    expect(answeredItems(answered({ items: [] }))).toEqual([]);
  });
});

describe("a message card's 发送 (#1519)", () => {
  const ok = (body: unknown) => ({ ok: true, status: 200, text: async () => JSON.stringify(body) });
  afterEach(() => vi.unstubAllGlobals());
  const card = (id: string, body: Record<string, unknown> = {}): FeedItem => ({
    id, conversation: "c1", at: ago(5), kind: "message", ref: "example:1", body: { draft: "在修了", ...body },
  });

  it("reads sent from the card the hub keeps, with what went out (the draft for a card from before)", () => {
    expect(cardSent(card("m"))).toBeUndefined();
    expect(cardSent(card("m", { sent_at: "2026-11-04T10:23:00Z", sent: "修好了" }))).toEqual({ at: "2026-11-04T10:23:00Z", text: "修好了" });
    expect(cardSent(card("m", { sent_at: "2026-11-04T10:23:00Z" }))?.text).toBe("在修了");
  });

  it("reads filed from the hub's filed_at only while the card is not sent (#4797)", () => {
    expect(cardFiled(card("m"))).toBeUndefined();
    expect(cardFiled(card("m", { filed_at: "2026-11-05T09:00:00Z" }))).toEqual({ at: "2026-11-05T09:00:00Z" });
    expect(cardFiled(card("m", { filed_at: "2026-11-05T09:00:00Z", sent_at: "2026-11-04T10:23:00Z" }))).toBeUndefined();
  });

  it("takes a card's new body when the hub sends it again (sent on another device)", () => {
    const before = card("m");
    const list = [before];
    expect(mergeInto(list, [card("m")])).toBe(list); // the same: nothing redraws
    const sent = card("m", { sent_at: "2026-11-04T10:23:00Z" });
    expect(mergeInto(list, [sent])).toEqual([sent]);
  });

  it("finds a blank left to fill in", () => {
    expect(draftPlaceholder("确实是 bug，【预计今晚 / 几点前】修好")).toBe("【预计今晚 / 几点前】");
    expect(draftPlaceholder("MR 在 {{链接}}")).toBe("{{链接}}");
    expect(draftPlaceholder("确实是 bug，修好了告诉你")).toBeUndefined();
  });

  it("sends once however many taps, with the card, and draws it sent", async () => {
    let answer: (v: unknown) => void = () => {};
    const fetcher = vi.fn((_url: string, init?: RequestInit) => (init?.method === "POST" ? new Promise((r) => (answer = r)) : Promise.resolve(ok({}))));
    vi.stubGlobal("localStorage", { getItem: () => "jd_test" });
    vi.stubGlobal("fetch", fetcher);
    const c = card("m1");
    const first = sendMessage(c, "修好了");
    const second = sendMessage(c, "修好了");
    expect(getFeedState().busy.m1).toBe(true);
    answer(ok({ id: "example:1", done_by: "replied" }));
    await Promise.all([first, second]);
    const posts = fetcher.mock.calls.filter(([, init]) => init?.method === "POST");
    expect(posts).toHaveLength(1);
    expect(String(posts[0][0])).toMatch(/\/inbox\/example:1\/reply$/);
    expect(JSON.parse(String(posts[0][1]?.body))).toEqual({ text: "修好了", card: "m1" });
    const now = getFeedState().feeds.c1?.find((i) => i.id === "m1");
    expect(now && cardSent(now)?.text).toBe("修好了");
    // Sent: a tap from a stale copy does nothing.
    await sendMessage(c, "修好了");
    expect(fetcher.mock.calls.filter(([, init]) => init?.method === "POST")).toHaveLength(1);
  });

  it("sends nothing for a card whose thing was handled elsewhere (#5095)", async () => {
    const fetcher = vi.fn().mockResolvedValue(ok({}));
    vi.stubGlobal("localStorage", { getItem: () => "jd_test" });
    vi.stubGlobal("fetch", fetcher);
    await sendMessage(card("m3", { filed_at: "2026-11-05T09:00:00Z" }), "修好了");
    expect(fetcher).not.toHaveBeenCalled();
    expect(getFeedState().busy.m3).toBeFalsy();
  });

  it("does not send a draft with a blank, and can be tapped again after a failure", async () => {
    const fetcher = vi.fn().mockResolvedValue({ ok: false, status: 502, text: async () => JSON.stringify({ error: "github: rate limited" }) });
    vi.stubGlobal("localStorage", { getItem: () => "jd_test" });
    vi.stubGlobal("fetch", fetcher);
    await expect(sendMessage(card("m2"), "【预计今晚 / 几点前】修好")).rejects.toThrow("【预计今晚 / 几点前】");
    expect(fetcher).not.toHaveBeenCalled();
    await expect(sendMessage(card("m2"), "今晚修好")).rejects.toThrow("rate limited");
    expect(getFeedState().busy.m2).toBe(false);
    expect(getFeedState().feeds.c1?.find((i) => i.id === "m2")).toBeUndefined();
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(ok({})));
    await sendMessage(card("m2"), "今晚修好");
    expect(cardSent(getFeedState().feeds.c1!.find((i) => i.id === "m2")!)?.text).toBe("今晚修好");
  });
});

describe("a receipt's outcome (#1516)", () => {
  const receipt = (body: Record<string, unknown>): FeedItem => ({ id: "r", conversation: "c1", at: ago(1), kind: "receipt", ref: "t", body });
  it("interrupted when the hub says so, or when an older hub's done receipt has the API error as its summary", () => {
    expect(receiptState(receipt({ state: "interrupted", summary: "模型服务出错，中断了", error: "API Error: 529 Overloaded" }))).toBe("interrupted");
    expect(receiptState(receipt({ state: "done", summary: "API Error: Connection lost mid-response. The response above may be incomplete." }))).toBe("interrupted");
    expect(receiptState(receipt({ state: "done", summary: "修好了 API Error: Connection lost 时的崩溃" }))).toBe("done");
    expect(receiptState(receipt({ state: "failed", summary: "没开起来：x" }))).toBe("failed");
    expect(receiptState(receipt({}))).toBe("done");
  });

  it("a line from a clock ahead does not hide the day lines after it (#1996)", () => {
    const noon = (n: number) => {
      const d = new Date();
      d.setDate(d.getDate() - n);
      d.setHours(12, 0, 0, 0);
      return (Date.now() - d.getTime()) / 60_000;
    };
    // three days ago, then one from a clock years ahead, then two days ago, then yesterday
    const out = displayLines([seg("s1", noon(3)), seg("s2", -5 * 365 * 24 * 60), seg("s3", noon(2)), seg("s4", noon(1))], []);
    const days = out.filter((l) => l.kind === "day");
    expect(days).toHaveLength(3);
    const dates = days.map((d) => (d.kind === "day" ? d.date.getTime() : 0));
    expect(dates).toEqual([...dates].sort((a, b) => a - b));
  });
});

describe("loading a conversation's feed (docs/protocol/protocol.md: since, oldest first, at most 500)", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("reads every page, not just the oldest 500", async () => {
    const t0 = Date.parse("2026-01-01T00:00:00Z");
    const all: FeedItem[] = Array.from({ length: 1201 }, (_, i) => ({ id: `f${String(i).padStart(4, "0")}`, conversation: "c-pages", at: new Date(t0 + i * 1000).toISOString(), kind: "routed", ref: "r" }));
    // the hub's page: after since, oldest first, at most 500
    const fetcher = vi.fn(async (url: string) => {
      const since = new URL(url, "http://hub").searchParams.get("since");
      const items = all.filter((it) => !since || Date.parse(it.at) > Date.parse(since)).slice(0, 500);
      return { ok: true, status: 200, text: async () => JSON.stringify({ items }) };
    });
    vi.stubGlobal("localStorage", { getItem: () => "jd_test" });
    vi.stubGlobal("fetch", fetcher);
    await loadFeed("c-pages");
    const got = getFeedState().feeds["c-pages"] ?? [];
    expect(got.length).toBe(1201);
    expect(got.at(-1)?.id).toBe("f1200");
  });

  it("a page holding two copies of one item keeps the later one, and items of another conversation go to their own feed", async () => {
    const at = (s: number) => new Date(Date.parse("2026-01-03T00:00:00Z") + s * 1000).toISOString();
    const items: FeedItem[] = [
      { id: "d1", conversation: "c-dup", at: at(2), kind: "routed", ref: "r", body: { draft: "first" } },
      { id: "o1", conversation: "c-dup-other", at: at(1), kind: "routed", ref: "r" },
      { id: "d0", conversation: "c-dup", at: at(1), kind: "routed", ref: "r" },
      { id: "d1", conversation: "c-dup", at: at(2), kind: "routed", ref: "r", body: { draft: "second" } },
    ];
    vi.stubGlobal("localStorage", { getItem: () => "jd_test" });
    vi.stubGlobal("fetch", vi.fn(async () => ({ ok: true, status: 200, text: async () => JSON.stringify({ items }) })));
    await loadFeed("c-dup");
    const feed = getFeedState().feeds["c-dup"] ?? [];
    expect(feed.map((i) => i.id)).toEqual(["d0", "d1"]);
    expect(field(feed[1], "draft")).toBe("second");
    expect(getFeedState().feeds["c-dup-other"]?.map((i) => i.id)).toEqual(["o1"]);
  });

  it("reads past a full page and applies the snapshot when view_actions is an object, as the hub sends it", async () => {
    const t0 = Date.parse("2026-01-02T00:00:00Z");
    const all: FeedItem[] = Array.from({ length: 501 }, (_, i) => ({ id: `g${String(i).padStart(4, "0")}`, conversation: "c-snap", at: new Date(t0 + i * 1000).toISOString(), kind: "routed", ref: "r" }));
    const answer = { key: "item-1/0", action: "b", client_msg_id: "cid-1", status: "sent", revision: 1, at: "2026-01-02T00:00:00Z" };
    const fetcher = vi.fn(async (url: string) => {
      const since = new URL(url, "http://hub").searchParams.get("since");
      const items = all.filter((it) => !since || Date.parse(it.at) > Date.parse(since)).slice(0, 500);
      // the hub's snapshot (server/internal/assistant/tasks.go): a map of answers, even when empty
      const body = { items, plans: {}, views: {}, view_actions: since ? {} : { [answer.key]: answer }, view_blocks: ["item-1/0"] };
      return { ok: true, status: 200, text: async () => JSON.stringify(body) };
    });
    vi.stubGlobal("localStorage", { getItem: () => "jd_test" });
    vi.stubGlobal("fetch", fetcher);
    await loadFeed("c-snap");
    expect(fetcher).toHaveBeenCalledTimes(2);
    expect(getFeedState().feeds["c-snap"]?.length).toBe(501);
    const cards = conversationCards("c-snap");
    expect(cards.actions["item-1/0"]?.client_msg_id).toBe("cid-1");
    expect(cards.blocks["item-1/0"]).toBe(true);
  });
});
