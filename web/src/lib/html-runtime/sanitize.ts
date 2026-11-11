// The page's side of an html card's drawing (#4215, docs/assistant/assistant-views.md "Security boundaries"): every
// operation the card's worker sends is checked here before it reaches the display frame. Only allowlisted elements
// and attributes pass, and no URL that could load anything gets through: no href, no src but an inline image, no
// url() in CSS but a reference inside the card. The frame draws them with createElement / setAttribute / textContent
// (never innerHTML). The frame's own CSP (default-src 'none') and its sandbox are the next layer; this one must hold
// without them.
import { CANVAS_AREA_MOST, CANVAS_SIDE_MOST, CSS_BYTES_MOST, IMAGE_BYTES_MOST, NODES_MOST, OPS_PER_BATCH_MOST, TEXT_BYTES_MOST } from "./limits";
import { BODY_ID, HEAD_ID, HTML_ID, type Op } from "./protocol";


/** HTML elements drawn as they are. */
const HTML_TAGS = new Set([
  "div", "span", "p", "br", "hr", "h1", "h2", "h3", "h4", "h5", "h6", "main", "section", "article", "aside", "header", "footer", "nav",
  "figure", "figcaption", "blockquote", "pre", "code", "kbd", "samp", "var", "small", "strong", "b", "em", "i", "u", "s", "mark",
  "sub", "sup", "abbr", "cite", "q", "time", "data", "del", "ins", "dfn", "bdi", "bdo", "wbr", "ul", "ol", "li", "dl", "dt", "dd",
  "table", "caption", "thead", "tbody", "tfoot", "tr", "th", "td", "colgroup", "col", "details", "summary", "button", "input",
  "select", "option", "optgroup", "textarea", "label", "fieldset", "legend", "output", "progress", "meter", "canvas", "img",
  "style", "center", "font", "address", "a",
]);
/** HTML elements drawn as another: a form is a div (it can't submit anywhere; the card's code sees its submit event). */
const HTML_AS: Record<string, string> = { form: "div" };

/** SVG elements drawn (no image, use, foreignObject, a, script, animation with href, filters that load). */
const SVG_TAGS = new Set([
  "svg", "g", "path", "rect", "circle", "ellipse", "line", "polyline", "polygon", "text", "tspan", "textPath", "defs", "title", "desc",
  "linearGradient", "radialGradient", "stop", "clipPath", "mask", "pattern", "marker", "symbol",
  "filter", "feGaussianBlur", "feOffset", "feBlend", "feColorMatrix", "feComposite", "feFlood", "feMerge", "feMergeNode", "feDropShadow",
  "animate", "animateTransform", "set",
]);

const INPUT_TYPES = new Set(["text", "number", "range", "checkbox", "radio", "date", "time", "datetime-local", "month", "week", "color", "search", "email", "tel", "url", "button", "submit", "reset"]);

/** Attributes any HTML element may carry; per-element ones below. */
const HTML_ATTRS = new Set(["id", "class", "style", "title", "lang", "dir", "role", "tabindex", "hidden", "translate", "draggable", "spellcheck"]);
const ELEMENT_ATTRS: Record<string, Set<string>> = {
  input: new Set(["type", "value", "checked", "disabled", "readonly", "placeholder", "min", "max", "step", "name", "maxlength", "minlength", "size", "required", "list"]),
  button: new Set(["type", "disabled", "name", "value"]),
  select: new Set(["disabled", "multiple", "name", "size", "required"]),
  option: new Set(["value", "selected", "disabled", "label"]),
  optgroup: new Set(["label", "disabled"]),
  textarea: new Set(["disabled", "readonly", "placeholder", "rows", "cols", "name", "maxlength", "minlength", "required", "wrap"]),
  label: new Set(["for"]),
  fieldset: new Set(["disabled", "name"]),
  output: new Set(["for", "name"]),
  progress: new Set(["value", "max"]),
  meter: new Set(["value", "min", "max", "low", "high", "optimum"]),
  details: new Set(["open"]),
  ol: new Set(["start", "reversed", "type"]),
  li: new Set(["value"]),
  td: new Set(["colspan", "rowspan", "headers", "align", "valign"]),
  th: new Set(["colspan", "rowspan", "headers", "scope", "abbr", "align", "valign"]),
  col: new Set(["span"]),
  colgroup: new Set(["span"]),
  table: new Set(["border", "cellpadding", "cellspacing"]),
  canvas: new Set(["width", "height"]),
  img: new Set(["src", "alt", "width", "height"]),
  time: new Set(["datetime"]),
  data: new Set(["value"]),
  font: new Set(["color", "size", "face"]),
  q: new Set([]),
  a: new Set([]),
};

