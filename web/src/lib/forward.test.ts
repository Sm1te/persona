import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import fixture from "../../../spec/fixtures/forward/cases.json";
import { initI18n, t } from "../i18n";
import { forwardDraftOf, forwardHeading, forwardMeta, forwardOrigin, forwardSummary, jumpWhenOpen, peekJump, takeJump, type ForwardSource } from "./forward";
import { forwardBody, forwardRef, forwardRoute, forwardURL, FORWARD_LIMIT, isoWithOffset } from "./forward-block";
import { addContext, contextXML, getContexts, removeContext, withContexts } from "./links";
import { parseUserText } from "./turns";
import type { Item, Session } from "./types";

// router.ts listens to popstate at load (no window in node); links.ts only needs navigate from it.
vi.mock("./router", () => ({ navigate: vi.fn() }));

// Forwarding a message (#3899, docs/protocol/protocol.md "Forwarded messages"): the block as spec/fixtures/forward
// writes and reads it (iOS ForwardTests checks the same cases), what of a message it carries, and its card's words.

interface Block { title: string; url: string; msg: string; attributes: Record<string, string>; text: string }
interface Expected {
  kind: string;
  title: string;
  url: string;
  msg: string;
  attributes: Record<string, string>;
  text: string;
  rest: string;
  origin: { machine: string; session: string; item?: string };
}
interface Case { name: string; block?: Block; xml?: string; message?: string; expected?: Expected }

const cases = (fixture as { version: number; cases: Case[] }).cases;

describe("spec/fixtures/forward", () => {
  it("is version 1 with writing and reading cases", () => {
    expect((fixture as { version: number }).version).toBe(1);
    expect(cases.length).toBeGreaterThanOrEqual(3);
  });

  it.each(cases.filter((c) => c.block).map((c) => [c.name, c] as const))("writes %s byte for byte, and reads it back", (_, c) => {
    const b = c.block!;
    const xml = contextXML({ kind: "forward", title: b.title, url: b.url, text: b.text, attrs: { msg: b.msg, ...b.attributes } });
    expect(xml).toBe(c.xml);
    // the attributes in any order write the same block
    const shuffled = Object.fromEntries(Object.entries({ ...b.attributes, msg: b.msg }).reverse());
    expect(contextXML({ kind: "forward", title: b.title, url: b.url, text: b.text, attrs: shuffled })).toBe(c.xml);
    const back = parseUserText(xml).contexts[0];
    const { msg, ...attributes } = back.attrs ?? {};
    expect({ url: back.url, msg, attributes }).toEqual({ url: b.url, msg: b.msg, attributes: b.attributes });
  });

  it.each(cases.filter((c) => c.message).map((c) => [c.name, c] as const))("reads %s: the block, the note after it and the origin", (_, c) => {
    const e = c.expected!;
    const p = parseUserText(c.message!);
    expect(p.contexts).toHaveLength(1);
    const r = p.contexts[0];
    const { msg, ...attributes } = r.attrs ?? {};
    expect({ kind: r.kind, title: r.title, url: r.url, msg, attributes, text: r.text, rest: p.text }).toEqual({
      kind: e.kind, title: e.title, url: e.url, msg: e.msg, attributes: e.attributes, text: e.text, rest: e.rest,
    });
    expect(forwardOrigin(r.url!, msg)).toEqual(e.origin);
  });
});

const source = (over: Partial<ForwardSource> = {}): ForwardSource => ({
  mid: "macbook",
  sid: "a1f3c2d4-0000-4000-8000-000000000006",
  item: "e5f0#12",
  role: "agent",
  author: "Claude",
  chat: "Network tests",
  project: "weather › tests",
  machine: "MacBook Pro",
  ...over,
});

