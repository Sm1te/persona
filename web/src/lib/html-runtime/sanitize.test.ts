// The page's checks on an html card's drawing (#4215, docs/assistant/assistant-views.md "Security boundaries"): the
// display frame's sandbox, CSP and nonce, and what the sanitizer lets through to it.
import { readFileSync } from "node:fs";
import { describe, expect, it, vi } from "vitest";
import { frameMessageFits, guestEvent } from "./host";
import { cardHeight, FRAME_MIN, frameHeight, HEIGHT_DEFAULT, HEIGHT_MAX } from "./height";
import { CSS_BYTES_MOST, NODES_MOST } from "./limits";
import { BODY_ID, HEAD_ID } from "./protocol";
import { drawnAttr, FRAME_SANDBOX, frameCsp, frameNonce, frameShell, LimitError, safeCss, Sanitizer } from "./sanitize";

describe("the display frame", () => {
  it("is sandboxed with allow-scripts and nothing else", () => {
    expect(FRAME_SANDBOX).toBe("allow-scripts");
    const tokens = FRAME_SANDBOX.split(/\s+/);
    for (const never of ["allow-same-origin", "allow-forms", "allow-popups", "allow-top-navigation", "allow-modals", "allow-popups-to-escape-sandbox", "allow-top-navigation-by-user-activation", "allow-downloads", "allow-pointer-lock", "allow-presentation", "allow-storage-access-by-user-activation"]) {
      expect(tokens).not.toContain(never);
    }
    // HtmlCard draws its iframe with exactly this, and runCard refuses any other
    const card = readFileSync(new URL("../../ui/HtmlCard.tsx", import.meta.url), "utf8");
    expect(card).toContain("sandbox={FRAME_SANDBOX}");
    expect(card.match(/sandbox=/g)).toHaveLength(1);
    expect(readFileSync(new URL("./host.ts", import.meta.url), "utf8")).toContain('o.frame.getAttribute("sandbox") !== FRAME_SANDBOX');
  });

  it("has its CSP first in its head: nothing loads or connects, only its own script runs", () => {
    const html = frameShell(false, "N0nceN0nceN0nce0", "/*frame*/");
    expect(html.startsWith(`<!doctype html><html><head><meta http-equiv="Content-Security-Policy" content="${frameCsp("N0nceN0nceN0nce0")}">`)).toBe(true);
    const csp = Object.fromEntries(frameCsp("N0nce").split("; ").map((d) => [d.split(" ")[0], d.slice(d.indexOf(" ") + 1)]));
    expect(csp).toEqual({
      "default-src": "'none'",
      "script-src": "'nonce-N0nce'",
      "style-src": "'unsafe-inline'",
      "img-src": "data:",
      "font-src": "'none'",
      "media-src": "'none'",
      "connect-src": "'none'",
      "frame-src": "'none'",
      "child-src": "'none'",
      "worker-src": "'none'",
      "object-src": "'none'",
      "manifest-src": "'none'",
      "base-uri": "'none'",
      "form-action": "'none'",
    });
  });

  it("is a fixed template: the only script is the frame's own, with the nonce, at the end of an empty body", () => {
    const nonce = "abcdefghijklmnop";
    const html = frameShell(true, nonce, "console.log(1)");
    expect(html.match(/<script/g)).toHaveLength(1);
    expect(html.endsWith(`</style></head><body><script nonce="${nonce}">console.log(1)</script></body></html>`)).toBe(true);
    // nothing in the script may end it or open another; the nonce is the generated kind
    for (const bad of ["x</script><script>evil()", "x<script>", "x<!--"]) expect(() => frameShell(true, nonce, bad)).toThrow();
    expect(() => frameShell(true, 'a" onload="x', "x")).toThrow();
    // the real frame script passes
    expect(() => frameShell(false, frameNonce(), readFileSync(new URL("./frame.js", import.meta.url), "utf8"))).not.toThrow();
    // the card's HTML is never part of it: frameShell takes none
    expect(frameShell.length).toBe(3);
  });

  it("gets a new nonce of at least 128 random bits each time, from crypto.getRandomValues", () => {
    const spy = vi.spyOn(crypto, "getRandomValues");
    const a = frameNonce();
    expect(spy).toHaveBeenCalledTimes(1);
    expect((spy.mock.calls[0][0] as Uint8Array).length * 8).toBeGreaterThanOrEqual(128);
    expect(a).toMatch(/^[A-Za-z0-9_-]{24}$/);
    expect(new Set(Array.from({ length: 50 }, frameNonce)).size).toBe(50);
    spy.mockRestore();
  });

  it("builds the DOM only with createElement, setAttribute and textContent: no HTML parsing of any kind", () => {
    const src = readFileSync(new URL("./frame.js", import.meta.url), "utf8").replace(/\/\/.*$/gm, "");
    for (const never of ["innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "DOMParser", "createContextualFragment", "eval(", "Function(", "srcdoc", "setTimeout(", "location"]) {
      expect(src).not.toContain(never);
    }
    // it takes messages from its parent only, carrying its nonce
    expect(src).toContain("e.source !== P || !m || m.n !== N");
  });
});

/** A tree as the guest sends it: [id, tag, attrs, kids, ns, props]. */
const el = (id: number, tag: string, attrs: [string, string][] = [], kids: unknown[] = [], ns = "", props: Record<string, unknown> = {}) => [id, tag, attrs, kids, ns, props];
const text = (id: number, t: string) => [id, "#text", t];
const insert = (tree: unknown[], parent = BODY_ID) => ["t", parent, 0, tree];

describe("the sanitizer", () => {
  it("draws what it allows and drops the rest, with everything under it", () => {
    const s = new Sanitizer();
    const out = s.clean([
      insert(el(10, "div", [["class", "a"], ["onclick", "x()"], ["style", "color: red"]], [text(11, "hi"), el(12, "script", [], [text(13, "alert(1)")])])),
      insert(el(20, "iframe", [["src", "https://example.com"]], [el(21, "p")])),
      insert(el(30, "meta", [["http-equiv", "refresh"], ["content", "0;url=https://example.com"]]), HEAD_ID),
      insert(el(31, "base", [["href", "https://example.com/"]]), HEAD_ID),
      insert(el(32, "link", [["rel", "prefetch"], ["href", "https://example.com"]]), HEAD_ID),
      insert(el(33, "object", [["data", "https://example.com"]])),
      insert(el(34, "embed", [["src", "https://example.com"]])),
    ]);
    expect(out).toEqual([["t", BODY_ID, 0, [10, "div", "", [["class", "a"], ["style", "color: red"]], [[11, "#text", "hi"]], {}]]]);
    // the dropped nodes are unknown: what names them does nothing
    expect(s.clean([["a", 20, "src", "https://example.com"], ["t", 12, 0, text(40, "x")], ["d", 13, "y"], ["r", 21]])).toEqual([]);
  });

  it("never draws meta, base, link, iframe, object or embed, in the head or the body, whatever they carry", () => {
    const tags: [string, [string, string][]][] = [
      ["meta", [["http-equiv", "refresh"], ["content", "0;url=https://example.com/"]]],
      ["meta", [["http-equiv", "Content-Security-Policy"], ["content", "default-src *"]]],
      ["meta", [["name", "viewport"], ["content", "width=device-width"]]],
      ["base", [["href", "https://example.com/"]]],
      ["base", [["target", "_blank"]]],
      ...["stylesheet", "prefetch", "preload", "dns-prefetch", "preconnect", "modulepreload", "icon", "manifest"].map((rel) => ["link", [["rel", rel], ["href", "https://example.com/x"]]] as [string, [string, string][]]),
      ["iframe", [["src", "https://example.com/"]]],
      ["iframe", [["srcdoc", "<script>x()</script>"]]],
      ["object", [["data", "https://example.com/"], ["type", "text/html"]]],
      ["embed", [["src", "https://example.com/"], ["type", "image/svg+xml"]]],
      ["frame", [["src", "https://example.com/"]]],
      ["frameset", []],
      ["portal", [["src", "https://example.com/"]]],
      ["noscript", []],
      ["template", []],
      ["slot", []],
    ];
    for (const [tag, attrs] of tags) {
      for (const parent of [HEAD_ID, BODY_ID]) {
        for (const name of [tag, tag.toUpperCase()]) {
          const s = new Sanitizer();
          expect(s.clean([insert(el(10, name, attrs, [text(11, "x")]), parent)]), `${name} in ${parent === HEAD_ID ? "head" : "body"}`).toEqual([]);
          // nor nested inside something that is drawn, nor in svg
          expect(s.clean([insert(el(20, "div", [], [el(21, name, attrs)]), parent)])).toEqual([["t", parent, 0, [20, "div", "", [], [], {}]]]);
          expect(new Sanitizer().clean([insert(el(30, "svg", [], [el(31, name, attrs, [], "svg")], "svg"), parent)])).toEqual([["t", parent, 0, [30, "svg", "svg", [], [], {}]]]);
        }
      }
    }
  });

  it("never lets a URL that loads anything through", () => {
    const a = (tag: string, name: string, value: string, ns = "") => drawnAttr(tag, ns, name, value);
    expect(a("a", "href", "javascript:alert(1)")).toBeNull();
    expect(a("a", "href", "https://example.com")).toBeNull();
    expect(a("img", "src", "https://example.com/x.png")).toBeNull();
    expect(a("img", "srcset", "https://example.com/x.png 1x")).toBeNull();
    expect(a("img", "src", "data:image/svg+xml;base64,PHN2Zz4=")).toBeNull();
    expect(a("img", "src", "data:image/png;base64,iVBORw0KGgo=")).toBe("data:image/png;base64,iVBORw0KGgo=");
    expect(a("button", "formaction", "https://example.com")).toBeNull();
    expect(a("div", "background", "https://example.com/x.png")).toBeNull();
    expect(a("div", "onmouseover", "x()")).toBeNull();
    expect(a("input", "type", "password")).toBeNull();
    expect(a("input", "type", "file")).toBeNull();
    expect(a("input", "type", "image")).toBeNull();
    expect(a("image", "href", "https://example.com/x.png", "svg")).toBeNull();
    expect(a("rect", "fill", "url(https://example.com/x)", "svg")).toBeNull();
    expect(a("rect", "fill", "url(#grad)", "svg")).toBe("url(#grad)");
    expect(a("rect", "style", "fill: url(https://example.com/x)", "svg")).toBe("fill: none");
  });

  it("draws no form, no link target, no password field, and turns off autofill", () => {
    const out = new Sanitizer().clean([
      insert(el(10, "form", [["action", "https://example.com"], ["method", "post"]], [el(11, "input", [["type", "password"], ["name", "p"], ["autocomplete", "current-password"]]), el(12, "a", [["href", "https://example.com"], ["target", "_blank"]], [text(13, "go")])])),
    ]);
    expect(out).toEqual([
      ["t", BODY_ID, 0, [10, "div", "", [["role", "form"]], [[11, "input", "", [["autocomplete", "off"], ["name", "p"]], [], {}], [12, "a", "", [], [[13, "#text", "go"]], {}]], {}]],
    ]);
  });

  it("drops svg elements that load or embed, and svg outside an svg", () => {
    const out = new Sanitizer().clean([
      insert(el(10, "svg", [["viewBox", "0 0 10 10"]], [el(11, "image", [["href", "https://example.com/x.png"]], [], "svg"), el(12, "use", [["href", "https://example.com/s.svg#a"]], [], "svg"), el(13, "foreignObject", [], [el(14, "div")], "svg"), el(15, "rect", [["width", "5"]], [], "svg")], "svg")),
      insert(el(20, "rect", [], [], "svg")),
    ]);
    expect(out).toEqual([["t", BODY_ID, 0, [10, "svg", "svg", [["viewBox", "0 0 10 10"]], [[15, "rect", "svg", [["width", "5"]], [], {}]], {}]]]);
  });

  it("takes from CSS what could load, however it is written", () => {
    expect(safeCss("a{background:url(https://example.com/x)}", false)).toBe("a{background:none}");
    expect(safeCss("@import url(https://example.com/x.css); a{color:red}", false)).toBe(" a{color:red}");
    expect(safeCss("@import 'https://example.com/x.css'; a{color:red}", false)).toBe(" a{color:red}");
    expect(safeCss("@font-face{font-family:x;src:url(https://example.com/f.woff)} a{color:red}", false)).toBe(" a{color:red}");
    expect(safeCss("a{background:\\75 rl(https://example.com/x)}", false)).toBe("a{background:none}");
    expect(safeCss("a{background:u\\rl(https://example.com/x)}", false)).toBe("a{background:none}");
    // a comment splits tokens: "u rl(" is no url() to a browser either
    expect(safeCss("a{background:u/**/rl(https://example.com/x)}", false)).not.toMatch(/url\s*\(/i);
    expect(safeCss("a{background-image:image-set('https://example.com/x' 1x)}", false)).not.toMatch(/example\.com'/);
    expect(safeCss("a{-webkit-text-security:disc}", false)).not.toContain("text-security");
    expect(safeCss("color: red; background: url(#g)", true)).toBe("color: red; background: url(#g)");
    // an inline style can't close its declaration block and open a rule
    expect(safeCss("color:red}b{color:blue", true)).toBe("color:redbcolor:blue");
  });

  it("checks a style element's sheet as a whole, whenever its text changes", () => {
    const s = new Sanitizer();
    expect(s.clean([insert(el(10, "style", [], [text(11, "a{color:red}")]), HEAD_ID)])).toEqual([["t", HEAD_ID, 0, [10, "style", "", [], [[0, "#css", "a{color:red}"]], {}]]]);
    // split across text nodes, @import still never gets through
    expect(s.clean([["t", 10, 0, text(12, "@imp")], ["t", 10, 0, text(13, "ort url(https://example.com/x.css);")]])).toEqual([
      ["s", 10, "a{color:red}@imp"],
      ["s", 10, "a{color:red}"],
    ]);
    expect(s.clean([["d", 11, "b{background:url(https://example.com/x)}"]])).toEqual([["s", 10, "b{background:none}"]]);
  });

  it("sets only the controls' own state, and only on the elements that have it", () => {
    const s = new Sanitizer();
    s.clean([insert(el(10, "input", [["type", "checkbox"]])), insert(el(11, "div"))]);
    expect(s.clean([["p", 10, "checked", 1], ["p", 10, "value", 5], ["p", 11, "value", "x"], ["p", 10, "innerHTML", "<img>"], ["p", 10, "onclick", "x"]])).toEqual([
      ["p", 10, "checked", true],
      ["p", 10, "value", "5"],
    ]);
  });

  it("stops a card that draws past its bounds", () => {
    const s = new Sanitizer();
    const many = Array.from({ length: NODES_MOST + 1 }, (_, i) => insert(el(100 + i, "span")));
    expect(() => s.clean(many.slice(0, 4000))).not.toThrow();
    expect(() => s.clean(many.slice(4000))).toThrow(LimitError);
    expect(() => new Sanitizer().clean([insert(el(10, "style", [], [text(11, `a{color:red}${" ".repeat(CSS_BYTES_MOST)}`)]))])).toThrow(LimitError);
    expect(() => new Sanitizer().clean(Array.from({ length: 5001 }, () => ["f", 3]))).toThrow(LimitError);
  });

  it("forgets a removed subtree, so its ids are free and its counts given back", () => {
    const s = new Sanitizer();
    s.clean([insert(el(10, "div", [], [el(11, "p", [], [text(12, "abc")])]))]);
    expect(s.counts).toEqual({ nodes: 3, text: 3, css: 0 });
    expect(s.clean([["r", 10]])).toEqual([["r", 10]]);
    expect(s.counts).toEqual({ nodes: 0, text: 0, css: 0 });
    expect(s.has(11)).toBe(false);
    // the document's own nodes stay
    expect(s.clean([["r", BODY_ID]])).toEqual([]);
  });
});

describe("what the frame tells the page", () => {
  const known = { has: (id: number) => id === 10 };
  it("is an allowed event on a node the frame has, its fields with their types", () => {
    expect(guestEvent({ type: "click", target: 10 }, known)).toEqual({ type: "click", target: 10 });
    expect(guestEvent({ type: "input", target: 10, value: "x".repeat(70_000), checked: "yes", key: 5 }, known)).toEqual({ type: "input", target: 10, value: "x".repeat(65_536) });
    expect(guestEvent({ type: "submit", target: 10 }, known)).toBeNull();
    expect(guestEvent({ type: "click", target: 11 }, known)).toBeNull();
    expect(guestEvent({ type: "click", target: "10" }, known)).toEqual({ type: "click", target: 10 });
    expect(guestEvent(null, known)).toBeNull();
  });
  it("has only the keys frame.js sends, within the size", () => {
    expect(frameMessageFits({ n: "a", ev: { type: "click", target: 1 } })).toBe(true);
    expect(frameMessageFits({ n: "a", ready: true })).toBe(true);
    expect(frameMessageFits({ n: "a", ev: { type: "click", target: 1, token: "x" } })).toBe(false);
    expect(frameMessageFits({ n: "a", ops: [] })).toBe(false);
    expect(frameMessageFits({ n: "a", ev: { type: "input", target: 1, value: "x".repeat(80 * 1024) } })).toBe(false);
    // how tall the card's content is: a number, nothing else with it
    expect(frameMessageFits({ n: "a", h: 120 })).toBe(true);
    expect(frameMessageFits({ n: "a", h: "120" })).toBe(false);
    expect(frameMessageFits({ n: "a", h: 120, top: 0 })).toBe(false);
  });
  it("sizes the frame to the card's content, between its least and the component's height", () => {
    expect(frameHeight(12, cardHeight(undefined))).toBe(FRAME_MIN);
    expect(frameHeight(88.2, cardHeight(undefined))).toBe(89);
    expect(frameHeight(5000, cardHeight(undefined))).toBe(HEIGHT_DEFAULT);
    expect(frameHeight(5000, cardHeight(600))).toBe(600);
    expect(frameHeight(5000, cardHeight(9999))).toBe(HEIGHT_MAX);
    // frame.js reports its body's height whenever it changes, once a frame at most
    const src = readFileSync(new URL("./frame.js", import.meta.url), "utf8");
    expect(src).toContain("new ResizeObserver");
    expect(src).toContain("requestAnimationFrame(reportHeight)");
    expect(src).toContain("post({ h: h })");
  });
});