/** SVG presentation and geometry attributes. Any value with url( must be a reference inside the card: url(#id). */
const SVG_ATTRS = new Set([
  "id", "class", "style", "viewBox", "preserveAspectRatio", "width", "height", "x", "y", "x1", "y1", "x2", "y2", "cx", "cy", "r", "rx", "ry",
  "fx", "fy", "d", "points", "transform", "fill", "fill-opacity", "fill-rule", "stroke", "stroke-width", "stroke-opacity", "stroke-linecap",
  "stroke-linejoin", "stroke-dasharray", "stroke-dashoffset", "stroke-miterlimit", "opacity", "font-size", "font-family", "font-weight",
  "font-style", "text-anchor", "dominant-baseline", "alignment-baseline", "letter-spacing", "dx", "dy", "rotate", "textLength", "lengthAdjust",
  "offset", "stop-color", "stop-opacity", "gradientUnits", "gradientTransform", "spreadMethod", "patternUnits", "patternTransform",
  "patternContentUnits", "clip-path", "clipPathUnits", "clip-rule", "mask", "maskUnits", "maskContentUnits", "marker-start", "marker-mid",
  "marker-end", "markerWidth", "markerHeight", "markerUnits", "refX", "refY", "orient", "visibility", "display", "vector-effect", "pathLength",
  "role", "tabindex", "filter", "filterUnits", "primitiveUnits", "stdDeviation", "in", "in2", "result", "mode", "values", "type", "operator",
  "k1", "k2", "k3", "k4", "flood-color", "flood-opacity", "attributeName", "attributeType", "from", "to", "by", "dur", "begin", "end",
  "repeatCount", "repeatDur", "keyTimes", "keySplines", "calcMode", "additive", "accumulate", "restart", "xml:space", "startOffset", "method",
  "spacing", "side",
]);

/** An element the card may not draw here, and nothing under it: script, iframe, object, link, meta, base, and the rest. */
export function drawnTag(tag: string, ns: string): { tag: string; ns: string } | null {
  if (ns === "svg") return SVG_TAGS.has(tag) ? { tag, ns } : null;
  const t = tag.toLowerCase();
  if (HTML_TAGS.has(t)) return { tag: t, ns: "" };
  if (t in HTML_AS) return { tag: HTML_AS[t], ns: "" };
  return null;
}