describe("a forward", () => {
  beforeEach(() => initI18n("en"));
  afterEach(() => initI18n("zh-Hans"));

  it("carries its source and a link back", () => {
    const ref = forwardRef("The answer", source({ time: "2026-11-11T02:24:00Z" }));
    expect(ref).toMatchObject({ kind: "forward", text: "The answer", title: "Forwarded from Claude · Network tests" });
    expect(ref.attrs).toMatchObject({ msg: "e5f0#12", role: "agent", author: "Claude", chat: "Network tests", project: "weather › tests", machine: "MacBook Pro" });
    expect(Date.parse(ref.attrs!.time)).toBe(Date.parse("2026-11-11T02:24:00Z"));
    // the link names the session and the item, `#` escaped, and reads back
    expect(ref.url).toBe("persona://session/macbook/a1f3c2d4-0000-4000-8000-000000000006?at=e5f0%2312");
    expect(forwardOrigin(ref.url)).toEqual({ machine: "macbook", session: "a1f3c2d4-0000-4000-8000-000000000006", item: "e5f0#12" });
    // what goes out: the attributes in the shared order
    expect(contextXML(ref)).toMatch(/^<persona-context kind="forward" title="Forwarded from Claude · Network tests" url="[^"]+" msg="e5f0#12" role="agent" author="Claude" chat="Network tests" project="weather › tests" machine="MacBook Pro" time="[^"]+">\nThe answer\n<\/persona-context>$/);
  });

  it("names the user's own message Me, and leaves a Persona chat's project and computer out", () => {
    const mine = forwardRef("Mine", source({ role: "user" }));
    expect(mine.attrs).toMatchObject({ role: "user", author: "Me" });
    expect(mine.title).toBe("Forwarded from Me · Network tests");
    const p = forwardRef("Hi", source({ role: "persona", author: "Persona", project: undefined, machine: undefined }));
    expect(p.attrs?.project).toBeUndefined();
    expect(p.attrs?.machine).toBeUndefined();
  });

  it("writes its time with the sender's offset, to the second", () => {
    const iso = isoWithOffset("2026-11-11T02:24:05.123Z");
    expect(iso).toMatch(/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(Z|[+-]\d\d:\d\d)$/);
    expect(Date.parse(iso)).toBe(Date.parse("2026-11-11T02:24:05Z"));
    expect(isoWithOffset("not a time")).toBe("");
  });

  it("escapes what URLComponents escapes, and only that", () => {
    expect(forwardURL("m", "s", "a#3")).toBe("persona://session/m/s?at=a%233");
    expect(forwardURL("m", "s", "a b")).toBe("persona://session/m/s?at=a%20b");
    expect(forwardURL("m", "s", "x:y/z")).toBe("persona://session/m/s?at=x:y/z");
    expect(forwardURL("m", "s", "")).toBe("persona://session/m/s");
    expect(forwardOrigin("https://example.com/session/m/s")).toBeNull();
    expect(forwardOrigin("persona://session/m")).toBeNull();
    expect(forwardOrigin("persona://session/m/s", "u1")).toEqual({ machine: "m", session: "s", item: "u1" });
  });
});

describe("what a forward carries", () => {
  beforeEach(() => initI18n("en"));
  afterEach(() => initI18n("zh-Hans"));

  it("is what the chat shows: no metadata or photo note; a quote or forward it opened with as `> ` lines", () => {
    const inner = contextXML({ kind: "forward", title: "Forwarded from Codex · Release", url: "persona://session/s/x?at=1", text: "Ship it", attrs: { msg: "1", role: "agent", author: "Codex", chat: "Release" } });
    const raw = `<persona-context kind="message-source" title="">\n{"author":"user"}\n</persona-context>\n${inner}\n\nMy words\n<attached_images>\n/Users/me/.persona/inbox/a.jpg\n</attached_images>`;
    const body = forwardBody(raw);
    expect(body).toBe("> Forwarded from Codex · Release\n> Ship it\n\nMy words");
    expect(body).not.toContain("message-source");
    expect(body).not.toContain("attached_images");
  });

  it("names files on its computer instead of copying them", () => {
    const raw = 'Look\n<persona-file kind="video" path="/Users/me/a.mp4" name="a.mp4" size="12"/>';
    expect(forwardBody(raw, "MacBook Pro")).toBe("Look\n\n(File on MacBook Pro: a.mp4)");
    expect(forwardBody(raw)).toBe("Look\n\n(File: a.mp4)");
  });

  it("is cut past the limit, and says so", () => {
    const long = "x".repeat(FORWARD_LIMIT + 500);
    const cut = forwardBody(long);
    expect(cut.startsWith("x".repeat(FORWARD_LIMIT))).toBe(true);
    expect(cut.endsWith(t("chat-forwardto.cut"))).toBe(true);
    expect(cut.length).toBeLessThan(long.length);
  });

  it("goes over a box once (lib/links.ts addContext): the same message twice is one forward", () => {
    const key = "test/forward-once";
    addContext(key, forwardRef("x", source()));
    addContext(key, forwardRef("x", source()));
    expect(getContexts(key)).toHaveLength(1);
    getContexts(key).forEach((r) => removeContext(key, r.id));
  });

  it("with no note says to look at it; with pictures, at those", () => {
    const ref = forwardRef("x", source());
    expect(withContexts([ref], "  ")).toMatch(/\n\nPlease look at the forwarded message above\.$/);
    expect(withContexts([ref], "", true)).toMatch(/\n\nSee the images$/);
    expect(withContexts([ref], "Make the upload use the same retries")).toMatch(/<\/persona-context>\n\nMake the upload use the same retries$/);
  });
});

