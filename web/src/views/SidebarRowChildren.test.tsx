// A parent's sidebar row carries its child sessions (#5120): its mark is the folded state, its second line what a
// waiting child asks, its count holds the children's cards, and a line says how many subagents run.
import { describe, expect, it, vi } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import type { Interaction, Session } from "../lib/types";

vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
  const kept = new Map<string, string>();
  g.localStorage = { getItem: (k: string) => kept.get(k) ?? null, setItem: (k: string, v: string) => void kept.set(k, v), removeItem: (k: string) => void kept.delete(k) };
});

const fake = vi.hoisted(() => ({ interactions: [] as Interaction[] }));
vi.mock("../lib/store", async (importOriginal) => {
  const real = await importOriginal<typeof import("../lib/store")>();
  return { ...real, useStore: <T,>(select: (s: ReturnType<typeof real.getState>) => T) => select({ ...real.getState(), interactions: fake.interactions }) };
});

const { SessionRow } = await import("./Sidebar");

const parent: Session = { machine_id: "mac", session_id: "lead", title: "Migrate the tests", cwd: "/code", status: "idle", state: "idle" };
const props = { k: "s:mac/lead", reg: () => () => {}, hover: () => {}, active: false, to: { name: "session", mid: "mac", sid: "lead" } as const, go: () => () => {} };
const render = (s: Session, interactions: Interaction[] = []) => {
  fake.interactions = interactions;
  return renderToStaticMarkup(<SessionRow {...props} s={s} />);
};
const mark = (html: string) => html.match(/class="status-mark[^"]*"[^>]*data-state="([^"]+)"/)?.[1];
const line = (html: string) => html.match(/<span data-row-line[^>]*>(.*?)<\/span><\/span>/)?.[1]?.replace(/<[^>]+>/g, "");
const childLine = (html: string) => html.match(/<span data-children-line[^>]*>.*?<span class="truncate tabular-nums">(.*?)<\/span>/)?.[1];
const badge = (html: string) => html.match(/text-attention tabular-nums[^"]*">(\d+)<\/span>/)?.[1];

describe("a parent's sidebar row (#5120)", () => {
  it("draws as before without children", () => {
    const html = render(parent);
    expect(mark(html)).toBe("idle");
    expect(html).not.toContain("data-children-line");
    expect(badge(html)).toBeUndefined();
  });

  it("works while a child works, and says how many subagents run", () => {
    const html = render({ ...parent, children: { total: 3, working: 2 } });
    expect(mark(html)).toBe("working");
    expect(childLine(html)).toBe("子 Agent 2 个在跑");
  });

  it("counts a parent's own background agents and its children once", () => {
    // a Paseo parent that lists its running children as its own background agents too
    const html = render({ ...parent, waiting: false, running_subagents: 3, children: { total: 3, working: 2 } });
    expect(childLine(html)).toBe("子 Agent 3 个在跑");
  });

  it("needs you when a child asks: its line is the child's card, its count holds it", () => {
    const card = { id: "c1", machine_id: "mac", session_id: "child", folded_under: "lead", kind: "question", questions: [{ question: "Which test runner?" }] } as Interaction;
    const html = render({ ...parent, children: { total: 2, needs_you: 1 } }, [card]);
    expect(mark(html)).toBe("needs_you");
    expect(line(html)).toBe("在问你：Which test runner?");
    expect(badge(html)).toBe("1");
  });

  it("counts its own cards and its children's together, its own first in the line", () => {
    const own = { id: "o1", machine_id: "mac", session_id: "lead", kind: "permission", tool_name: "Bash", tool_input: { command: "swift test" } } as Interaction;
    const child = { id: "c1", machine_id: "mac", session_id: "child", folded_under: "lead", kind: "question", questions: [{ question: "Which test runner?" }] } as Interaction;
    const other = { id: "x1", machine_id: "other", session_id: "child", folded_under: "lead", kind: "question", questions: [{ question: "Not this one" }] } as Interaction;
    const html = render({ ...parent, state: "needs_you", pending_interactions: 1, children: { total: 1, needs_you: 1 } }, [own, child, other]);
    expect(badge(html)).toBe("2");
    expect(line(html)).not.toContain("Which test runner?");
    expect(html).not.toContain("Not this one");
  });
});
