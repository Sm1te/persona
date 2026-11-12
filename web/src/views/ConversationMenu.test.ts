import { describe, expect, it, vi } from "vitest";

vi.hoisted(() => {
  const kept = new Map<string, string>();
  const g = globalThis as Record<string, unknown>;
  g.localStorage = { getItem: (k: string) => kept.get(k) ?? null, setItem: (k: string, v: string) => void kept.set(k, v), removeItem: (k: string) => void kept.delete(k) };
  g.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
});

import type { Session } from "../lib/types";
import type { ToastAction } from "../ui/atoms";
import { conversationMenuItems, leaderItem, type ConversationMenuDeps } from "./ConversationMenu";

const conv = (o: Partial<Session> = {}) => ({ machine_id: "hub", session_id: "c1", title: "周报", status: "idle", assistant: true, ...o }) as Session;

function deps(over: Partial<ConversationMenuDeps> = {}) {
  const calls: string[] = [];
  const actions: ToastAction[] = [];
  const d: ConversationMenuDeps = {
    rename: async (_s, title) => void calls.push(`rename:${title}`),
    archive: async (s) => void calls.push(`archive:${s.session_id}`),
    unarchive: async (s) => void calls.push(`unarchive:${s.session_id}`),
    end: async (s) => void calls.push(`end:${s.session_id}`),
    refresh: async () => void calls.push("refresh"),
    lead: async (s, on) => void calls.push(`${on ? "make" : "end"}:${s.session_id}`),
    copy: (text) => void calls.push(`copy:${text}`),
    notify: (text, tone, action) => {
      calls.push(`${tone}:${text}`);
      if (action) actions.push(action);
    },
    ...over,
  };
  return { d, calls, actions };
}


describe("a Persona conversation's sidebar menu (iOS AssistantSidebar)", () => {
  it("offers 设为常驻, 重命名…, 归档, 拷贝会话 ID and 结束… for a running conversation, and never the word 置顶", () => {
    const items = conversationMenuItems(conv(), { main: false });
    expect(items.map((i) => i.label)).toEqual(["设为常驻", "重命名…", "归档", "拷贝会话 ID", "结束…"]);
    expect(items.find((i) => i.key === "end")).toMatchObject({ danger: true, confirm: "确认结束", inline: true });
  });

  it("gives the main conversation only 拷贝会话 ID and 结束…", () => {
    expect(conversationMenuItems(conv(), { main: true }).map((i) => i.key)).toEqual(["copy", "end"]);
  });

  it("has no 结束… once it has stopped", () => {
    expect(conversationMenuItems(conv({ status: "offline" }), { main: false, leader: true }).map((i) => i.label)).toEqual(["取消常驻", "重命名…", "归档", "拷贝会话 ID"]);
  });

  it("makes a chat a lead (常驻) and ends one through the hub, says so, then reads the list again", async () => {
    const { d, calls } = deps();
    await conversationMenuItems(conv(), { main: false }, d).find((i) => i.key === "leader")!.onSelect();
    expect(calls).toEqual(["make:c1", "ok:已设为常驻「周报」", "refresh"]);
    calls.length = 0;
    await leaderItem(conv(), true, d).onSelect();
    expect(calls).toEqual(["end:c1", "ok:已取消常驻「周报」，它派出的任务不变", "refresh"]);
  });

  it("says so when the hub refuses", async () => {
    const { d, calls } = deps({ lead: async () => { throw new Error("at most 20 directions: end one first"); } });
    await leaderItem(conv(), false, d).onSelect();
    expect(calls).toEqual(["error:没能改常驻：at most 20 directions: end one first", "refresh"]);
  });

  it("renames, archives, copies the id and ends through the hub, then reads the list again", async () => {
    const { d, calls } = deps();
    const items = conversationMenuItems(conv(), { main: false }, d);
    expect(items.find((i) => i.key === "rename")!.rename!.value).toBe("周报");
    await items.find((i) => i.key === "rename")!.rename!.commit("新标题");
    await items.find((i) => i.key === "archive")!.onSelect();
    await items.find((i) => i.key === "copy")!.onSelect();
    await items.find((i) => i.key === "end")!.onSelect();
    expect(calls).toEqual(["rename:新标题", "ok:已重命名", "refresh", "archive:c1", "ok:已归档「周报」", "refresh", "copy:c1", "end:c1", "ok:已结束对话，记录仍保留", "refresh"]);
  });

  it("offers 撤销 after archiving, which unarchives it and reads the list again", async () => {
    const { d, calls, actions } = deps();
    await conversationMenuItems(conv(), { main: false }, d).find((i) => i.key === "archive")!.onSelect();
    expect(actions.map((a) => a.label)).toEqual(["撤销"]);
    calls.length = 0;
    actions[0].run();
    await vi.waitFor(() => expect(calls).toEqual(["unarchive:c1", "ok:已取消归档「周报」", "refresh"]));
  });

  it("says why when 撤销 fails, and offers no 撤销 when archiving failed", async () => {
    const { d, calls, actions } = deps({ unarchive: async () => Promise.reject(new Error("offline")) });
    await conversationMenuItems(conv(), { main: false }, d).find((i) => i.key === "archive")!.onSelect();
    calls.length = 0;
    actions[0].run();
    await vi.waitFor(() => expect(calls).toEqual(["error:没能撤销：offline", "refresh"]));
    const failed = deps({ archive: async () => Promise.reject(new Error("offline")) });
    await conversationMenuItems(conv(), { main: false }, failed.d).find((i) => i.key === "archive")!.onSelect();
    expect(failed.actions).toEqual([]);
    expect(failed.calls).toEqual(["error:offline", "refresh"]);
  });

  it("gives the background supervisor only 拷贝会话 ID: it is built in, running or not (#5082)", async () => {
    const { d, calls } = deps();
    const items = conversationMenuItems(conv(), { main: false, builtIn: true }, d);
    expect(items.map((i) => i.label)).toEqual(["拷贝会话 ID"]);
    expect(conversationMenuItems(conv({ status: "offline" }), { main: false, leader: true, builtIn: true }).map((i) => i.key)).toEqual(["copy"]);
    await items[0].onSelect();
    expect(calls).toEqual(["copy:c1"]);
  });

  it("keeps the menu open with the reason when ending fails", async () => {
    const { d, calls } = deps({ end: async () => Promise.reject(new Error("offline")) });
    const end = conversationMenuItems(conv(), { main: false }, d).find((i) => i.key === "end")!;
    await expect(end.onSelect()).rejects.toThrow("没能结束对话：offline");
    expect(calls).toEqual([]);
  });
});
