// The "Today" card's "Needs you" row says what each waiting task waits on (#4478), not only how many wait: one task's ask under
// its title, several as "<title>: <ask>" lines; a task the store has no card for keeps its title.
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, describe, expect, it } from "vitest";
import type { FeedItem, TodayBody } from "../../lib/feed";
import { getState } from "../../lib/store";
import type { Todo } from "../../lib/todos";
import type { Interaction, Session } from "../../lib/types";
import { FeedLineView } from "./Tasks";

const now = new Date().toISOString();
const todo = (id: string, sid: string): Todo => ({ id, title: id, origin: "c1", created_at: now, updated_at: now, agents: [{ machine_id: "m1", session_id: sid }] }) as Todo;
const session = (sid: string) => ({ machine_id: "m1", session_id: sid, title: sid, state: "needs_you", status: "blocked", updated_at: now }) as Session;
const permission: Interaction = { id: "ix1", machine_id: "m1", session_id: "s1", kind: "permission", tool_name: "Bash", tool_input: { command: "swift test" } };

const seed = (patch: Record<string, unknown>) => Object.assign(getState() as unknown as Record<string, unknown>, patch);
const before = { ...getState() };
afterEach(() => seed(before));

const today = (body: TodayBody): FeedItem => ({ id: "f1", conversation: "c1", at: now, kind: "today", body: body as unknown as Record<string, unknown> });

describe("the 今天 card's 需要你 row", () => {
  it("says what the one waiting task asks", () => {
    seed({ todos: [todo("修测试", "s1")], sessions: { m1: [session("s1")] }, interactions: [permission], answered: {} });
    const html = renderToStaticMarkup(<FeedLineView item={today({ date: now, waiting: [{ id: "修测试", title: "修测试" }] })} />);
    expect(html).toContain("需要你：修测试");
    expect(html).toContain("在等你批准：swift test");
  });

  it("lists several by title and what each asks, a title alone without a card", () => {
    seed({ todos: [todo("修测试", "s1"), todo("写文档", "s2")], sessions: { m1: [session("s1"), session("s2")] }, interactions: [permission], answered: {} });
    const html = renderToStaticMarkup(<FeedLineView item={today({ date: now, waiting: [{ id: "修测试", title: "修测试" }, { id: "写文档", title: "写文档" }] })} />);
    expect(html).toContain("2 件需要你");
    expect(html).toContain("修测试：在等你批准：swift test");
    // each task on its own truncated line, not one clamped block
    const lines = [...html.matchAll(/data-today-line="true" class="truncate[^"]*" title="([^"]*)"/g)].map((m) => m[1]);
    expect(lines).toEqual(["修测试：在等你批准：swift test", "写文档"]);
    expect(html).not.toContain("line-clamp-2 whitespace-pre-line text-[12px] text-ink-2\">修测试");
  });

  it("shows three and says how many more past them", () => {
    const ids = ["a", "b", "c", "d", "e"];
    seed({ todos: ids.map((id) => todo(id, `s-${id}`)), sessions: { m1: ids.map((id) => session(`s-${id}`)) }, interactions: [], answered: {} });
    const html = renderToStaticMarkup(<FeedLineView item={today({ date: now, waiting: ids.map((id) => ({ id, title: id })) })} />);
    const lines = [...html.matchAll(/data-today-line="true"[^>]*>([^<]*)</g)].map((m) => m[1]);
    expect(lines).toEqual(["a", "b", "c", "还有 2 件"]);
  });

  it("does not list a card just answered here", () => {
    const answered = { [permission.id]: { ix: permission, resolution: { allow: true }, at: Date.now() } };
    seed({ todos: [todo("修测试", "s1")], sessions: { m1: [session("s1")] }, interactions: [], answered });
    const html = renderToStaticMarkup(<FeedLineView item={today({ date: now, waiting: [{ id: "修测试", title: "修测试" }] })} />);
    expect(html).not.toContain("swift test");
    expect(html).not.toContain("data-today-line");
  });
});
