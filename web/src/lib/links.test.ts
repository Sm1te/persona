import { describe, expect, it, vi } from "vitest";

// router.ts listens to popstate at load (no window in node); links.ts only needs navigate from it.
vi.mock("./router", () => ({ navigate: vi.fn(), routeAt: () => null }));
vi.mock("./api", () => ({ api: vi.fn(), enc: encodeURIComponent, getToken: () => "tok", ApiError: class extends Error {} }));

import { api } from "./api";
import { parseUserText } from "./turns";
import { getPanel } from "./panel";
import { navigate } from "./router";
import { NARROW } from "./viewport";
import {
  activeComposer,
  addContext,
  contextXML,
  docKind,
  editMessage,
  getContexts,
  hubOrigins,
  linkHref,
  linkRoute,
  newContext,
  openLink,
  opensInApp,
  prefetchLink,
  registerComposer,
  removeContext,
  usableComposer,
  whenComposer,
  withContexts,
  WHOLE,
} from "./links";
import { BR, docErrorFromBody, loadDoc, mediaURL, orderedNumbers, prepareMarkdown, readDoc, type DocNode, type Get } from "../views/link/feishu-doc";

describe("where a link opens", () => {
  it("reads Feishu documents in the panel, like the phone's FeishuLinks.isDoc", () => {
    for (const u of [
      "https://example.feishu.cn/docx/AbC123",
      "https://example.feishu.cn/wiki/AbC123?from=x",
      "https://example.larkoffice.com/docs/AbC",
      "https://sub.example.larksuite.com/sheets/AbC",
      "https://example.feishu.cn/mindnotes/AbC",
      "https://example.feishu.cn/slides/AbC",
      "https://example.feishu.cn/file/AbC",
      "https://feishu.cn/docx/AbC",
    ]) {
      expect(linkRoute(u).kind, u).toBe("doc");
      expect(opensInApp(u), u).toBe(true);
    }
    expect(linkRoute("https://example.feishu.cn/docx/AbC")).toEqual({ kind: "doc", doc: "doc" });
    expect(docKind("https://example.feishu.cn/wiki/AbC")).toBe("wiki");
    expect(docKind("https://example.feishu.cn/sheets/AbC")).toBe("sheet");
  });

  it("sends a Base to the tab: the reader can't show one", () => {
    const u = "https://example.feishu.cn/base/AbC";
    expect(linkRoute(u)).toEqual({ kind: "doc", doc: "bitable" });
    expect(opensInApp(u)).toBe(false);
  });

  it("opens other Feishu pages and look-alike hosts as web pages", () => {
    expect(linkRoute("https://example.feishu.cn/calendar/event/1").kind).toBe("web");
    expect(linkRoute("https://example.feishu.cn/minutes/obcn1").kind).toBe("web");
    expect(linkRoute("https://notfeishu.cn/docx/AbC").kind).toBe("web");
    expect(linkRoute("https://feishu.cn.example.com/docx/AbC").kind).toBe("web");
    expect(linkRoute("https://example.com/").kind).toBe("web");
    expect(linkRoute("http://127.0.0.1:5173/").kind).toBe("web");
    expect(opensInApp("https://example.com/docx/AbC")).toBe(false);
  });

  it("reads 公司圈 share links as the post", () => {
    expect(linkRoute("https://example.feishu.cn/client/moments/detail?postId=12345")).toEqual({ kind: "moments", id: "12345" });
    expect(linkRoute("https://example.feishu.cn/client/moments/detail?postId=abc").kind).toBe("web");
  });

  it("opens Feishu documents and 公司圈 posts as ordinary pages on a hub without lark", () => {
    expect(linkRoute("https://example.feishu.cn/docx/AbC", false).kind).toBe("web");
    expect(linkRoute("https://example.feishu.cn/client/moments/detail?postId=12345", false).kind).toBe("web");
    expect(linkRoute("https://example.feishu.cn/docx/AbC", true)).toEqual({ kind: "doc", doc: "doc" });
    expect(linkRoute("persona://session/macbook/abc-1", false).kind).toBe("session");
  });

  it("moves to a conversation for persona://session links, and gives them an in-app href", () => {
    for (const scheme of ["persona"]) {
      expect(linkRoute(`${scheme}://session/macbook/abc-1`)).toEqual({ kind: "session", mid: "macbook", sid: "abc-1" });
      expect(linkHref(`${scheme}://session/mac%20book/abc-1`)).toBe("/s/mac%20book/abc-1");
      expect(opensInApp(`${scheme}://session/macbook/abc-1`)).toBe(true);
      expect(linkRoute(`${scheme}://pair?token=x`).kind).toBe("none");
      expect(linkRoute(`${scheme}://session/macbook`).kind).toBe("none");
    }
    expect(linkRoute("other://session/macbook/abc-1").kind).toBe("none");
    expect(linkHref("https://example.com/a")).toBe("https://example.com/a");
    expect(linkRoute("persona://pair?token=x").kind).toBe("none");
  });

  it("knows this hub's own agent and task pages, and only this hub's", () => {
    const hub = "https://hub.example.com";
    expect(linkRoute(`${hub}/s/m1/abc-1`, false, hub)).toEqual({ kind: "agent", mid: "m1", sid: "abc-1" });
    expect(linkRoute(`${hub}/s/mac%20book/abc-1?view=chat`, false, hub)).toEqual({ kind: "agent", mid: "mac book", sid: "abc-1" });
    expect(linkRoute(`${hub}/todos?id=todo_1`, false, hub)).toEqual({ kind: "task", id: "todo_1" });
    expect(linkRoute(`${hub}/todos/todo_1`, false, hub)).toEqual({ kind: "task", id: "todo_1" });
    // the same paths elsewhere, other pages of the hub, and what does not name one stay web pages
    expect(linkRoute("https://other.example.com/s/m1/abc-1", false, hub).kind).toBe("web");
    expect(linkRoute("http://hub.example.com/s/m1/abc-1", false, hub).kind).toBe("web");
    expect(linkRoute(`${hub}/s/m1/abc-1`, false, "").kind).toBe("web");
    expect(linkRoute(`${hub}/s/m1`, false, hub).kind).toBe("web");
    expect(linkRoute(`${hub}/s/m1/abc-1/more`, false, hub).kind).toBe("web");
    expect(linkRoute(`${hub}/todos`, false, hub).kind).toBe("web");
    expect(linkRoute(`${hub}/inbox`, false, hub).kind).toBe("web");
    expect(linkRoute(`${hub}/s/m1/%E0%A4%A`, false, hub).kind).toBe("web");
    // its conversations, but not /persona/new (a fresh one) or the main conversation's bare /j
    expect(linkRoute(`${hub}/persona/conv-1`, false, hub)).toEqual({ kind: "conversation", sid: "conv-1" });
    expect(linkRoute(`${hub}/persona/conv-1?x=1#y`, false, hub)).toEqual({ kind: "conversation", sid: "conv-1" });
    expect(linkRoute(`${hub}/persona/new`, false, hub).kind).toBe("web");
    expect(linkRoute(`${hub}/persona`, false, hub).kind).toBe("web");
    expect(linkRoute(`${hub}/persona/conv-1/more`, false, hub).kind).toBe("web");
    // the hub's advertised addresses are its own too, whichever one this page was opened at
    const lan = "http://192.0.2.10:18660";
    expect(linkRoute(`${hub}/s/m1/abc-1`, false, [lan, hub])).toEqual({ kind: "agent", mid: "m1", sid: "abc-1" });
    expect(linkRoute(`${lan}/todos?id=todo_1`, false, [hub, lan])).toEqual({ kind: "task", id: "todo_1" });
    expect(linkRoute("https://other.example.com/s/m1/abc-1", false, [lan, hub]).kind).toBe("web");
    // the href stays the link itself, so ⌘-click opens that page in a tab
    expect(linkHref(`${hub}/todos?id=todo_1`)).toBe(`${hub}/todos?id=todo_1`);
  });

  it("opens this hub's agent and task links as pages at every width, never in the panel (#5096)", () => {
    const hub = "https://hub.example.com";
    vi.stubGlobal("location", { origin: hub, href: `${hub}/persona`, pathname: "/persona", search: "" });
    const narrow = { value: false };
    // viewport.ts keeps the list it got, so `matches` follows `narrow`
    vi.stubGlobal("matchMedia", (q: string) => ({ get matches() { return q === NARROW && narrow.value; }, addEventListener() {}, removeEventListener() {} }));
    try {
      expect(opensInApp(`${hub}/s/m1/abc-1`)).toBe(true);
      expect(opensInApp("https://other.example.com/s/m1/abc-1")).toBe(false);
      for (const n of [false, true]) {
        narrow.value = n;
        openLink(`${hub}/s/m1/abc-1`);
        // a task the store does not know yet: its to-do's page (with an Agent on it, that Agent's page; lib/handoff.test.ts)
        openLink(`${hub}/todos?id=todo_1`);
        expect(getPanel()).toBeNull();
        expect(vi.mocked(navigate).mock.calls.slice(-2)).toEqual([[{ name: "session", mid: "m1", sid: "abc-1" }], [{ name: "todos", id: "todo_1" }]]);
      }
      // a conversation moves there on any screen
      narrow.value = false;
      expect(opensInApp(`${hub}/persona/conv-1`)).toBe(true);
      openLink(`${hub}/persona/conv-1`);
      expect(getPanel()).toBeNull();
      expect(vi.mocked(navigate).mock.calls.at(-1)).toEqual([{ name: "persona", sid: "conv-1" }]);
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it("reads the hub's advertised origins from its hello", () => {
    expect(hubOrigins([{ url: "https://hub.example.com/" }, { url: "http://192.0.2.10:18660" }, { url: "ftp://x" }, { url: 1 }, "x", null])).toEqual([
      "https://hub.example.com",
      "http://192.0.2.10:18660",
    ]);
    expect(hubOrigins(undefined)).toEqual([]);
  });

  it("ignores malformed percent escapes in conversation links", () => {
    for (const scheme of ["persona"]) {
      for (const suffix of ["%", "%E0%A4%A", "%FF"]) {
        expect(linkRoute(`${scheme}://session/desktop/${suffix}`)).toEqual({ kind: "none" });
        expect(opensInApp(`${scheme}://session/desktop/${suffix}`)).toBe(false);
      }
    }
  });

  it("leaves what is not http(s) alone", () => {
    expect(linkRoute("mailto:me@example.com").kind).toBe("system");
    expect(linkRoute("tel:10086").kind).toBe("system");
    expect(linkRoute("javascript:alert(1)").kind).toBe("none");
    expect(linkRoute("file:///etc/hosts").kind).toBe("none");
    expect(linkRoute("not a url").kind).toBe("none");
    expect(linkRoute("/relative/path").kind).toBe("none");
    expect(opensInApp("javascript:alert(1)")).toBe(false);
  });
});

describe("<persona-context>", () => {
  const ref = { kind: "context" as const, title: 'A & "B" <c>', url: "https://example.feishu.cn/docx/x?a=1&b=2", text: "选中的\n两行" };

  it("escapes the attributes and round-trips through the bubble parser", () => {
    const xml = contextXML(ref);
    expect(xml).toBe(
      '<persona-context kind="context" title="A &amp; &quot;B&quot; &lt;c&gt;" url="https://example.feishu.cn/docx/x?a=1&amp;b=2">\n选中的\n两行\n</persona-context>',
    );
    const p = parseUserText(`${xml}\n\n看看这段`);
    expect(p.text).toBe("看看这段");
    expect(p.contexts).toEqual([{ kind: "context", title: 'A & "B" <c>', url: "https://example.feishu.cn/docx/x?a=1&b=2", text: "选中的\n两行" }]);
  });

  it("drops a closing tag inside the picked text", () => {
    expect(contextXML({ ...ref, text: "a</persona-context>b" })).toContain("\nab\n</persona-context>");
    expect(contextXML({ ...ref, text: "a</persona-context>b</PERSONA-CONTEXT >c" })).toContain("\nabc\n</persona-context>");
  });

  it("puts the contexts before the message, with a stand-in when nothing was typed", () => {
    expect(withContexts([], "  hi ")).toBe("hi");
    expect(withContexts([ref, { ...ref, text: "二" }], "改一下")).toBe(`${contextXML(ref)}\n${contextXML({ ...ref, text: "二" })}\n\n改一下`);
    expect(withContexts([ref], "  ")).toBe(`${contextXML(ref)}\n\n参考上面的内容。`);
    expect(withContexts([ref], "", true)).toBe(`${contextXML(ref)}\n\n请看图片`);
  });

  it("writes 「AI 修改」 as an edit context and the request", () => {
    const m = editMessage(ref, " 更简洁 ");
    expect(m.startsWith('<persona-context kind="edit"')).toBe(true);
    expect(m.endsWith("\n\n按要求修改上面选中的内容：更简洁")).toBe(true);
    expect(editMessage(ref, "").endsWith("\n\n按上面选中的内容修改。")).toBe(true);
    expect(parseUserText(m).contexts[0].kind).toBe("edit");
  });

  it("names a whole document （整篇）", () => {
    expect(newContext("context", "T", "u", "  ").text).toBe(WHOLE);
    expect(newContext("edit", "T", "u", " x ").text).toBe("x");
  });
});

describe("contexts waiting in a composer", () => {
  it("adds once per passage and removes by id", () => {
    const a = newContext("context", "T", "https://x", "one");
    addContext("k1", a);
    addContext("k1", { ...a, id: "other" }); // the same passage again
    expect(getContexts("k1")).toHaveLength(1);
    const b = newContext("edit", "T", "https://x", "one");
    addContext("k1", b);
    expect(getContexts("k1").map((r) => r.kind)).toEqual(["context", "edit"]);
    expect(getContexts("k2")).toEqual([]);
    removeContext("k1", a.id);
    removeContext("k1", b.id);
    expect(getContexts("k1")).toEqual([]);
  });

  it("knows the composer last mounted or focused, and hands waiting work to the one that mounts", () => {
    const sent: string[] = [];
    const one = { key: "one", send: async (t: string) => (sent.push(`one:${t}`), true), focus() {} };
    const two = { key: "two", send: async (t: string) => (sent.push(`two:${t}`), true), focus() {} };
    const offOne = registerComposer(one);
    const offTwo = registerComposer(two);
    expect(activeComposer()).toBe(two);
    offTwo();
    expect(activeComposer()).toBe(one);
    whenComposer("two", (c) => void c.send("later"));
    expect(sent).toEqual([]);
    const offTwoAgain = registerComposer(two);
    expect(sent).toEqual(["two:later"]);
    offTwoAgain();
    offOne();
    expect(activeComposer()).toBeUndefined();
  });

  it("passes over a composer that can't send, and reports work no composer took", () => {
    vi.useFakeTimers();
    const readOnly = { key: "ro", send: async () => false, focus() {}, ready: () => false };
    const off = registerComposer(readOnly);
    expect(activeComposer()).toBe(readOnly);
    expect(usableComposer()).toBeUndefined();
    let expired = false;
    whenComposer("never", () => {}, () => (expired = true), 100);
    vi.advanceTimersByTime(150);
    expect(expired).toBe(true);
    off();
    vi.useRealTimers();
  });
});

describe("reading a Feishu document", () => {
  const url = "https://example.feishu.cn/docx/AbC";
  const fail = (status: number, body: Record<string, unknown>) => docErrorFromBody(status, body);

  it("uses the Markdown export when allowed", async () => {
    const get: Get = async (p) => {
      expect(p).toContain("format=markdown");
      return { title: "T", markdown: "# T\n\nbody", authority: { copy: false } };
    };
    expect(await readDoc(url, undefined, get)).toEqual({ kind: "markdown", title: "T", markdown: "# T\n\nbody", copy: false, partial: false });
  });

  it("falls back to the JSON tree when export is forbidden", async () => {
    const asked: string[] = [];
    const get: Get = async (p) => {
      asked.push(p.includes("markdown") ? "md" : "json");
      if (p.includes("markdown")) throw fail(403, { error: { code: "DOCUMENT_EXPORT_FORBIDDEN" } });
      return { title: "T", content: { type: "doc", content: [{ type: "paragraph" }] } };
    };
    const c = await readDoc(url, undefined, get);
    expect(asked).toEqual(["md", "json"]);
    expect(c.kind).toBe("tree");
  });

  it("takes a tree answered to the Markdown request without asking again", async () => {
    let n = 0;
    const get: Get = async () => (n++, { title: "T", content: { type: "doc", content: [] } });
    expect((await readDoc(url, undefined, get)).kind).toBe("tree");
    expect(n).toBe(1);
  });

  it("says what it can't show", async () => {
    expect((await readDoc("https://example.feishu.cn/base/AbC", "表", async () => ({}))).kind).toBe("unsupported");
    const board = await readDoc("https://example.feishu.cn/wiki/AbC", undefined, async () => ({ title: "B", content: { type: "doc" }, object: { kind: "whiteboard", token: "t", image: "/api/v1/x" } }));
    expect(board).toMatchObject({ kind: "unsupported", doc: "whiteboard", image: "/api/v1/x" });
    const odd = await readDoc(url, "T", async () => {
      throw fail(422, { error: { code: "DOCUMENT_FORMAT_UNSUPPORTED" } });
    });
    expect(odd).toMatchObject({ kind: "unsupported", title: "T" });
  });

  it("explains failures", async () => {
    await expect(readDoc(url, undefined, async () => { throw fail(404, { error: { code: "DOCUMENT_NOT_FOUND" } }); })).rejects.toThrow("文档不存在或已被删除");
    expect(fail(401, { error: { code: "AUTH_SESSION_EXPIRED" } }).message).toContain("没有登录飞书");
    expect(fail(502, { error: "lark: the Lark gateway is not configured (set lark.token or lark.container in hub.json)", code: "upstream_error" }).message).toContain("没有接入飞书");
    expect(fail(403, { error: "forbidden", code: "forbidden" }).message).toBe("没有访问权限");
  });
});

describe("a document read as another manager (the chat's the link was in)", () => {
  it("is read, kept and read ahead apart from the default's, which names none", async () => {
    const doc = "https://example.feishu.cn/docx/Kept1";
    vi.mocked(api).mockImplementation(async () => ({ title: "T", markdown: "body" }));
    const named = () => vi.mocked(api).mock.calls.filter(([p]) => String(p).includes("Kept1")).map(([, init]) => (init?.headers as Record<string, string> | undefined)?.["X-Persona-Manager"]);
    try {
      await loadDoc(doc, undefined, false, "m-2");
      await loadDoc(doc, undefined, false, "m-2");
      await loadDoc(doc);
      await loadDoc(doc, undefined, false, "assistant");
      expect(named()).toEqual(["m-2", undefined]);
      prefetchLink(doc, "m-3");
      await loadDoc(doc, undefined, false, "m-3");
      expect(named()).toEqual(["m-2", undefined, "m-3"]);
    } finally {
      vi.mocked(api).mockReset();
    }
  });

  it("links its media and the hub's files as that manager", () => {
    const md = "Body\n\n![pic](</api/v1/lark/raw/m?x=1>) [file](</api/v1/lark/raw/f>) [site](https://example.com/api/v1/x)";
    expect(prepareMarkdown(md, "", "k", "/persona", "m-2")).toBe("Body\n\n![pic](</persona/api/v1/lark/raw/m?x=1&manager=m-2&token=k>) [file](</persona/api/v1/lark/raw/f?manager=m-2>) [site](https://example.com/api/v1/x)");
    expect(prepareMarkdown(md, "", "k", "/persona", "assistant")).toBe(prepareMarkdown(md, "", "k", "/persona"));
    expect(mediaURL("http://127.0.0.1:3000/api/documents/media/abc", "m-2", "k")).toMatch(/\/api\/v1\/lark\/raw\/documents\/media\/abc\?manager=m-2&token=k$/);
    expect(mediaURL("http://127.0.0.1:3000/api/documents/media/abc", undefined, "k")).toMatch(/\/api\/v1\/lark\/raw\/documents\/media\/abc\?token=k$/);
    expect(mediaURL("https://example.com/pic.png", "m-2")).toBe("https://example.com/pic.png");
  });
});

describe("the Markdown export", () => {
  it("drops the title and source lines, moves media under the hub with the token, and turns <br> into breaks", () => {
    const md = "# 标题\n\n[Source](<https://example.feishu.cn/docx/x>)\n\n正文\n\n![图](<http://127.0.0.1:3000/api/documents/media/abc?x=1>)\n\n| a | b |\n| --- | --- |\n| 1<br>2 | 3 |\n\n```\nkeep <br> here\n```";
    const out = prepareMarkdown(md, "标题", "t k");
    expect(out.startsWith("正文")).toBe(true);
    expect(out).toContain("![图](</api/v1/lark/raw/documents/media/abc?x=1&token=t%20k>)");
    expect(out).toContain(`| 1${BR}2 | 3 |`);
    expect(out).toContain("keep <br> here");
  });

  it("keeps a first heading that is not the title", () => {
    expect(prepareMarkdown("# 另一个\n正文", "标题")).toBe("# 另一个\n正文");
  });

  it("puts the hub's media under the sub-path the page is served at", () => {
    const out = prepareMarkdown("正文\n\n![图](</api/v1/lark/raw/m?x=1>) [文件](</api/v1/lark/raw/f>) [站外](https://example.com/api/v1/x)", "", "k", "/persona");
    expect(out).toContain("![图](</persona/api/v1/lark/raw/m?x=1&token=k>)");
    expect(out).toContain("[文件](</persona/api/v1/lark/raw/f>)");
    expect(out).toContain("[站外](https://example.com/api/v1/x)");
  });
});

describe("the JSON tree", () => {
  it("keeps counting one-item ordered lists until a restart", () => {
    const ol = (attrs?: Record<string, unknown>): DocNode => ({ type: "orderedList", attrs, content: [{ type: "listItem" }] });
    expect(orderedNumbers([ol(), ol(), { type: "paragraph" }, ol(), ol({ restart: true, start: 5 }), ol()])).toEqual([1, 2, undefined, 1, 5, 6]);
  });
});