const isAria = (n: string) => /^aria-[a-z]+$/.test(n);
const isData = (n: string) => /^data-[a-z0-9_.-]+$/.test(n) && n.length <= 64;
const NUMBER = /^\s*-?\d+(\.\d+)?\s*%?\s*$/;
const INLINE_IMAGE = /^data:image\/(?:png|gif|jpeg|webp);base64,[a-z0-9+/=\s]+$/i;
/** url(#id) and nothing else: a reference inside the card, which loads nothing. */
const LOCAL_URL = /url\(\s*(['"]?)#[\w-]+\1\s*\)/gi;

/**
 * The value an attribute is drawn with, or null to leave it off. Event handlers (on…) never reach the page: the card's
 * runtime runs them. A link's href stays with the card's runtime too (a press asks to open it, through persona.open).
 */
export function drawnAttr(tag: string, ns: string, name: string, value: string): string | null {
  if (value.length > 64 * 1024) return null;
  if (ns === "svg") {
    if (!SVG_ATTRS.has(name)) return isAria(name) || isData(name) ? value : null;
    if (name === "style") return safeCss(value, true);
    return hasUrl(value.replace(LOCAL_URL, "")) ? null : value;
  }
  const n = name.toLowerCase();
  if (n.startsWith("on") || n === "href" || n === "srcset" || n === "action" || n === "formaction" || n === "poster" || n === "background") return null;
  if (!(HTML_ATTRS.has(n) || isAria(n) || isData(n) || ELEMENT_ATTRS[tag]?.has(n))) return null;
  if (n === "style") return safeCss(value, true);
  if (n === "src") return tag === "img" && value.length <= IMAGE_BYTES_MOST && INLINE_IMAGE.test(value) ? value : null;
  if (n === "type" && tag === "input") return INPUT_TYPES.has(value.toLowerCase()) ? value.toLowerCase() : null;
  if ((n === "width" || n === "height") && tag === "canvas") return canvasSide(value);
  if ((n === "width" || n === "height") && !NUMBER.test(value)) return null;
  if (n === "tabindex" && !/^-?\d{1,3}$/.test(value.trim())) return null;
  // none of these attributes is a URL the browser loads, whatever its text says
  return value;
}

/** A canvas side the page will make: a whole number of pixels, within the bounds. */
export function canvasSide(v: string): string | null {
  const n = Number(v.trim());
  return Number.isInteger(n) && n >= 0 && n <= CANVAS_SIDE_MOST ? String(n) : null;
}
export const canvasFits = (w: number, h: number) => w <= CANVAS_SIDE_MOST && h <= CANVAS_SIDE_MOST && w * h <= CANVAS_AREA_MOST;

const hasUrl = (s: string) => /url\s*\(|\bsrc\s*\(|image-set|image\s*\(|cross-fade|element\s*\(|@import|javascript:|expression\s*\(/i.test(s);

/** CSS escapes undone (\75 rl, \(), so what is checked is what the browser will read; comments dropped. */
function plainCss(css: string): string {
  let out = css.replace(/\/\*[\s\S]*?(\*\/|$)/g, " ");
  out = out.replace(/\\([0-9a-fA-F]{1,6})\s?|\\([^\n0-9a-fA-F])/g, (_, hex: string | undefined, ch: string | undefined) => {
    if (hex) {
      const cp = parseInt(hex, 16);
      return cp > 0 && cp <= 0x10ffff && !(cp >= 0xd800 && cp <= 0xdfff) ? String.fromCodePoint(cp) : "�";
    }
    return ch ?? "";
  });
  // a backslash left (one before a newline, or at the end) is dropped: no escape reaches the browser
  return out.replace(/\\/g, "");
}

/**
 * CSS the card may use, inline (a style attribute) or a sheet (a style element). What could load anything is taken
 * out: @import, @font-face, url() other than url(#id), image-set() and the like; -webkit-text-security (a password
 * field look) too. If anything like that is still there afterwards, nothing of it is kept.
 */
export function safeCss(css: string, inline: boolean): string {
  let out = plainCss(css);
  out = out.replace(/@import[^;{}]*(;|$)/gi, "").replace(/@font-face\s*\{[^}]*\}?/gi, "").replace(/@namespace[^;{}]*(;|$)/gi, "");
  out = out.replace(/-webkit-text-security\s*:[^;}]*/gi, "");
  out = out.replace(/url\s*\(\s*(['"]?)(?!#[\w-]+\1\s*\))[^)]*\)?/gi, "none");
  out = out.replace(/(?:-webkit-)?image-set\s*\(|cross-fade\s*\(|element\s*\(|image\s*\(|\bsrc\s*\(/gi, "none(");
  if (hasUrl(out.replace(LOCAL_URL, "")) || /@import|@font-face|text-security|behavior\s*:|-moz-binding/i.test(out)) return "";
  return inline ? out.replace(/[{}]/g, "") : out;
}

export class LimitError extends Error {}

/** The props a control's state may be set by (value, checked…), on the elements that have it. */
const PROPS: Record<string, Set<string>> = {
  value: new Set(["input", "textarea", "select", "option", "progress", "meter", "output"]),
  checked: new Set(["input"]),
  indeterminate: new Set(["input"]),
  selected: new Set(["option"]),
  open: new Set(["details"]),
  selectedIndex: new Set(["select"]),
};
function cleanProp(tag: string, name: string, value: unknown): [string, unknown] | null {
  if (!PROPS[name]?.has(tag)) return null;
  if (name === "value") return [name, String(value ?? "").slice(0, 64 * 1024)];
  if (name === "selectedIndex") return [name, Number(value) | 0];
  return [name, Boolean(value)];
}

/**
 * What the display frame is told to draw (frame.js applies these as they are):
 *  ["t", parent, before, tree]  tree: [id, "#text", text] | [id, tag, ns, attrs [[name, value]…], kids […], props {…}]
 *  ["r", id]  ["a", id, name, value | null]  ["d", id, text]  ["p", id, name, value]  ["s", styleId, css]  ["f", id]
 */
export type Clean = unknown[];

type Known = { tag: string; ns: string; parent: number; kids: Set<number>; text?: string };

/**
 * Checks the guest's operations and turns them into what the frame may draw. It keeps its own record of the card's
 * nodes (ids, tags, parents): a node it didn't let through (a script, an iframe…) is unknown, and so is everything
 * under it, so operations naming it do nothing. Only allowlisted elements and attributes pass; a style element's
 * text is never drawn as it comes, its sheet is the text checked again as a whole whenever it changes. Counts what
 * is drawn and throws LimitError past the bounds.
 */
export class Sanitizer {
  private nodes = new Map<number, Known>();
  private sheets = new Map<number, number[]>();
  private nodeCount = 0;
  private textBytes = 0;
  private cssBytes = new Map<number, number>();

  constructor() {
    for (const [id, tag, parent] of [[HTML_ID, "html", 0], [HEAD_ID, "head", HTML_ID], [BODY_ID, "body", HTML_ID]] as const) this.nodes.set(id, { tag, ns: "", parent, kids: new Set() });
  }

  get counts() {
    let css = 0;
    for (const v of this.cssBytes.values()) css += v;
    return { nodes: this.nodeCount, text: this.textBytes, css };
  }

  /** Whether the frame has this node (an event's target must be one). */
  has(id: number): boolean {
    return this.nodes.has(id);
  }

  tagOf(id: number): string | undefined {
    return this.nodes.get(id)?.tag;
  }

  clean(ops: unknown): Clean[] {
    if (!Array.isArray(ops)) throw new LimitError("ops: not a list");
    if (ops.length > OPS_PER_BATCH_MOST) throw new LimitError("ops");
    const out: Clean[] = [];
    for (const op of ops) if (Array.isArray(op)) this.one(op as Op, out);
    return out;
  }

  private one(op: Op, out: Clean[]) {
    switch (op[0]) {
      case "t": {
        const parent = Number(op[1]);
        const p = this.nodes.get(parent);
        if (!p || !Array.isArray(op[3])) return;
        const tree = this.build(op[3] as unknown[], parent, p);
        // text added to a style element: its sheet, checked again (the text itself is never drawn)
        if (p.tag === "style") return this.restyle(parent, out);
        if (!tree) return;
        const before = Number(op[2]) || 0;
        out.push(["t", parent, before && this.nodes.get(before)?.parent === parent ? before : 0, tree]);
        return;
      }
      case "r": {
        const id = Number(op[1]);
        const r = this.nodes.get(id);
        if (!r || id <= BODY_ID) return;
        this.forget(id);
        if (this.nodes.get(r.parent)?.tag === "style") return this.restyle(r.parent, out);
        out.push(["r", id]);
        return;
      }
      case "a": {
        const id = Number(op[1]);
        const name = op[2];
        const r = this.nodes.get(id);
        if (!r || r.tag === "#text" || typeof name !== "string" || name === "autocomplete") return;
        if (op[3] === null || op[3] === undefined) {
          if (name === "style") this.cssBytes.delete(-id);
          if (/^[a-zA-Z][\w:.-]*$/.test(name)) out.push(["a", id, name, null]);
          return;
        }
        const v = this.attr(r, id, name, String(op[3]));
        if (v !== null) out.push(["a", id, name, v]);
        return;
      }
      case "d": {
        const id = Number(op[1]);
        const r = this.nodes.get(id);
        if (!r || r.tag !== "#text") return;
        const text = String(op[2] ?? "");
        this.countText(r.text ?? "", text);
        r.text = text;
        if (this.nodes.get(r.parent)?.tag === "style") return this.restyle(r.parent, out);
        out.push(["d", id, text]);
        return;
      }
      case "p": {
        const id = Number(op[1]);
        const r = this.nodes.get(id);
        const p = r && cleanProp(r.tag, String(op[2]), op[3]);
        if (p) out.push(["p", id, ...p]);
        return;
      }
      case "f": {
        const id = Number(op[1]);
        if (this.nodes.get(id)?.tag !== "#text" && this.nodes.has(id)) out.push(["f", id]);
        return;
      }
    }
  }

  private attr(r: Known, id: number, name: string, value: string): string | null {
    const v = drawnAttr(r.tag, r.ns, name, value);
    if (v !== null && name === "style") this.countCss(-id, v.length);
    return v;
  }

  /** A subtree as the frame will draw it, or null when its root is one it doesn't draw. */
  private build(tree: unknown[], parent: number, p: Known): unknown[] | null {
    const id = Number(tree[0]);
    if (!Number.isSafeInteger(id) || id <= BODY_ID || this.nodes.has(id)) return null;
    if (tree[1] === "#text") {
      const text = String(tree[2] ?? "");
      this.countNode();
      this.countText("", text);
      this.adopt(id, { tag: "#text", ns: "", parent, kids: new Set(), text }, p);
      if (p.tag === "style") {
        this.sheets.set(parent, [...(this.sheets.get(parent) ?? []), id]);
        return null;
      }
      return [id, "#text", text];
    }
    if (p.tag === "style") return null;
    const tag = String(tree[1] ?? "");
    const drawn = drawnTag(tag, tree[4] === "svg" ? "svg" : "");
    if (!drawn) return null;
    // an svg element is drawn only inside an svg (an svg element itself may sit in HTML)
    if (drawn.ns === "svg" && drawn.tag !== "svg" && p.ns !== "svg") return null;
    this.countNode();
    const r: Known = { tag: drawn.tag, ns: drawn.ns, parent, kids: new Set() };
    this.adopt(id, r, p);
    const attrs: [string, string][] = [];
    if (drawn.tag === "input" || drawn.tag === "textarea" || drawn.tag === "select") attrs.push(["autocomplete", "off"]);
    if (tag.toLowerCase() === "form") attrs.push(["role", "form"]);
    for (const a of Array.isArray(tree[2]) ? tree[2] : []) {
      if (!Array.isArray(a) || typeof a[0] !== "string" || a[0] === "autocomplete") continue;
      const v = this.attr(r, id, a[0], String(a[1] ?? ""));
      if (v !== null) attrs.push([a[0], v]);
    }
    if (drawn.tag === "style") this.sheets.set(id, []);
    const kids: unknown[] = [];
    for (const c of Array.isArray(tree[3]) ? tree[3] : []) {
      if (!Array.isArray(c)) continue;
      const k = this.build(c, id, r);
      if (k) kids.push(k);
    }
    const props: Record<string, unknown> = {};
    const raw = tree[5];
    if (raw && typeof raw === "object") for (const [k, v] of Object.entries(raw as Record<string, unknown>)) {
      const p2 = cleanProp(drawn.tag, k, v);
      if (p2) props[p2[0]] = p2[1];
    }
    const node: unknown[] = [id, drawn.tag, drawn.ns, attrs, kids, props];
    if (drawn.tag === "style") node[4] = [[0, "#css", this.sheet(id)]];
    return node;
  }

  private adopt(id: number, r: Known, p: Known) {
    this.nodes.set(id, r);
    p.kids.add(id);
  }

  /** A style element's sheet from its text nodes, checked as a whole. */
  private sheet(id: number): string {
    const css = safeCss((this.sheets.get(id) ?? []).map((t) => this.nodes.get(t)?.text ?? "").join(""), false);
    this.countCss(id, css.length);
    return css;
  }

  private restyle(id: number, out: Clean[]) {
    if (this.nodes.has(id)) out.push(["s", id, this.sheet(id)]);
  }

  private forget(id: number) {
    const r = this.nodes.get(id);
    if (!r) return;
    for (const kid of [...r.kids]) this.forget(kid);
    if (r.tag === "#text") {
      this.textBytes -= (r.text ?? "").length;
      const sheet = this.sheets.get(r.parent);
      if (sheet) this.sheets.set(r.parent, sheet.filter((t) => t !== id));
    }
    this.nodes.get(r.parent)?.kids.delete(id);
    this.sheets.delete(id);
    this.cssBytes.delete(id);
    this.cssBytes.delete(-id);
    this.nodes.delete(id);
    this.nodeCount--;
  }

  private countNode() {
    if (++this.nodeCount > NODES_MOST) throw new LimitError("nodes");
  }

  private countText(before: string, after: string) {
    this.textBytes += after.length - before.length;
    if (this.textBytes > TEXT_BYTES_MOST) throw new LimitError("text");
  }

  private countCss(key: number, bytes: number) {
    this.cssBytes.set(key, bytes);
    if (this.counts.css > CSS_BYTES_MOST) throw new LimitError("css");
  }
}

/** The display frame's sandbox: scripts (its own, by nonce) and nothing else, an opaque origin. Never more (a test pins it). */
export const FRAME_SANDBOX = "allow-scripts";

/** The display frame's CSP: nothing loads or connects, no frame, no form, no base; only its own script (by nonce) runs;
 *  inline styles (the card's, checked) and inline images (data:) are what it draws with. */
export function frameCsp(nonce: string): string {
  return [
    "default-src 'none'",
    `script-src 'nonce-${nonce}'`,
    "style-src 'unsafe-inline'",
    "img-src data:",
    "font-src 'none'",
    "media-src 'none'",
    "connect-src 'none'",
    "frame-src 'none'",
    "child-src 'none'",
    "worker-src 'none'",
    "object-src 'none'",
    "manifest-src 'none'",
    "base-uri 'none'",
    "form-action 'none'",
  ].join("; ");
}

/** A nonce for the frame's one script (its CSP allows only that): 144 random bits, new for every frame. */
export function frameNonce(): string {
  const b = new Uint8Array(18);
  crypto.getRandomValues(b);
  return btoa(String.fromCharCode(...b)).replace(/[+/=]/g, (c) => (c === "+" ? "-" : c === "/" ? "_" : ""));
}

/**
 * The display frame's document. Its CSP comes before anything else: nothing loads (default-src 'none'), no script runs
 * but the frame's own (frame.js, by its nonce), no form goes anywhere. The frame is sandboxed with allow-scripts only:
 * an opaque origin, without the app's storage, cookies or DOM. The card's code never runs here (it runs in QuickJS in a
 * worker); this frame's script only draws what the page checked and tells the page what the user did.
 */
export function frameShell(dark: boolean, nonce: string, script: string): string {
  // the frame's own script (frame.js), never anything else: nothing in it may end or open a script element
  if (/<\/script|<script|<!--/i.test(script)) throw new Error("frame script: must not contain <script, </script or <!--");
  if (!/^[A-Za-z0-9_-]{16,}$/.test(nonce)) throw new Error("frame nonce: at least 16 URL-safe characters");
  const csp = frameCsp(nonce);
  const ink = dark ? "#f2f2f4" : "#1c1c1e";
  const link = dark ? "#6cb4ff" : "#0a66c2";
  return (
    `<!doctype html><html><head><meta http-equiv="Content-Security-Policy" content="${csp}">` +
    `<meta name="color-scheme" content="${dark ? "dark" : "light"}">` +
    `<style>:root{color-scheme:${dark ? "dark" : "light"}}html,body{background:transparent}` +
    `body{display:flow-root;margin:0;padding:12px;font:13px/1.5 system-ui,-apple-system,"Segoe UI",sans-serif;color:${ink};overflow:auto;-webkit-text-size-adjust:100%}` +
    `a{color:${link};text-decoration:underline;cursor:pointer}</style></head>` +
    `<body><script nonce="${nonce}">${script}</script></body></html>`
  );
}