describe("the card and the route", () => {
  beforeEach(() => initI18n("en"));
  afterEach(() => initI18n("zh-Hans"));

  it("heads the card with who wrote it and the chat, then the computer and time", () => {
    const now = new Date();
    const at = new Date(now.getTime() - 60_000).toISOString();
    const ref = forwardRef("x", source({ time: at }));
    expect(forwardHeading(ref.attrs)).toBe("Forwarded from Claude · Network tests");
    expect(forwardMeta(ref.attrs)).toMatch(/^MacBook Pro · \d\d:\d\d$/);
    // another day's carries its date
    const old = forwardRef("x", source({ time: new Date(now.getTime() - 3 * 86_400_000).toISOString() }));
    expect(forwardMeta(old.attrs).length).toBeGreaterThan(forwardMeta(ref.attrs).length);
    // the reader's own language for the user's own message, whatever the sender's
    expect(forwardHeading({ role: "user", author: "我", chat: "Release" })).toBe("Forwarded from Me · Release");
    expect(forwardHeading({ author: "Claude" })).toBe("Forwarded from Claude");
    expect(forwardMeta({})).toBe("");
  });

  it("shows the first lines without blank ones", () => {
    expect(forwardSummary("\n a \n\n b\n")).toBe("a\nb");
    expect(forwardSummary("  ")).toBe(t("chat-forward.no_content"));
  });

  it("names the computers it comes from and goes to", () => {
    const across = forwardRoute("MacBook Pro", { agent: "Codex", machine: "Mac Studio" });
    expect(across).toBe("From MacBook Pro · to Codex on Mac Studio");
    expect(forwardRoute("Mac Studio", { agent: "Codex", machine: "Mac Studio" })).toBe("To Codex on Mac Studio");
    expect(forwardRoute("MacBook Pro")).toBe("From MacBook Pro · to Persona");
    expect(forwardRoute(undefined)).toBe("To Persona");
    expect(forwardRoute(undefined, { agent: "Claude" })).toBe("To Claude");
  });
});

describe("the picked message as a forward (ChatView)", () => {
  const items: Item[] = [
    { id: "u1#0", seq: 1, role: "user", kind: "text", text: '<persona-context kind="forward" title="t" url="persona://session/m/s?at=1" msg="1" role="agent" author="Codex" chat="Release">\nShip it\n</persona-context>\n\nMy note', ts: "2026-11-11T01:00:00Z" },
    { id: "u1#1", seq: 1, role: "user", kind: "image", image: { mime: "image/png", data: "AAAA" } },
    { id: "r1#0", seq: 2, role: "assistant", kind: "text", text: "A long reply", full_size: 40_000, ts: "2026-11-11T01:01:00Z" },
  ];
  const session = { machine_id: "macbook", session_id: "s1", title: "Network tests", cwd: "/w/weather", status: "idle", agent: "codex", space_name: "weather", slot_name: "tests" } as Session;
  const chat = { mid: "macbook", sid: "s1", assistant: false, title: "Network tests", items, session, machine: "MacBook Pro" };

  it("takes an Agent's reply with its project and computer, and reads the whole of a cut one when it goes", () => {
    const d = forwardDraftOf(chat, { itemId: "r1#0", text: "A long", isUser: false });
    expect(d.source).toEqual({ mid: "macbook", sid: "s1", item: "r1#0", role: "agent", author: "Codex", chat: "Network tests", project: "weather › tests", machine: "MacBook Pro", time: "2026-11-11T01:01:00Z" });
    expect(d.raw).toBe("A long reply");
    expect(d.cut).toBe("r1#0");
    expect(d.images).toBeUndefined();
  });

  it("takes your own message as the transcript has it, quotes and pictures with it", () => {
    const d = forwardDraftOf(chat, { itemId: "u1#0", text: "My note", isUser: true, images: [items[1]], photos: ["/p/a.jpg"] });
    expect(d.source.role).toBe("user");
    expect(d.raw).toContain('kind="forward"');
    expect(d.cut).toBeUndefined();
    expect(d.images).toEqual([items[1]]);
    expect(d.photos).toEqual(["/p/a.jpg"]);
  });

  it("calls Persona's own messages Persona's, with no project or computer", () => {
    const d = forwardDraftOf({ ...chat, assistant: true, session: { ...session, assistant: true } }, { itemId: "r1#0", text: "x", isUser: false });
    expect(d.source).toMatchObject({ role: "persona", author: "Persona" });
    expect(d.source.project).toBeUndefined();
    expect(d.source.machine).toBeUndefined();
    expect(forwardDraftOf(chat, { itemId: "r1#0", text: "x", isUser: false, from: "@Patrol" }).source).toMatchObject({ role: "other", author: "@Patrol" });
  });
});

describe("opening the original", () => {
  it("is taken once, by the chat it names", () => {
    jumpWhenOpen("m", "s", "i1");
    expect(takeJump("m", "other")).toBeUndefined();
    expect(peekJump("m", "s")).toBe("i1");
    expect(takeJump("m", "s")).toBe("i1");
    expect(takeJump("m", "s")).toBeUndefined();
  });
});
