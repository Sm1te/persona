// The card's world inside QuickJS (#4215; loaded by html-runtime.worker.ts, never run on the page). This file is the
// source of one function expression: the worker evaluates it in a fresh QuickJS context and calls it with the host
// object. Nothing here can reach the network, the page or storage: QuickJS has only the ECMAScript built-ins, and the
// host gives just what is below asks for (operations out, timers, the bridge, canvas drawing, Intl formatting).
//
// The DOM is a small one: enough for cards written with elements, classes, styles, events, SVG and a 2D canvas.
// Every change to the document is recorded as an operation (protocol.ts) and sent to the page at the end of the turn;
// the page checks each one against its allowlist before drawing it. Layout can't be read synchronously here: sizes
// are the frame's.
(function (H) {
  "use strict";
  const G = globalThis;
  const info = JSON.parse(H.info());
  let W = info.width;
  let Hh = info.height;

  // ── nodes ──
  const HTML_ID = 1, HEAD_ID = 2, BODY_ID = 3;
  let nextId = 4;
  let ops = [];
  const byId = new Map();
  const VOID = new Set(["area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "param", "source", "track", "wbr"]);
  const RAW = new Set(["script", "style", "xmp", "noscript", "iframe", "noembed", "noframes"]);
  const RCDATA = new Set(["textarea", "title"]);
  const SVG_CASE = {};
  for (const n of ["linearGradient", "radialGradient", "clipPath", "textPath", "foreignObject", "animateTransform", "animateMotion", "feGaussianBlur", "feOffset", "feBlend", "feColorMatrix", "feComposite", "feFlood", "feMerge", "feMergeNode", "feDropShadow"]) SVG_CASE[n.toLowerCase()] = n;
  const SVG_ATTR_CASE = {};
  for (const n of ["viewBox", "preserveAspectRatio", "gradientUnits", "gradientTransform", "patternUnits", "patternTransform", "patternContentUnits", "clipPathUnits", "maskUnits", "maskContentUnits", "markerWidth", "markerHeight", "markerUnits", "refX", "refY", "textLength", "lengthAdjust", "pathLength", "stdDeviation", "spreadMethod", "attributeName", "attributeType", "repeatCount", "repeatDur", "keyTimes", "keySplines", "calcMode", "startOffset", "filterUnits", "primitiveUnits"]) SVG_ATTR_CASE[n.toLowerCase()] = n;

  const emit = (op) => ops.push(op);
  const connected = (n) => {
    for (let x = n; x; x = x.parentNode) if (x === document) return true;
    return false;
  };

  class EventTarget {
    constructor() { this._listeners = null; }
    addEventListener(type, fn, opts) {
      if (typeof fn !== "function" && !(fn && typeof fn.handleEvent === "function")) return;
      const capture = typeof opts === "boolean" ? opts : !!(opts && opts.capture);
      const once = !!(opts && typeof opts === "object" && opts.once);
      const l = this._listeners || (this._listeners = {});
      const list = l[type] || (l[type] = []);
      if (list.some((x) => x.fn === fn && x.capture === capture)) return;
      list.push({ fn, capture, once });
    }
    removeEventListener(type, fn, opts) {
      const capture = typeof opts === "boolean" ? opts : !!(opts && opts.capture);
      const list = this._listeners && this._listeners[type];
      if (!list) return;
      const i = list.findIndex((x) => x.fn === fn && x.capture === capture);
      if (i >= 0) list.splice(i, 1);
    }
    dispatchEvent(ev) { return dispatch(this, ev); }
  }

  class Node extends EventTarget {
    constructor(type, name) {
      super();
      this.nodeType = type;
      this.nodeName = name;
      this.parentNode = null;
      this.childNodes = [];
      this._id = 0;
    }
    get ownerDocument() { return this === document ? null : document; }
    get parentElement() { return this.parentNode && this.parentNode.nodeType === 1 ? this.parentNode : null; }
    get firstChild() { return this.childNodes[0] || null; }
    get lastChild() { return this.childNodes[this.childNodes.length - 1] || null; }
    get nextSibling() { const p = this.parentNode; if (!p) return null; return p.childNodes[p.childNodes.indexOf(this) + 1] || null; }
    get previousSibling() { const p = this.parentNode; if (!p) return null; return p.childNodes[p.childNodes.indexOf(this) - 1] || null; }
    get isConnected() { return connected(this); }
    hasChildNodes() { return this.childNodes.length > 0; }
    contains(n) { for (let x = n; x; x = x.parentNode) if (x === this) return true; return false; }
    getRootNode() { let x = this; while (x.parentNode) x = x.parentNode; return x; }
    appendChild(n) { return this.insertBefore(n, null); }
    insertBefore(n, ref) {
      if (!(n instanceof Node)) throw new TypeError("insertBefore: not a node");
      if (n.contains(this)) throw new Error("HierarchyRequestError");
      if (n.nodeType === 11) {
        for (const c of [...n.childNodes]) this.insertBefore(c, ref);
        return n;
      }
      if (ref && ref.parentNode !== this) throw new Error("NotFoundError");
      if (n === ref) return n;
      if (n.parentNode) n.parentNode.removeChild(n);
      const i = ref ? this.childNodes.indexOf(ref) : this.childNodes.length;
      this.childNodes.splice(i, 0, n);
      n.parentNode = this;
      if (connected(this) && this.nodeType !== 9) emit(["t", this._id, ref ? ref._id : 0, tree(n)]);
      return n;
    }
    removeChild(n) {
      const i = this.childNodes.indexOf(n);
      if (i < 0) throw new Error("NotFoundError");
      if (connected(this)) emit(["r", n._id]);
      this.childNodes.splice(i, 1);
      n.parentNode = null;
      return n;
    }
    replaceChild(n, old) { this.insertBefore(n, old); return this.removeChild(old); }
    remove() { if (this.parentNode) this.parentNode.removeChild(this); }
    get textContent() {
      if (this.nodeType === 3 || this.nodeType === 8) return this.data;
      let s = "";
      for (const c of this.childNodes) if (c.nodeType !== 8) s += c.textContent;
      return s;
    }
    set textContent(v) {
      if (this.nodeType === 3 || this.nodeType === 8) { this.data = v; return; }
      for (const c of [...this.childNodes]) this.removeChild(c);
      const s = v === null || v === undefined ? "" : String(v);
      if (s) this.appendChild(new Text(s));
    }
    get nodeValue() { return this.nodeType === 3 || this.nodeType === 8 ? this.data : null; }
    set nodeValue(v) { if (this.nodeType === 3 || this.nodeType === 8) this.data = v; }
    cloneNode(deep) {
      let c;
      if (this.nodeType === 3) c = new Text(this.data);
      else if (this.nodeType === 8) c = new Comment(this.data);
      else if (this.nodeType === 11) c = new DocumentFragment();
      else {
        c = makeElement(this.localName, this._ns);
        for (const [k, v] of this._attrs) c._attrs.set(k, v);
        if (this._value !== undefined) c._value = this._value;
        if (this._checked !== undefined) c._checked = this._checked;
      }
      if (deep) for (const k of this.childNodes) c.appendChild(k.cloneNode(true));
      return c;
    }
    // childNode / parentNode mixins
    append(...ns) { for (const n of ns) this.appendChild(typeof n === "string" ? new Text(n) : n); }
    prepend(...ns) { const ref = this.firstChild; for (const n of ns) this.insertBefore(typeof n === "string" ? new Text(n) : n, ref); }
    before(...ns) { const p = this.parentNode; if (p) for (const n of ns) p.insertBefore(typeof n === "string" ? new Text(n) : n, this); }
    after(...ns) { const p = this.parentNode; if (!p) return; const ref = this.nextSibling; for (const n of ns) p.insertBefore(typeof n === "string" ? new Text(n) : n, ref); }
    replaceWith(...ns) { const p = this.parentNode; if (!p) return; this.before(...ns); this.remove(); }
    replaceChildren(...ns) { for (const c of [...this.childNodes]) this.removeChild(c); this.append(...ns); }
    get children() { return this.childNodes.filter((c) => c.nodeType === 1); }
    get childElementCount() { return this.children.length; }
    get firstElementChild() { return this.children[0] || null; }
    get lastElementChild() { const c = this.children; return c[c.length - 1] || null; }
    get nextElementSibling() { let x = this.nextSibling; while (x && x.nodeType !== 1) x = x.nextSibling; return x; }
    get previousElementSibling() { let x = this.previousSibling; while (x && x.nodeType !== 1) x = x.previousSibling; return x; }
    querySelector(sel) { return query(this, sel, true)[0] || null; }
    querySelectorAll(sel) { return query(this, sel, false); }
    getElementsByTagName(t) { return this.querySelectorAll(t === "*" ? "*" : t); }
    getElementsByClassName(c) { return this.querySelectorAll(String(c).trim().split(/\s+/).map((x) => "." + cssEscape(x)).join("")); }
  }

  const register = (n) => { n._id = nextId++; byId.set(n._id, n); return n; };

  class CharacterData extends Node {
    constructor(type, name, data) { super(type, name); this._data = String(data); }
    get data() { return this._data; }
    set data(v) {
      const s = v === null || v === undefined ? "" : String(v);
      if (s === this._data) return;
      this._data = s;
      if (this.nodeType === 3 && connected(this)) emit(["d", this._id, s]);
    }
    get length() { return this._data.length; }
  }
  class Text extends CharacterData {
    constructor(data) { super(3, "#text", data === undefined ? "" : data); register(this); }
    get wholeText() { return this._data; }
  }
  class Comment extends CharacterData {
    constructor(data) { super(8, "#comment", data === undefined ? "" : data); register(this); }
  }
  class DocumentFragment extends Node {
    constructor() { super(11, "#document-fragment"); register(this); }
    getElementById(id) { return this.querySelector("#" + cssEscape(id)); }
  }

  // ── elements ──
  const BOOL_ATTRS = ["disabled", "readonly", "required", "multiple", "hidden", "open", "reversed", "autofocus"];
  const STR_ATTRS = ["id", "title", "lang", "dir", "name", "placeholder", "alt", "role", "min", "max", "step", "for", "label", "src", "href", "rel", "target"];

  class Element extends Node {
    constructor(tag, ns) {
      super(1, ns === "svg" ? tag : tag.toUpperCase());
      this.localName = tag;
      this._ns = ns || "";
      this._attrs = new Map();
      this._style = null;
      this._classList = null;
      this._dataset = null;
      register(this);
    }
    get tagName() { return this.nodeName; }
    get namespaceURI() { return this._ns === "svg" ? "http://www.w3.org/2000/svg" : "http://www.w3.org/1999/xhtml"; }
    getAttribute(n) { const v = this._attrs.get(this._an(n)); return v === undefined ? null : v; }
    getAttributeNS(_ns, n) { return this.getAttribute(n); }
    hasAttribute(n) { return this._attrs.has(this._an(n)); }
    hasAttributes() { return this._attrs.size > 0; }
    getAttributeNames() { return [...this._attrs.keys()]; }
    get attributes() {
      const list = [...this._attrs].map(([name, value]) => ({ name, value, localName: name, nodeName: name }));
      list.getNamedItem = (n) => list.find((a) => a.name === n) || null;
      return list;
    }
    setAttribute(n, v) {
      const name = this._an(n);
      if (!/^[^\s"'>/=\u0000-\u001f]+$/.test(name)) throw new Error("InvalidCharacterError");
      const s = String(v);
      if (this._attrs.get(name) === s) return;
      this._attrs.set(name, s);
      if (name === "style" && this._style) this._style._read(s);
      if (name === "class" && this._classList) this._classList._sync();
      if (connected(this)) emit(["a", this._id, name, s]);
    }
    setAttributeNS(_ns, n, v) { this.setAttribute(n.replace(/^xlink:/, ""), v); }
    removeAttribute(n) {
      const name = this._an(n);
      if (!this._attrs.has(name)) return;
      this._attrs.delete(name);
      if (name === "style" && this._style) this._style._read("");
      if (connected(this)) emit(["a", this._id, name, null]);
    }
    toggleAttribute(n, force) {
      const has = this.hasAttribute(n);
      const want = force === undefined ? !has : !!force;
      if (want && !has) this.setAttribute(n, "");
      if (!want && has) this.removeAttribute(n);
      return want;
    }
    _an(n) {
      const s = String(n);
      if (this._ns === "svg") return SVG_ATTR_CASE[s.toLowerCase()] || s;
      return s.toLowerCase();
    }
    get className() { return this.getAttribute("class") || ""; }
    set className(v) { this.setAttribute("class", v); }
    get classList() { return this._classList || (this._classList = new TokenList(this)); }
    get dataset() {
      if (this._dataset) return this._dataset;
      const el = this;
      const key = (p) => "data-" + String(p).replace(/[A-Z]/g, (c) => "-" + c.toLowerCase());
      this._dataset = new Proxy({}, {
        get: (_, p) => (typeof p === "string" ? (el.getAttribute(key(p)) === null ? undefined : el.getAttribute(key(p))) : undefined),
        set: (_, p, v) => { el.setAttribute(key(p), v); return true; },
        deleteProperty: (_, p) => { el.removeAttribute(key(p)); return true; },
        has: (_, p) => el.hasAttribute(key(p)),
        ownKeys: () => el.getAttributeNames().filter((n) => n.startsWith("data-")).map((n) => n.slice(5).replace(/-([a-z])/g, (_, c) => c.toUpperCase())),
        getOwnPropertyDescriptor: (_, p) => (el.hasAttribute(key(p)) ? { value: el.getAttribute(key(p)), enumerable: true, configurable: true, writable: true } : undefined),
      });
      return this._dataset;
    }
    get style() { return this._style || (this._style = styleOf(this)); }
    set style(v) { this.setAttribute("style", v); }
    get innerHTML() { return this.childNodes.map(serialize).join(""); }
    set innerHTML(v) {
      for (const c of [...this.childNodes]) this.removeChild(c);
      const frag = parseFragment(String(v === null || v === undefined ? "" : v), this._ns === "svg" || this.localName === "svg" ? "svg" : "");
      this.appendChild(frag);
    }
    get outerHTML() { return serialize(this); }
    set outerHTML(v) {
      const frag = parseFragment(String(v), this._ns);
      this.replaceWith(frag);
    }
    get innerText() { return this.textContent; }
    set innerText(v) { this.textContent = v; }
    insertAdjacentHTML(where, html) { this._adjacent(where, parseFragment(String(html), this._ns)); }
    insertAdjacentElement(where, el) { this._adjacent(where, el); return el; }
    insertAdjacentText(where, t) { this._adjacent(where, new Text(t)); }
    _adjacent(where, n) {
      switch (String(where).toLowerCase()) {
        case "beforebegin": if (this.parentNode) this.parentNode.insertBefore(n, this); break;
        case "afterbegin": this.insertBefore(n, this.firstChild); break;
        case "beforeend": this.appendChild(n); break;
        case "afterend": if (this.parentNode) this.parentNode.insertBefore(n, this.nextSibling); break;
        default: throw new SyntaxError("insertAdjacent: " + where);
      }
    }
    matches(sel) { return compile(sel)(this, null); }
    closest(sel) { const m = compile(sel); for (let x = this; x && x.nodeType === 1; x = x.parentNode) if (m(x, null)) return x; return null; }
    getElementById(id) { return this.querySelector("#" + cssEscape(id)); }
    click() { dispatch(this, makeEvent("click", { bubbles: true, cancelable: true }), true); }
    focus() { document.activeElement = this; if (connected(this)) emit(["f", this._id]); }
    blur() { if (document.activeElement === this) document.activeElement = document.body; }
    scrollIntoView() {}
    scrollTo() {}
    scrollBy() {}
    getBoundingClientRect() { return rect(this); }
    getClientRects() { return [rect(this)]; }
    get clientWidth() { return box(this).width; }
    get clientHeight() { return box(this).height; }
    get offsetWidth() { return box(this).width; }
    get offsetHeight() { return box(this).height; }
    get scrollWidth() { return box(this).width; }
    get scrollHeight() { return box(this).height; }
    get offsetTop() { return 0; }
    get offsetLeft() { return 0; }
    get offsetParent() { return this.parentElement; }
    get scrollTop() { return 0; }
    set scrollTop(_) {}
    get scrollLeft() { return 0; }
    set scrollLeft(_) {}
    animate() { return { finished: Promise.resolve(), cancel() {}, play() {}, pause() {}, onfinish: null }; }
    getAnimations() { return []; }
    attachShadow() { throw new Error("NotSupportedError: shadow roots are not available in cards"); }
  }
  for (const a of BOOL_ATTRS) Object.defineProperty(Element.prototype, a === "readonly" ? "readOnly" : a, {
    get() { return this.hasAttribute(a); },
    set(v) { this.toggleAttribute(a, !!v); },
    configurable: true,
  });
  for (const a of STR_ATTRS) Object.defineProperty(Element.prototype, a === "for" ? "htmlFor" : a, {
    get() { return this.getAttribute(a) || ""; },
    set(v) { this.setAttribute(a, v); },
    configurable: true,
  });
  Object.defineProperty(Element.prototype, "tabIndex", { get() { const v = parseInt(this.getAttribute("tabindex"), 10); return Number.isNaN(v) ? -1 : v; }, set(v) { this.setAttribute("tabindex", String(v | 0)); }, configurable: true });

  // Form controls: value and checked are the control's own (the attribute is only where they start), kept in step
  // with the page by the events it sends.
  const VALUED = new Set(["input", "textarea", "select", "option", "button", "progress", "meter", "output", "li", "data"]);
  Object.defineProperty(Element.prototype, "value", {
    get() {
      const t = this.localName;
      if (t === "select") { const o = this.options.find((x) => x.selected) || this.options[0]; return o ? o.value : ""; }
      if (t === "option") return this.hasAttribute("value") ? this.getAttribute("value") : this.textContent.trim();
      if (t === "textarea") return this._value !== undefined ? this._value : this.textContent;
      if (t === "progress" || t === "meter") return Number(this.getAttribute("value") || 0);
      if (this._value !== undefined) return this._value;
      if (t === "input" && (this.type === "checkbox" || this.type === "radio")) return this.getAttribute("value") || "on";
      return this.getAttribute("value") || "";
    },
    set(v) {
      const t = this.localName;
      const s = v === null || v === undefined ? "" : String(v);
      if (t === "select") { for (const o of this.options) o._selectedSilently(o.value === s); if (connected(this)) emit(["p", this._id, "value", s]); return; }
      if (t === "progress" || t === "meter" || t === "option" || t === "button" || t === "li" || t === "data") { this.setAttribute("value", s); return; }
      if (!VALUED.has(t)) { this._value = s; return; }
      this._value = s;
      if (connected(this)) emit(["p", this._id, "value", s]);
    },
    configurable: true,
  });
  Object.defineProperty(Element.prototype, "checked", {
    get() { return this._checked !== undefined ? this._checked : this.hasAttribute("checked"); },
    set(v) {
      const b = !!v;
      if (this._checked === b) return;
      this._checked = b;
      if (b && this.type === "radio" && this.name) for (const r of radios(this)) if (r !== this) r._checked = false;
      if (connected(this)) emit(["p", this._id, "checked", b]);
    },
    configurable: true,
  });
  Object.defineProperty(Element.prototype, "indeterminate", { get() { return !!this._indeterminate; }, set(v) { this._indeterminate = !!v; if (connected(this)) emit(["p", this._id, "indeterminate", !!v]); }, configurable: true });
  Object.defineProperty(Element.prototype, "type", {
    get() { const t = this.localName; const v = (this.getAttribute("type") || "").toLowerCase(); if (t === "input") return v || "text"; if (t === "button") return v || "submit"; if (t === "select") return this.multiple ? "select-multiple" : "select-one"; return v; },
    set(v) { this.setAttribute("type", v); },
    configurable: true,
  });
  Object.defineProperty(Element.prototype, "selected", {
    get() { return this._selected !== undefined ? this._selected : this.hasAttribute("selected"); },
    set(v) { const sel = this.closest("select"); if (sel && !sel.multiple && v) for (const o of sel.options) o._selectedSilently(o === this); else this._selectedSilently(!!v); if (connected(this)) emit(["p", this._id, "selected", !!v]); },
    configurable: true,
  });
  Element.prototype._selectedSilently = function (v) { this._selected = !!v; };
  Object.defineProperty(Element.prototype, "options", { get() { return this.localName === "select" ? query(this, "option", false) : undefined; }, configurable: true });
  Object.defineProperty(Element.prototype, "selectedIndex", {
    get() { const o = this.options || []; const i = o.findIndex((x) => x.selected); return i < 0 && o.length && !this.multiple ? 0 : i; },
    set(i) { const o = this.options || []; o.forEach((x, j) => x._selectedSilently(j === i)); if (connected(this)) emit(["p", this._id, "selectedIndex", i]); },
    configurable: true,
  });
  Object.defineProperty(Element.prototype, "form", { get() { return this.closest("form"); }, configurable: true });
  Object.defineProperty(Element.prototype, "valueAsNumber", { get() { const n = parseFloat(this.value); return Number.isNaN(n) ? NaN : n; }, set(v) { this.value = String(v); }, configurable: true });
  Object.defineProperty(Element.prototype, "elements", { get() { return this.localName === "form" ? query(this, "input,select,textarea,button,output,fieldset", false) : undefined; }, configurable: true });
  Element.prototype.submit = function () { /* nothing to submit to */ };
  Element.prototype.requestSubmit = function () { if (this.localName === "form") dispatch(this, makeEvent("submit", { bubbles: true, cancelable: true })); };
  Element.prototype.reset = function () { if (this.localName === "form") for (const c of this.elements) { c._value = undefined; c._checked = undefined; if (connected(c)) emit(["p", c._id, "value", c.value]); } };
  Element.prototype.select = function () {};
  Element.prototype.setSelectionRange = function () {};
  Element.prototype.checkValidity = function () { return true; };
  Element.prototype.reportValidity = function () { return true; };
  Element.prototype.setCustomValidity = function () {};
  Object.defineProperty(Element.prototype, "labels", { get() { return this.id ? query(document, `label[for="${this.id}"]`, false) : []; }, configurable: true });
  function radios(el) { const root = el.closest("form") || document; return query(root, "input", false).filter((r) => r.type === "radio" && r.name === el.name); }

  // details: open is the attribute
  // canvas: width / height, and a 2D context drawn by the worker
  Object.defineProperty(Element.prototype, "width", {
    get() { if (this.localName === "canvas") { const v = parseInt(this.getAttribute("width"), 10); return Number.isNaN(v) ? 300 : v; } return parseFloat(this.getAttribute("width")) || 0; },
    set(v) { this.setAttribute("width", String(Math.max(0, Math.floor(Number(v) || 0)))); if (this._ctx) this._ctx._reset(); },
    configurable: true,
  });
  Object.defineProperty(Element.prototype, "height", {
    get() { if (this.localName === "canvas") { const v = parseInt(this.getAttribute("height"), 10); return Number.isNaN(v) ? 150 : v; } return parseFloat(this.getAttribute("height")) || 0; },
    set(v) { this.setAttribute("height", String(Math.max(0, Math.floor(Number(v) || 0)))); if (this._ctx) this._ctx._reset(); },
    configurable: true,
  });
  Element.prototype.getContext = function (kind) {
    if (this.localName !== "canvas" || kind !== "2d") return null;
    return this._ctx || (this._ctx = new Context2D(this));
  };
  Element.prototype.toDataURL = function () { return "data:,"; };
  Element.prototype.toBlob = function (cb) { if (typeof cb === "function") setTimeout(() => cb(null), 0); };
  Element.prototype.transferControlToOffscreen = function () { throw new Error("NotSupportedError"); };
  // svg: what charts ask for
  Element.prototype.getBBox = function () { return { x: 0, y: 0, width: 0, height: 0 }; };
  Element.prototype.getTotalLength = function () { return 0; };
  Element.prototype.getComputedTextLength = function () { return 0; };
  Element.prototype.getScreenCTM = function () { return { a: 1, b: 0, c: 0, d: 1, e: 0, f: 0, inverse() { return this; } }; };

  function makeElement(tag, ns) {
    const t = ns === "svg" ? (SVG_CASE[tag.toLowerCase()] || tag) : tag.toLowerCase();
    return new Element(t, ns);
  }

  const box = (el) => (el.localName === "html" || el.localName === "body" ? { width: W, height: Hh } : el.localName === "canvas" ? { width: el.width, height: el.height } : { width: W, height: 0 });
  const rect = (el) => { const b = box(el); return { x: 0, y: 0, left: 0, top: 0, width: b.width, height: b.height, right: b.width, bottom: b.height, toJSON() { return this; } }; };

  class TokenList {
    constructor(el) { this._el = el; }
    _list() { return (this._el.getAttribute("class") || "").split(/\s+/).filter(Boolean); }
    _sync() {}
    _set(list) { this._el.setAttribute("class", list.join(" ")); }
    get length() { return this._list().length; }
    get value() { return this._el.getAttribute("class") || ""; }
    item(i) { return this._list()[i] || null; }
    contains(c) { return this._list().includes(String(c)); }
    add(...cs) { const l = this._list(); for (const c of cs) if (!l.includes(String(c))) l.push(String(c)); this._set(l); }
    remove(...cs) { this._set(this._list().filter((x) => !cs.map(String).includes(x))); }
    toggle(c, force) { const has = this.contains(c); const want = force === undefined ? !has : !!force; if (want && !has) this.add(c); if (!want && has) this.remove(c); return want; }
    replace(a, b) { const l = this._list(); const i = l.indexOf(String(a)); if (i < 0) return false; l[i] = String(b); this._set(l); return true; }
    forEach(f) { this._list().forEach(f); }
    [Symbol.iterator]() { return this._list()[Symbol.iterator](); }
    toString() { return this.value; }
  }

  // style: a declaration list kept as text in the style attribute
  const camel = (p) => p.replace(/-([a-z])/g, (_, c) => c.toUpperCase());
  const kebab = (p) => (p.startsWith("--") ? p : p.replace(/^(webkit|moz|ms)([A-Z])/, "-$1$2").replace(/[A-Z]/g, (c) => "-" + c.toLowerCase()));
  function styleOf(el) {
    const decls = new Map();
    const api = {
      _read(text) {
        decls.clear();
        for (const part of String(text).split(";")) {
          const i = part.indexOf(":");
          if (i > 0) decls.set(part.slice(0, i).trim().toLowerCase(), part.slice(i + 1).trim());
        }
      },
      _write() { el.setAttribute("style", [...decls].map(([k, v]) => k + ": " + v).join("; ")); },
      setProperty(p, v, prio) { const k = kebab(String(p)); if (v === null || v === undefined || v === "") decls.delete(k); else decls.set(k, String(v) + (prio ? " !important" : "")); api._write(); },
      getPropertyValue(p) { return decls.get(kebab(String(p))) || ""; },
      removeProperty(p) { const k = kebab(String(p)); const old = decls.get(k) || ""; decls.delete(k); api._write(); return old; },
      getPropertyPriority(p) { return /!important$/.test(decls.get(kebab(String(p))) || "") ? "important" : ""; },
      get cssText() { return [...decls].map(([k, v]) => k + ": " + v).join("; "); },
      set cssText(v) { api._read(v); api._write(); },
      get length() { return decls.size; },
      item(i) { return [...decls.keys()][i] || ""; },
    };
    api._read(el.getAttribute("style") || "");
    return new Proxy(api, {
      get(t, p) {
        if (typeof p !== "string") return undefined;
        if (p in t) return t[p];
        return decls.get(kebab(p)) || "";
      },
      set(t, p, v) {
        if (typeof p !== "string") return true;
        if (p === "cssText") { t.cssText = v; return true; }
        t.setProperty(kebab(p), v === null || v === undefined ? "" : String(v));
        return true;
      },
    });
  }

  // ── the document ──
  class Document extends Node {
    constructor() {
      super(9, "#document");
      this._id = 0;
      this.readyState = "loading";
      this.activeElement = null;
      this.currentScript = null;
      this.title = "";
      this.visibilityState = "visible";
      this.hidden = false;
      this.characterSet = "UTF-8";
      this.compatMode = "CSS1Compat";
      this.contentType = "text/html";
      this.URL = "about:srcdoc";
      this.documentURI = "about:srcdoc";
      this.referrer = "";
    }
    get documentElement() { return htmlEl; }
    get head() { return headEl; }
    get body() { return bodyEl; }
    get defaultView() { return G; }
    get location() { return LOCATION; }
    get cookie() { return ""; }
    set cookie(_) {}
    get forms() { return query(this, "form", false); }
    get scripts() { return query(this, "script", false); }
    get images() { return query(this, "img", false); }
    get links() { return []; }
    createElement(t) { return makeElement(String(t), ""); }
    createElementNS(ns, t) { return makeElement(String(t), ns === "http://www.w3.org/2000/svg" ? "svg" : ""); }
    createTextNode(s) { return new Text(String(s)); }
    createComment(s) { return new Comment(String(s)); }
    createDocumentFragment() { return new DocumentFragment(); }
    createEvent() { return makeEvent("", {}); }
    getElementById(id) { return this.querySelector("#" + cssEscape(id)); }
    getElementsByName(n) { return query(this, `[name="${String(n).replace(/"/g, '\\"')}"]`, false); }
    hasFocus() { return true; }
    open() { return this; }
    close() {}
    write(...parts) { if (this.readyState === "loading") bodyEl.insertAdjacentHTML("beforeend", parts.join("")); }
    writeln(...parts) { this.write(...parts, "\n"); }
    execCommand() { return false; }
    elementFromPoint() { return null; }
    get fonts() { return { ready: Promise.resolve(), load: () => Promise.resolve([]), check: () => true, add() {}, addEventListener() {} }; }
  }

  const document = new Document();
  const htmlEl = new Element("html", "");
  const headEl = new Element("head", "");
  const bodyEl = new Element("body", "");
  // the fixed ids the page knows (protocol.ts)
  byId.delete(htmlEl._id); byId.delete(headEl._id); byId.delete(bodyEl._id);
  htmlEl._id = HTML_ID; headEl._id = HEAD_ID; bodyEl._id = BODY_ID;
  byId.set(HTML_ID, htmlEl); byId.set(HEAD_ID, headEl); byId.set(BODY_ID, bodyEl);
  document.childNodes.push(htmlEl); htmlEl.parentNode = document;
  htmlEl.childNodes.push(headEl, bodyEl); headEl.parentNode = htmlEl; bodyEl.parentNode = htmlEl;
  document.activeElement = bodyEl;

  // ── serialize, as innerHTML reads it ──
  const escText = (s) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/ /g, "&nbsp;");
  const escAttr = (s) => s.replace(/&/g, "&amp;").replace(/"/g, "&quot;").replace(/ /g, "&nbsp;");
  function serialize(n) {
    if (n.nodeType === 3) return n.parentNode && RAW.has(n.parentNode.localName) ? n.data : escText(n.data);
    if (n.nodeType === 8) return "<!--" + n.data + "-->";
    if (n.nodeType === 11) return n.childNodes.map(serialize).join("");
    let s = "<" + n.localName;
    for (const [k, v] of n._attrs) s += " " + k + '="' + escAttr(v) + '"';
    if (VOID.has(n.localName) && n._ns !== "svg") return s + ">";
    return s + ">" + n.childNodes.map(serialize).join("") + "</" + n.localName + ">";
  }

  // What the page is sent when a subtree joins the document: the whole subtree, with what its controls hold.
  function tree(n) {
    if (n.nodeType === 3) return [n._id, "#text", n.data];
    if (n.nodeType === 8) return [n._id, "#comment"];
    const attrs = [...n._attrs];
    const kids = n.childNodes.map(tree);
    const props = {};
    if (n._value !== undefined) props.value = n._value;
    if (n._checked !== undefined) props.checked = n._checked;
    if (n._selected !== undefined) props.selected = n._selected;
    return [n._id, n.localName, attrs, kids, n._ns, props];
  }

  // ── the parser ──
  const ENTITIES = { amp: "&", lt: "<", gt: ">", quot: '"', apos: "'", nbsp: " ", copy: "©", reg: "®", trade: "™", times: "×", divide: "÷", middot: "·", hellip: "…", mdash: "—", ndash: "–", lsquo: "‘", rsquo: "’", ldquo: "“", rdquo: "”", laquo: "«", raquo: "»", bull: "•", deg: "°", plusmn: "±", larr: "←", rarr: "→", uarr: "↑", darr: "↓", harr: "↔", yen: "¥", euro: "€", pound: "£", cent: "¢", sect: "§", para: "¶", frac12: "½", frac14: "¼", frac34: "¾", minus: "−", le: "≤", ge: "≥", ne: "≠", infin: "∞", check: "✓", hearts: "♥", star: "☆", starf: "★", ensp: " ", emsp: " ", thinsp: " ", zwj: "‍", zwnj: "‌", shy: "­", iexcl: "¡", iquest: "¿", micro: "µ", sup2: "²", sup3: "³", alpha: "α", beta: "β", gamma: "γ", delta: "δ", pi: "π", sigma: "σ", mu: "μ", lambda: "λ", theta: "θ", omega: "ω", Delta: "Δ", Sigma: "Σ", Omega: "Ω" };
  function decode(s) {
    if (s.indexOf("&") < 0) return s;
    return s.replace(/&(#x[0-9a-f]+|#\d+|[a-z][a-z0-9]*);?/gi, (m, e) => {
      if (e[0] === "#") {
        const cp = e[1] === "x" || e[1] === "X" ? parseInt(e.slice(2), 16) : parseInt(e.slice(1), 10);
        return cp > 0 && cp <= 0x10ffff && !(cp >= 0xd800 && cp <= 0xdfff) ? String.fromCodePoint(cp) : "�";
      }
      return Object.prototype.hasOwnProperty.call(ENTITIES, e) ? ENTITIES[e] : m;
    });
  }
  const CLOSES_P = new Set(["address", "article", "aside", "blockquote", "details", "div", "dl", "fieldset", "figcaption", "figure", "footer", "form", "h1", "h2", "h3", "h4", "h5", "h6", "header", "hr", "main", "nav", "ol", "p", "pre", "section", "table", "ul", "menu"]);

  // Parses into `root` (a fragment or the body). Head-only tags at the top of a document go to the head.
  function parseInto(root, html, ns, whole) {
    const stack = [root];
    const top = () => stack[stack.length - 1];
    const nsOf = () => { for (let i = stack.length - 1; i > 0; i--) { const e = stack[i]; if (e.localName === "foreignObject") return ""; if (e._ns === "svg") return "svg"; } return ns; };
    const closeTo = (name, stopAt) => {
      for (let i = stack.length - 1; i > 0; i--) {
        const e = stack[i];
        if (e.localName === name || (e._ns !== "svg" && e.localName === name.toLowerCase())) { stack.length = i; return true; }
        if (stopAt && stopAt.includes(e.localName)) return false;
      }
      return false;
    };
    const isOpen = (name, stopAt) => { for (let i = stack.length - 1; i > 0; i--) { if (stack[i].localName === name) return true; if (stopAt && stopAt.includes(stack[i].localName)) return false; } return false; };
    const add = (n) => { top().appendChild(n); };
    let i = 0;
    const len = html.length;
    while (i < len) {
      const lt = html.indexOf("<", i);
      if (lt < 0) { addText(html.slice(i)); break; }
      if (lt > i) addText(html.slice(i, lt));
      i = lt;
      if (html.startsWith("<!--", i)) { const e = html.indexOf("-->", i + 4); add(new Comment(html.slice(i + 4, e < 0 ? len : e))); i = e < 0 ? len : e + 3; continue; }
      if (html.startsWith("<![CDATA[", i)) { const e = html.indexOf("]]>", i); addText(html.slice(i + 9, e < 0 ? len : e), true); i = e < 0 ? len : e + 3; continue; }
      if (html[i + 1] === "!" || html[i + 1] === "?") { const e = html.indexOf(">", i); i = e < 0 ? len : e + 1; continue; }
      const m = /^<(\/?)([a-zA-Z][^\s/>]*)/.exec(html.slice(i, i + 64));
      if (!m) { addText("<"); i++; continue; }
      const closing = m[1] === "/";
      const rawName = m[2];
      let j = i + m[0].length;
      // attributes
      const attrs = [];
      let selfClose = false;
      while (j < len) {
        while (j < len && /\s/.test(html[j])) j++;
        if (html[j] === ">") { j++; break; }
        if (html[j] === "/" && html[j + 1] === ">") { selfClose = true; j += 2; break; }
        if (html[j] === "/") { j++; continue; }
        const an = /^[^\s/>=]+/.exec(html.slice(j, j + 256));
        if (!an) { j++; continue; }
        j += an[0].length;
        while (j < len && /\s/.test(html[j])) j++;
        let val = "";
        if (html[j] === "=") {
          j++;
          while (j < len && /\s/.test(html[j])) j++;
          const q = html[j];
          if (q === '"' || q === "'") { const e = html.indexOf(q, j + 1); val = html.slice(j + 1, e < 0 ? len : e); j = e < 0 ? len : e + 1; }
          else { const v = /^[^\s>]*/.exec(html.slice(j)); val = v[0]; j += val.length; }
        }
        attrs.push([an[0], decode(val)]);
      }
      i = j;
      const inSvg = nsOf() === "svg" || rawName.toLowerCase() === "svg";
      const name = inSvg ? (SVG_CASE[rawName.toLowerCase()] || rawName) : rawName.toLowerCase();
      if (closing) {
        if (name === "body" || name === "html" || name === "head") continue;
        if (name === "p" && !isOpen("p", ["button", "table", "td", "th"])) { add(makeElement("p", "")); continue; }
        closeTo(name, ["table", "template"]);
        continue;
      }
      if (!inSvg && (name === "html" || name === "body" || name === "head")) {
        if (whole && name !== "head") for (const [k, v] of attrs) (name === "body" ? bodyEl : htmlEl).setAttribute(k, v);
        continue;
      }
      if (!inSvg) {
        if (CLOSES_P.has(name) && isOpen("p", ["button", "table", "td", "th", "li", "dd", "dt"])) closeTo("p");
        if (name === "li") closeTo("li", ["ul", "ol", "menu"]);
        if (name === "dt" || name === "dd") { closeTo("dt", ["dl"]); closeTo("dd", ["dl"]); }
        if (name === "tr") closeTo("tr", ["table", "thead", "tbody", "tfoot"]);
        if (name === "td" || name === "th") { closeTo("td", ["tr", "table"]); closeTo("th", ["tr", "table"]); }
        if (name === "thead" || name === "tbody" || name === "tfoot") { closeTo("thead", ["table"]); closeTo("tbody", ["table"]); closeTo("tfoot", ["table"]); }
        if (name === "option") closeTo("option", ["select", "datalist", "optgroup"]);
        if (name === "optgroup") { closeTo("option", ["select"]); closeTo("optgroup", ["select"]); }
      }
      const el = makeElement(name, inSvg ? "svg" : "");
      for (const [k, v] of attrs) if (!el._attrs.has(el._an(k))) el._attrs.set(el._an(k), v);
      if (el.hasAttribute("checked")) el._checked = undefined;
      const headOnly = whole && stack.length === 1 && !inSvg && (name === "meta" || name === "title" || name === "link" || name === "base");
      if (headOnly) headEl.appendChild(el);
      else add(el);
      if ((VOID.has(name) && !inSvg) || selfClose) continue;
      if (!inSvg && (RAW.has(name) || RCDATA.has(name))) {
        const endRe = new RegExp("</" + name + "\\s*>", "i");
        const rest = html.slice(i);
        const em = endRe.exec(rest);
        const body = em ? rest.slice(0, em.index) : rest;
        i = em ? i + em.index + em[0].length : len;
        if (body) el.appendChild(new Text(RCDATA.has(name) ? decode(body) : body));
        if (name === "title" && whole) document.title = el.textContent;
        continue;
      }
      stack.push(el);
    }
    function addText(s, raw) {
      if (!s) return;
      const t = raw ? s : decode(s);
      const last = top().lastChild;
      if (last && last.nodeType === 3) last.data += t;
      else add(new Text(t));
    }
  }

  function parseFragment(html, ns) {
    const f = new DocumentFragment();
    parseInto(f, html, ns || "", false);
    return f;
  }

  // ── selectors ──
  function cssEscape(s) { return String(s).replace(/([^\w-])/g, "\\$1"); }
  const selCache = new Map();
  function compile(sel) {
    const key = String(sel);
    let f = selCache.get(key);
    if (f) return f;
    const groups = splitTop(key, ",").map((g) => parseComplex(g.trim()));
    if (groups.some((g) => !g)) throw new SyntaxError("'" + key + "' is not a valid selector");
    f = (el, scope) => groups.some((g) => matchComplex(el, g, g.length - 1, scope));
    if (selCache.size > 500) selCache.clear();
    selCache.set(key, f);
    return f;
  }
  function splitTop(s, ch) {
    const out = []; let depth = 0, q = "", start = 0;
    for (let i = 0; i < s.length; i++) {
      const c = s[i];
      if (q) { if (c === "\\") i++; else if (c === q) q = ""; continue; }
      if (c === '"' || c === "'") q = c;
      else if (c === "(" || c === "[") depth++;
      else if (c === ")" || c === "]") depth--;
      else if (c === ch && depth === 0) { out.push(s.slice(start, i)); start = i + 1; }
    }
    out.push(s.slice(start));
    return out;
  }
  // a complex selector: [{comb, compound}] with comb the combinator before it (" ", ">", "+", "~")
  function parseComplex(s) {
    if (!s) return null;
    const parts = [];
    let i = 0, comb = " ";
    while (i < s.length) {
      while (i < s.length && /\s/.test(s[i])) { i++; comb = comb === " " || comb === "" ? " " : comb; }
      if (i >= s.length) break;
      if (">+~".includes(s[i])) { comb = s[i]; i++; continue; }
      let j = i, depth = 0, q = "";
      while (j < s.length) {
        const c = s[j];
        if (q) { if (c === "\\") j++; else if (c === q) q = ""; j++; continue; }
        if (c === '"' || c === "'") q = c;
        else if (c === "(" || c === "[") depth++;
        else if (c === ")" || c === "]") depth--;
        else if (depth === 0 && (/\s/.test(c) || ">+~".includes(c))) break;
        j++;
      }
      const compound = parseCompound(s.slice(i, j));
      if (!compound) return null;
      parts.push({ comb: parts.length ? comb : "", compound });
      comb = "";
      i = j;
    }
    return parts.length ? parts : null;
  }
  function parseCompound(s) {
    const tests = [];
    let i = 0;
    const ident = () => { const m = /^(?:\\.|[\w-]|[^\x00-\x7f])+/.exec(s.slice(i)); if (!m) return null; i += m[0].length; return m[0].replace(/\\(.)/g, "$1"); };
    if (s[i] === "*") i++;
    else if (/[a-zA-Z]/.test(s[i] || "")) { const t = ident(); tests.push((el) => el.localName.toLowerCase() === t.toLowerCase()); }
    while (i < s.length) {
      const c = s[i];
      if (c === "#") { i++; const v = ident(); if (v === null) return null; tests.push((el) => el.getAttribute("id") === v); }
      else if (c === ".") { i++; const v = ident(); if (v === null) return null; tests.push((el) => (el.getAttribute("class") || "").split(/\s+/).includes(v)); }
      else if (c === "[") {
        const e = s.indexOf("]", i);
        if (e < 0) return null;
        const m = /^\s*([^\s~|^$*!=]+)\s*(?:([~|^$*]?=)\s*(?:"([^"]*)"|'([^']*)'|([^\s\]]+))\s*(i)?)?\s*$/.exec(s.slice(i + 1, e));
        if (!m) return null;
        const [, an, op, v1, v2, v3, ci] = m;
        const want = v1 !== undefined ? v1 : v2 !== undefined ? v2 : v3;
        tests.push((el) => {
          let v = el.getAttribute(an);
          if (v === null) return false;
          if (!op) return true;
          let w = want;
          if (ci) { v = v.toLowerCase(); w = w.toLowerCase(); }
          switch (op) {
            case "=": return v === w;
            case "~=": return v.split(/\s+/).includes(w);
            case "|=": return v === w || v.startsWith(w + "-");
            case "^=": return w !== "" && v.startsWith(w);
            case "$=": return w !== "" && v.endsWith(w);
            case "*=": return w !== "" && v.includes(w);
          }
          return false;
        });
        i = e + 1;
      } else if (c === ":") {
        i++;
        if (s[i] === ":") { i++; ident(); continue; } // pseudo-elements: match the element
        const name = ident();
        if (!name) return null;
        let arg = null;
        if (s[i] === "(") {
          let depth = 1, j = i + 1;
          while (j < s.length && depth) { if (s[j] === "(") depth++; else if (s[j] === ")") depth--; j++; }
          arg = s.slice(i + 1, j - 1).trim();
          i = j;
        }
        const t = pseudo(name.toLowerCase(), arg);
        if (!t) return null;
        tests.push(t);
      } else return null;
    }
    return tests;
  }
  function nth(arg) {
    const a = arg.replace(/\s+/g, "").toLowerCase();
    if (a === "odd") return (n) => n % 2 === 1;
    if (a === "even") return (n) => n % 2 === 0;
    const m = /^([+-]?\d*)n([+-]\d+)?$/.exec(a);
    if (m) {
      const k = m[1] === "" || m[1] === "+" ? 1 : m[1] === "-" ? -1 : parseInt(m[1], 10);
      const b = m[2] ? parseInt(m[2], 10) : 0;
      return (n) => (k === 0 ? n === b : (n - b) / k >= 0 && (n - b) % k === 0);
    }
    const b = parseInt(a, 10);
    return Number.isNaN(b) ? null : (n) => n === b;
  }
  function pseudo(name, arg) {
    const sibs = (el) => (el.parentNode ? el.parentNode.children : [el]);
    switch (name) {
      case "first-child": return (el) => sibs(el)[0] === el;
      case "last-child": return (el) => { const s = sibs(el); return s[s.length - 1] === el; };
      case "only-child": return (el) => sibs(el).length === 1;
      case "first-of-type": return (el) => sibs(el).find((x) => x.localName === el.localName) === el;
      case "last-of-type": return (el) => { const s = sibs(el).filter((x) => x.localName === el.localName); return s[s.length - 1] === el; };
      case "nth-child": { const f = arg && nth(arg); return f ? (el) => f(sibs(el).indexOf(el) + 1) : null; }
      case "nth-last-child": { const f = arg && nth(arg); return f ? (el) => { const s = sibs(el); return f(s.length - s.indexOf(el)); } : null; }
      case "nth-of-type": { const f = arg && nth(arg); return f ? (el) => f(sibs(el).filter((x) => x.localName === el.localName).indexOf(el) + 1) : null; }
      case "not": { if (!arg) return null; const m = compile(arg); return (el) => !m(el, null); }
      case "is": case "where": case "matches": { if (!arg) return null; const m = compile(arg); return (el) => m(el, null); }
      case "has": { if (!arg) return null; return (el) => query(el, arg.replace(/^\s*>/, ":scope >"), true).length > 0; }
      case "checked": return (el) => (el.localName === "option" ? el.selected : !!el.checked);
      case "disabled": return (el) => el.hasAttribute("disabled");
      case "enabled": return (el) => !el.hasAttribute("disabled");
      case "empty": return (el) => el.childNodes.every((c) => c.nodeType === 8);
      case "root": return (el) => el === htmlEl;
      case "scope": return (el, scope) => (scope ? el === scope : el === htmlEl);
      case "focus": case "focus-visible": case "focus-within": return (el) => document.activeElement === el;
      case "hover": case "active": case "visited": case "target": return () => false;
      case "link": case "any-link": return (el) => el.localName === "a" && el.hasAttribute("href");
      case "required": return (el) => el.hasAttribute("required");
      case "optional": return (el) => !el.hasAttribute("required");
      case "read-only": return (el) => el.hasAttribute("readonly");
      case "placeholder-shown": return (el) => !!el.getAttribute("placeholder") && !el.value;
      case "open": return (el) => el.hasAttribute("open");
      default: return null;
    }
  }
  function matchCompound(el, tests, scope) { for (const t of tests) if (!t(el, scope)) return false; return true; }
  function matchComplex(el, parts, i, scope) {
    if (!el || el.nodeType !== 1 || !matchCompound(el, parts[i].compound, scope)) return false;
    if (i === 0) return true;
    const comb = parts[i].comb;
    if (comb === ">") return matchComplex(el.parentElement, parts, i - 1, scope);
    if (comb === "+") return matchComplex(el.previousElementSibling, parts, i - 1, scope);
    if (comb === "~") { for (let x = el.previousElementSibling; x; x = x.previousElementSibling) if (matchComplex(x, parts, i - 1, scope)) return true; return false; }
    for (let x = el.parentElement; x; x = x.parentElement) if (matchComplex(x, parts, i - 1, scope)) return true;
    return false;
  }
  function query(root, sel, first) {
    const m = compile(sel);
    const out = [];
    const scope = root.nodeType === 1 ? root : null;
    const walk = (n) => {
      for (const c of n.childNodes) {
        if (c.nodeType !== 1) continue;
        if (m(c, scope)) { out.push(c); if (first) return true; }
        if (walk(c)) return true;
      }
      return false;
    };
    walk(root);
    out.item = (i) => out[i] || null;
    return out;
  }

  // ── events ──
  class Event {
    constructor(type, init) {
      init = init || {};
      this.type = String(type);
      this.bubbles = !!init.bubbles;
      this.cancelable = !!init.cancelable;
      this.composed = !!init.composed;
      this.defaultPrevented = false;
      this.target = null;
      this.currentTarget = null;
      this.eventPhase = 0;
      this.timeStamp = Date.now() - started;
      this.isTrusted = false;
      this._stop = false;
      this._stopNow = false;
      this.detail = init.detail === undefined ? null : init.detail;
    }
    preventDefault() { if (this.cancelable) this.defaultPrevented = true; }
    stopPropagation() { this._stop = true; }
    stopImmediatePropagation() { this._stop = true; this._stopNow = true; }
    composedPath() { return this._path || []; }
    get returnValue() { return !this.defaultPrevented; }
    set returnValue(v) { if (!v) this.preventDefault(); }
    initEvent(type, bubbles, cancelable) { this.type = String(type); this.bubbles = !!bubbles; this.cancelable = !!cancelable; }
  }
  class CustomEvent extends Event {}
  const makeEvent = (type, init) => Object.assign(new Event(type, init), init || {});

  const inline = new Map();
  function handlerOf(node, type) {
    const code = node.nodeType === 1 ? node.getAttribute("on" + type) : null;
    if (code !== null) {
      let fn = inline.get(code);
      if (!fn) {
        try { fn = new Function("event", code); } catch (e) { reportError(e); fn = () => {}; }
        inline.set(code, fn);
      }
      return fn;
    }
    const prop = node["on" + type];
    return typeof prop === "function" ? prop : null;
  }

  function dispatch(target, ev, trusted) {
    if (!(ev instanceof Event)) throw new TypeError("dispatchEvent: not an event");
    ev.target = target;
    ev.isTrusted = !!trusted;
    const path = [];
    for (let x = target; x; x = x.parentNode) path.push(x);
    if (path[path.length - 1] === document) path.push(G);
    ev._path = path;
    const call = (node, phase) => {
      ev.currentTarget = node;
      ev.eventPhase = phase;
      const list = node._listeners && node._listeners[ev.type];
      if (phase !== 1) {
        const h = handlerOf(node, ev.type);
        if (h) {
          try { if (h.call(node, ev) === false) ev.preventDefault(); } catch (e) { reportError(e); }
          if (ev._stopNow) return;
        }
      }
      if (!list) return;
      for (const l of [...list]) {
        if (phase === 1 && !l.capture) continue;
        if (phase === 3 && l.capture) continue;
        if (l.once) node.removeEventListener(ev.type, l.fn, l.capture);
        try { if (typeof l.fn === "function") l.fn.call(node, ev); else l.fn.handleEvent(ev); } catch (e) { reportError(e); }
        if (ev._stopNow) return;
      }
    };
    for (let i = path.length - 1; i > 0 && !ev._stop; i--) call(path[i], 1);
    if (!ev._stop) call(target, 2);
    if (ev.bubbles) for (let i = 1; i < path.length && !ev._stop; i++) call(path[i], 3);
    ev.currentTarget = null;
    ev.eventPhase = 0;
    if (trusted && !ev.defaultPrevented) defaults(target, ev);
    return !ev.defaultPrevented;
  }

  // What a press does when the card lets it: a link asks to open its address; a submit button or Enter in a form's
  // field sends the form a submit event (it goes nowhere).
  function defaults(target, ev) {
    if (ev.type === "click") {
      const a = target.closest && target.closest("a[href]");
      if (a) { const href = a.getAttribute("href"); if (/^https?:/i.test(href)) bridgeCall("open", href); return; }
      const btn = target.closest && target.closest("button,input");
      if (btn && btn.form && (btn.type === "submit" || btn.type === "image")) dispatch(btn.form, makeEvent("submit", { bubbles: true, cancelable: true }), false);
    }
    if (ev.type === "keydown" && ev.key === "Enter" && target.localName === "input" && target.form) dispatch(target.form, makeEvent("submit", { bubbles: true, cancelable: true }), false);
  }

  // ── timers (the worker keeps them; they die with it) ──
  const started = Date.now();
  let timerSeq = 0;
  const timers = new Map();
  let refused = 0;
  function addTimer(fn, ms, args, repeat, frame) {
    if (typeof fn !== "function") return 0;
    if (timers.size >= H.limits.timers) { refused++; return 0; }
    const id = ++timerSeq;
    const delay = frame ? H.limits.frame : Math.max(H.limits.timerMin, Number(ms) || 0);
    timers.set(id, { fn, args, repeat, delay, frame });
    H.timer(id, delay);
    return id;
  }
  function clearTimer(id) { if (timers.delete(Number(id))) H.clearTimer(Number(id)); }

  // ── the 2D canvas: drawn by the worker on an OffscreenCanvas, shown by the page ──
  const C2D_METHODS = ["fillRect", "strokeRect", "clearRect", "beginPath", "closePath", "moveTo", "lineTo", "arc", "arcTo", "bezierCurveTo", "quadraticCurveTo", "rect", "roundRect", "ellipse", "fill", "stroke", "clip", "save", "restore", "translate", "rotate", "scale", "transform", "setTransform", "resetTransform", "fillText", "strokeText", "setLineDash", "reset"];
  const C2D_PROPS = { fillStyle: "#000000", strokeStyle: "#000000", lineWidth: 1, font: "10px sans-serif", textAlign: "start", textBaseline: "alphabetic", globalAlpha: 1, lineCap: "butt", lineJoin: "miter", miterLimit: 10, lineDashOffset: 0, shadowColor: "rgba(0, 0, 0, 0)", shadowBlur: 0, shadowOffsetX: 0, shadowOffsetY: 0, globalCompositeOperation: "source-over", imageSmoothingEnabled: true, direction: "inherit", filter: "none", letterSpacing: "0px", wordSpacing: "0px", fontKerning: "auto" };
  const canvasCmds = new Map();
  let gradSeq = 0;
  class Gradient {
    constructor(kind, args) { this._g = { g: ++gradSeq, kind, args: args.map(Number), stops: [] }; }
    addColorStop(o, c) { this._g.stops.push([Number(o), String(c)]); }
  }
  class Context2D {
    constructor(canvas) { this.canvas = canvas; this._state = Object.assign({}, C2D_PROPS); this._stack = []; this._dash = []; }
    _push(cmd) {
      const id = this.canvas._id;
      let list = canvasCmds.get(id);
      if (!list) { list = []; canvasCmds.set(id, list); }
      if (list.length > 20000) list.splice(0, list.length - 10000);
      list.push(cmd);
    }
    _reset() { this._state = Object.assign({}, C2D_PROPS); this._stack = []; canvasCmds.set(this.canvas._id, [["#size", this.canvas.width, this.canvas.height]]); }
    measureText(t) { flushCanvas(); return JSON.parse(H.measure(this.canvas._id, String(this._state.font), String(t))); }
    getLineDash() { return this._dash.slice(); }
    createLinearGradient(...a) { return new Gradient("linear", a); }
    createRadialGradient(...a) { return new Gradient("radial", a); }
    createConicGradient(...a) { return new Gradient("conic", a); }
    createPattern() { return null; }
    drawImage() { console.warn("drawImage is not available in cards"); }
    getImageData(_x, _y, w, h) { return { width: w, height: h, data: new Uint8ClampedArray(Math.max(0, w * h * 4) | 0) }; }
    putImageData() {}
    createImageData(w, h) { return { width: w, height: h, data: new Uint8ClampedArray(Math.max(0, w * h * 4) | 0) }; }
    isPointInPath() { return false; }
    isPointInStroke() { return false; }
    getTransform() { return { a: 1, b: 0, c: 0, d: 1, e: 0, f: 0 }; }
  }
  for (const m of C2D_METHODS) Context2D.prototype[m] = function (...args) {
    if (m === "save") this._stack.push(Object.assign({}, this._state));
    if (m === "restore") { const s = this._stack.pop(); if (s) this._state = s; }
    if (m === "setLineDash") this._dash = Array.isArray(args[0]) ? args[0].map(Number) : [];
    if (m === "reset") { this._state = Object.assign({}, C2D_PROPS); this._stack = []; }
    this._push([m, ...args.map((a) => (Array.isArray(a) ? a.map(Number) : typeof a === "object" && a !== null ? (a instanceof Gradient ? a._g : null) : a))]);
  };
  for (const p of Object.keys(C2D_PROPS)) Object.defineProperty(Context2D.prototype, p, {
    get() { return this._state[p]; },
    set(v) {
      const val = v instanceof Gradient ? v : typeof v === "object" ? String(v) : v;
      this._state[p] = val;
      this._push(["=", p, val instanceof Gradient ? JSON.parse(JSON.stringify(val._g)) : val]);
    },
  });
  function flushCanvas() {
    if (!canvasCmds.size) return;
    const out = {};
    for (const [id, cmds] of canvasCmds) { const el = byId.get(id); if (el) out[id] = { w: el.width, h: el.height, cmds }; }
    canvasCmds.clear();
    H.canvas(JSON.stringify(out));
  }

  // ── the bridge ──
  let bridgeSeq = 0;
  const pending = new Map();
  function bridgeCall(kind, value) {
    const id = ++bridgeSeq;
    return new Promise((resolve) => { pending.set(id, resolve); H.bridge(id, kind, value); });
  }
  const persona = Object.freeze({
    reply(text) {
      if (typeof text !== "string") return Promise.reject(new TypeError("persona.reply: text must be a string"));
      return bridgeCall("reply", text);
    },
    open(url) {
      if (typeof url !== "string") return Promise.reject(new TypeError("persona.open: url must be a string"));
      return bridgeCall("open", url);
    },
  });

  // ── errors and the console ──
  function reportError(e) {
    let msg;
    try { msg = e && e.stack ? String(e.message) + "\n" + String(e.stack).split("\n").slice(0, 3).join("\n") : String(e); } catch (_) { msg = "error"; }
    H.error(msg.slice(0, 500));
  }
  const fmt = (args) => args.map((a) => { try { return typeof a === "string" ? a : JSON.stringify(a); } catch (_) { return String(a); } }).join(" ").slice(0, 500);
  const console = {};
  for (const lvl of ["log", "info", "warn", "error", "debug", "trace"]) console[lvl] = (...a) => H.log(lvl, fmt(a));
  for (const f of ["group", "groupCollapsed", "groupEnd", "table", "time", "timeEnd", "timeLog", "count", "countReset", "assert", "dir", "dirxml", "clear"]) console[f] = () => {};

  // ── Intl, from the worker's own (QuickJS has none) ──
  const intlFormat = (kind, locale, opts, v) => H.intl(kind, Array.isArray(locale) ? String(locale[0] || "") : locale ? String(locale) : "", JSON.stringify(opts || {}), String(v));
  const Intl = {
    NumberFormat: function NumberFormat(locale, opts) {
      if (!(this instanceof NumberFormat)) return new NumberFormat(locale, opts);
      this.format = (v) => intlFormat("number", locale, opts, Number(v));
      this.formatToParts = (v) => [{ type: "literal", value: this.format(v) }];
      this.resolvedOptions = () => Object.assign({ locale: locale || info.lang }, opts || {});
    },
    DateTimeFormat: function DateTimeFormat(locale, opts) {
      if (!(this instanceof DateTimeFormat)) return new DateTimeFormat(locale, opts);
      this.format = (d) => intlFormat("date", locale, opts, d === undefined ? Date.now() : new Date(d).getTime());
      this.formatToParts = (d) => [{ type: "literal", value: this.format(d) }];
      this.resolvedOptions = () => Object.assign({ locale: locale || info.lang, timeZone: "UTC" }, opts || {});
    },
    PluralRules: function PluralRules() { this.select = (n) => (n === 1 ? "one" : "other"); },
    Collator: function Collator() { this.compare = (a, b) => (String(a) < String(b) ? -1 : String(a) > String(b) ? 1 : 0); },
    RelativeTimeFormat: function RelativeTimeFormat(locale, opts) { this.format = (v, unit) => intlFormat("relative", locale, Object.assign({ unit }, opts || {}), Number(v)); },
    getCanonicalLocales: (l) => (Array.isArray(l) ? l : l ? [l] : []),
  };
  Number.prototype.toLocaleString = function (locale, opts) { return intlFormat("number", locale, opts, Number(this)); };
  Date.prototype.toLocaleString = function (locale, opts) { return intlFormat("date", locale, Object.assign({ dateStyle: "medium", timeStyle: "short" }, opts || {}), this.getTime()); };
  Date.prototype.toLocaleDateString = function (locale, opts) { return intlFormat("date", locale, opts || { dateStyle: "medium" }, this.getTime()); };
  Date.prototype.toLocaleTimeString = function (locale, opts) { return intlFormat("date", locale, opts || { timeStyle: "short" }, this.getTime()); };

  // ── the window ──
  const LOCATION = Object.freeze({ href: "about:srcdoc", origin: "null", protocol: "about:", host: "", hostname: "", port: "", pathname: "srcdoc", search: "", hash: "", assign() {}, replace() {}, reload() {}, toString() { return "about:srcdoc"; } });
  const media = (q) => {
    const s = String(q);
    let matches = false;
    if (/prefers-color-scheme:\s*dark/.test(s)) matches = info.dark;
    else if (/prefers-color-scheme:\s*light/.test(s)) matches = !info.dark;
    else { const mw = /max-width:\s*(\d+)px/.exec(s); const nw = /min-width:\s*(\d+)px/.exec(s); if (mw || nw) matches = (!mw || W <= +mw[1]) && (!nw || W >= +nw[1]); }
    return { matches, media: s, onchange: null, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {}, dispatchEvent() { return true; } };
  };
  const winTarget = new EventTarget();
  const define = (name, value) => Object.defineProperty(G, name, { value, writable: true, configurable: true, enumerable: false });
  // An accessor, not a read-only value: QuickJS lets a script redefine a non-configurable data property of the global
  // object, but not an accessor. So the card can neither replace nor redefine these.
  const fixed = (name, value) => Object.defineProperty(G, name, { get: () => value, set: undefined, configurable: false, enumerable: false });
  fixed("persona", persona);
  fixed("document", document);
  fixed("location", LOCATION);
  define("window", G);
  define("self", G);
  define("globalThis", G);
  define("parent", G);
  define("top", G);
  define("frames", G);
  define("opener", null);
  define("console", console);
  define("Intl", Intl);
  define("Node", Node);
  define("Element", Element);
  define("HTMLElement", Element);
  define("SVGElement", Element);
  define("HTMLInputElement", Element);
  define("HTMLCanvasElement", Element);
  define("Text", Text);
  define("Comment", Comment);
  define("Document", Document);
  define("DocumentFragment", DocumentFragment);
  define("EventTarget", EventTarget);
  define("Event", Event);
  define("CustomEvent", CustomEvent);
  define("MouseEvent", Event);
  define("KeyboardEvent", Event);
  define("InputEvent", Event);
  define("FocusEvent", Event);
  define("PointerEvent", Event);
  define("CanvasRenderingContext2D", Context2D);
  define("CanvasGradient", Gradient);
  define("navigator", Object.freeze({ language: info.lang, languages: Object.freeze([info.lang]), userAgent: "Persona card", platform: "", onLine: false, hardwareConcurrency: 1, maxTouchPoints: 0, cookieEnabled: false }));
  define("screen", Object.freeze({ width: W, height: Hh, availWidth: W, availHeight: Hh, colorDepth: 24, pixelDepth: 24 }));
  define("history", Object.freeze({ length: 1, state: null, pushState() {}, replaceState() {}, back() {}, forward() {}, go() {} }));
  define("performance", Object.freeze({ now: () => H.now(), timeOrigin: started, mark() {}, measure() {}, getEntriesByName: () => [], getEntriesByType: () => [] }));
  define("setTimeout", (fn, ms, ...a) => addTimer(fn, ms, a, false, false));
  define("setInterval", (fn, ms, ...a) => addTimer(fn, ms, a, true, false));
  define("clearTimeout", clearTimer);
  define("clearInterval", clearTimer);
  define("requestAnimationFrame", (fn) => addTimer(fn, 0, [], false, true));
  define("cancelAnimationFrame", clearTimer);
  define("requestIdleCallback", (fn) => addTimer(() => fn({ didTimeout: false, timeRemaining: () => 0 }), 50, [], false, false));
  define("cancelIdleCallback", clearTimer);
  define("queueMicrotask", (fn) => { Promise.resolve().then(fn).catch(reportError); });
  define("structuredClone", (v) => JSON.parse(JSON.stringify(v)));
  define("matchMedia", media);
  define("getComputedStyle", (el) => (el && el.style ? el.style : styleOf(new Element("div", ""))));
  define("alert", () => {});
  define("confirm", () => false);
  define("prompt", () => null);
  define("print", () => {});
  define("open", () => null);
  define("close", () => {});
  define("focus", () => {});
  define("blur", () => {});
  define("scrollTo", () => {});
  define("scrollBy", () => {});
  define("scroll", () => {});
  define("postMessage", () => {});
  define("addEventListener", (t, f, o) => winTarget.addEventListener(t, f, o));
  define("removeEventListener", (t, f, o) => winTarget.removeEventListener(t, f, o));
  define("dispatchEvent", (e) => winTarget.dispatchEvent(e));
  Object.defineProperty(G, "_listeners", { get: () => winTarget._listeners, configurable: true });
  define("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  define("IntersectionObserver", class { constructor(cb) { this._cb = cb; } observe(el) { const cb = this._cb; setTimeout(() => cb([{ target: el, isIntersecting: true, intersectionRatio: 1, boundingClientRect: rect(el), intersectionRect: rect(el) }], this), 0); } unobserve() {} disconnect() {} takeRecords() { return []; } });
  define("MutationObserver", class { observe() {} disconnect() {} takeRecords() { return []; } });
  define("DOMParser", class { parseFromString(s) { const f = parseFragment(String(s), ""); return { body: f, documentElement: f, querySelector: (q) => f.querySelector(q), querySelectorAll: (q) => f.querySelectorAll(q), getElementById: (i) => f.getElementById(i) }; } });
  for (const name of ["innerWidth", "outerWidth"]) Object.defineProperty(G, name, { get: () => W, configurable: true });
  for (const name of ["innerHeight", "outerHeight"]) Object.defineProperty(G, name, { get: () => Hh, configurable: true });
  define("devicePixelRatio", info.dpr);
  define("scrollX", 0);
  define("scrollY", 0);
  define("pageXOffset", 0);
  define("pageYOffset", 0);
  define("isSecureContext", false);
  define("origin", "null");
  define("name", "");
  define("closed", false);
  define("length", 0);
  define("frameElement", null);
  for (const t of ["click", "load", "error", "input", "change", "submit", "keydown", "resize", "message"]) Object.defineProperty(G, "on" + t, { value: null, writable: true, configurable: true });

  // ── what the worker calls ──
  const scripts = [];
  const api = {
    // Parses the card; its scripts are run one at a time by run(i), each its own turn.
    load(html) {
      const f = new DocumentFragment();
      parseInto(f, String(html), "", true);
      bodyEl.appendChild(f);
      for (const s of query(document, "script", false)) {
        const type = (s.getAttribute("type") || "").trim().toLowerCase();
        if (type && !/^(text\/javascript|application\/javascript|module|text\/ecmascript)$/.test(type)) continue;
        if (s.hasAttribute("src")) continue; // nothing loads: the script never runs
        scripts.push(s);
      }
      return scripts.length;
    },
    run(i) {
      const s = scripts[i];
      if (!s) return;
      document.currentScript = s;
      try { (0, eval)(s.textContent); } catch (e) { if (isFatal(e)) throw e; reportError(e); }
      document.currentScript = null;
    },
    loaded() {
      document.readyState = "interactive";
      dispatch(document, makeEvent("readystatechange", {}));
      dispatch(document, makeEvent("DOMContentLoaded", { bubbles: true }));
      document.readyState = "complete";
      dispatch(document, makeEvent("readystatechange", {}));
      const load = makeEvent("load", {});
      load.target = document;
      winTarget.dispatchEvent(load);
      if (typeof G.onload === "function") try { G.onload(load); } catch (e) { reportError(e); }
    },
    event(json) {
      const e = JSON.parse(json);
      const target = byId.get(e.target);
      if (!target) return;
      if (e.value !== undefined) target._value = e.value;
      if (e.checked !== undefined) {
        target._checked = !!e.checked;
        if (e.checked && target.type === "radio" && target.name) for (const r of radios(target)) if (r !== target) r._checked = false;
      }
      if (e.selectedIndex !== undefined && target.localName === "select") (target.options || []).forEach((o, j) => o._selectedSilently(j === e.selectedIndex));
      if (e.open !== undefined) { if (e.open) target._attrs.set("open", ""); else target._attrs.delete("open"); }
      if (e.type === "focus") { document.activeElement = target; return; }
      const ev = makeEvent(e.type, { bubbles: e.type !== "toggle", cancelable: e.type === "click" || e.type === "keydown" || e.type === "submit" });
      for (const k of ["key", "shiftKey", "ctrlKey", "altKey", "metaKey"]) if (e[k] !== undefined) ev[k] = e[k];
      if (e.type === "click") { ev.button = 0; ev.detail = 1; ev.clientX = 0; ev.clientY = 0; }
      if (e.type === "keydown") ev.code = e.key && e.key.length === 1 ? "Key" + e.key.toUpperCase() : e.key;
      dispatch(target, ev, true);
    },
    fire(id) {
      const t = timers.get(id);
      if (!t) return;
      if (t.repeat) H.timer(id, t.delay);
      else timers.delete(id);
      try { t.fn.apply(G, t.frame ? [H.now()] : t.args); } catch (e) { if (isFatal(e)) throw e; reportError(e); }
    },
    settle(id, ok) {
      const r = pending.get(id);
      if (r) { pending.delete(id); r(!!ok); }
    },
    resize(w, h) {
      W = w; Hh = h;
      const ev = makeEvent("resize", {});
      winTarget.dispatchEvent(ev);
      if (typeof G.onresize === "function") try { G.onresize(ev); } catch (e) { reportError(e); }
    },
    // The operations since the last flush, and the canvases drawn meanwhile (sent to the worker first).
    flush() {
      flushCanvas();
      if (!ops.length) return "";
      const out = JSON.stringify(ops);
      ops = [];
      return out;
    },
    refusedTimers() { const n = refused; refused = 0; return n; },
  };
  // the interrupt (too long a turn) and out of memory are the runtime's, not the card's to catch and go on
  function isFatal(e) { return e && (e.name === "InternalError") && /interrupted|out of memory/.test(String(e.message)); }
  return api;
})
