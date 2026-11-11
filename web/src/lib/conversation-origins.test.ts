import { describe, expect, it } from "vitest";
import { autoConversations, autoOrigin, matchesOrigin, parseOriginFilter, recentConversations } from "./conversation-origins";
import type { Session } from "./types";

const conv = (id: string, extra: Partial<Session> = {}) => ({ machine_id: "hub", session_id: id, title: id, cwd: "", status: "idle", ...extra }) as Session;

describe("conversation origins (docs/protocol/protocol.md「Origin of assistant chats」)", () => {
  it("counts none, user and channel as the user's own and anything else as automatic", () => {
    expect(autoOrigin(conv("a"))).toBeUndefined();
    expect(autoOrigin(conv("a", { origin: "user" }))).toBeUndefined();
    expect(autoOrigin(conv("a", { origin: "channel" }))).toBeUndefined();
    expect(autoOrigin(conv("a", { origin: "inbox" }))).toBe("inbox");
    expect(autoOrigin(conv("a", { origin: "lark_agent" }))).toBe("lark_agent");
    expect(autoOrigin(conv("a", { origin: "something_new" }))).toBe("other");
  });

  it("tallies the automatic ones by origin, in the sidebar's order, with what runs and waits", () => {
    const list = [
      conv("main", { origin: "task" }),
      conv("t1", { origin: "task", state: "working" }),
      conv("i1", { origin: "inbox" }),
      conv("i2", { origin: "inbox", state: "needs_you" }),
      conv("i3", { origin: "inbox", archived: true, state: "needs_you" }),
      conv("x", { origin: "future" }),
      conv("mine"),
    ];
    const { total, groups } = autoConversations(list, "main");
    expect(total).toEqual({ count: 4, working: 1, needsYou: 1 });
    expect(groups).toEqual([
      { origin: "inbox", count: 2, working: 0, needsYou: 1 },
      { origin: "task", count: 1, working: 1, needsYou: 0 },
      { origin: "other", count: 1, working: 0, needsYou: 0 },
    ]);
  });

  it("filters 全部对话 by an address's filter", () => {
    expect(parseOriginFilter(undefined)).toBe("all");
    expect(parseOriginFilter("bogus")).toBe("all");
    expect(parseOriginFilter("mine")).toBe("mine");
    expect(parseOriginFilter("inbox")).toBe("inbox");
    expect(matchesOrigin(conv("a", { origin: "inbox" }), "mine")).toBe(false);
    expect(matchesOrigin(conv("a", { origin: "channel" }), "mine")).toBe(true);
    expect(matchesOrigin(conv("a", { origin: "inbox" }), "inbox")).toBe(true);
    expect(matchesOrigin(conv("a", { origin: "task" }), "inbox")).toBe(false);
    expect(matchesOrigin(conv("a", { origin: "task" }), "all")).toBe(true);
  });
});

describe("the sidebar's Recent (#4873)", () => {
  const at = (h: number) => new Date(Date.UTC(2026, 10, 12, 12) - h * 3_600_000).toISOString();
  const list = [
    conv("main", { updated_at: at(0) }),
    conv("lead", { updated_at: at(0.5) }),
    conv("inbox", { origin: "inbox", updated_at: at(0.2) }),
    conv("gone", { archived: true, updated_at: at(0.1) }),
    conv("a", { origin: "user", updated_at: at(1) }),
    conv("b", { updated_at: at(2) }),
    conv("wx", { origin: "channel", updated_at: at(3) }),
    conv("d", { updated_at: at(4) }),
    conv("e2", { updated_at: at(5) }),
    conv("e1", { updated_at: at(5) }),
    conv("never"),
  ];

  it("is the newest five of the user's own chats, never the main one, a leader, an archived or an automatic one", () => {
    expect(recentConversations(list, "main", ["lead"]).map((c) => c.session_id)).toEqual(["a", "b", "wx", "d", "e1"]);
  });

  it("takes an ended leader back, and is empty when nothing else is left", () => {
    expect(recentConversations(list, "main", [])[0].session_id).toBe("lead");
    expect(recentConversations(list.slice(0, 3), "main", ["lead"])).toEqual([]);
  });
});
