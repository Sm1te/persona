// The display frame's one script (#4215; inlined into the frame by sanitize.ts frameShell, allowed by its nonce). The
// frame is sandboxed with allow-scripts only: an opaque origin, without the app's storage, cookies or DOM, and its CSP
// lets nothing load. The card's own code never runs here. This script draws the operations the page already checked
// (sanitize.ts Sanitizer), with createElement / setAttribute / textContent only, and tells the page what the user did
// (the target's id and the control's state). It takes messages from its parent only, carrying its nonce; a press on a
// link or a submit is stopped here too, though the page never draws an href or a form.
(function () {
  "use strict";
  var me = document.currentScript;
  var N = me.nonce;
  // the body starts empty: this script is all that's in it, and leaves
  me.remove();
  var P = window.parent;
  var D = document;
  var SVG = "http://www.w3.org/2000/svg";
  var create = D.createElement.bind(D);
  var createNS = D.createElementNS.bind(D);
  var createText = D.createTextNode.bind(D);
  var nodes = new Map();
  var ids = new WeakMap();
  var post = function (m) {
    m.n = N;
    P.postMessage(m, "*");
  };
  var reg = function (id, n) {
    nodes.set(id, n);
    ids.set(n, id);
  };
  reg(1, D.documentElement);
  reg(2, D.head);
  reg(3, D.body);

  function prop(el, name, value) {
    try {
      el[name] = value;
    } catch (e) {
      /* not this element's */
    }
  }
  function make(t) {
    if (t[1] === "#text") {
      var tn = createText(String(t[2]));
      reg(t[0], tn);
      return tn;
    }
    var el = t[2] === "svg" ? createNS(SVG, t[1]) : create(t[1]);
    reg(t[0], el);
    var attrs = t[3] || [];
    for (var i = 0; i < attrs.length; i++) {
      try {
        el.setAttribute(attrs[i][0], attrs[i][1]);
      } catch (e) {
        /* not an attribute name */
      }
    }
    var kids = t[4] || [];
    for (var j = 0; j < kids.length; j++) {
      if (kids[j][1] === "#css") el.textContent = String(kids[j][2]);
      else el.appendChild(make(kids[j]));
    }
    var props = t[5] || {};
    for (var k in props) if (Object.prototype.hasOwnProperty.call(props, k)) prop(el, k, props[k]);
    return el;
  }
  function forget(n) {
    var w = D.createTreeWalker(n, 5);
    for (var x = w.currentNode; x; x = w.nextNode()) {
      var id = ids.get(x);
      if (id) nodes.delete(id);
    }
  }
  function apply(op) {
    var n = nodes.get(op[1]);
    switch (op[0]) {
      case "t": {
        if (!n) return;
        var before = op[2] ? nodes.get(op[2]) : null;
        n.insertBefore(make(op[3]), before && before.parentNode === n ? before : null);
        return;
      }
      case "r":
        if (n && op[1] > 3) {
          forget(n);
          if (n.parentNode) n.parentNode.removeChild(n);
        }
        return;
      case "a":
        if (!n || n.nodeType !== 1) return;
        try {
          if (op[3] === null) n.removeAttribute(op[2]);
          else n.setAttribute(op[2], op[3]);
        } catch (e) {
          /* not an attribute name */
        }
        return;
      case "d":
        if (n && n.nodeType === 3) n.data = String(op[2]);
        return;
      case "p":
        if (n && n.nodeType === 1) prop(n, op[2], op[3]);
        return;
      case "s":
        if (n && n.nodeType === 1) n.textContent = String(op[2]);
        return;
      case "f":
        // only while the card has the focus already: it never takes it from the rest of the app
        if (n && n.focus && D.hasFocus()) n.focus({ preventScroll: true });
        return;
    }
  }

  window.addEventListener("message", function (e) {
    var m = e.data;
    if (e.source !== P || !m || m.n !== N) return;
    if (m.ops) for (var i = 0; i < m.ops.length; i++) apply(m.ops[i]);
    if (m.bm) {
      var c = nodes.get(m.bm);
      if (c && c.nodeName === "CANVAS" && m.b) {
        var ctx = c.getContext("bitmaprenderer");
        if (ctx) ctx.transferFromImageBitmap(m.b);
        else m.b.close();
      } else if (m.b) m.b.close();
    }
  });

  function idOf(n) {
    for (var x = n; x; x = x.parentNode) {
      var id = ids.get(x);
      if (id) return id;
    }
    return 0;
  }
  function state(el) {
    if (!el || el.nodeType !== 1) return {};
    switch (el.nodeName) {
      case "INPUT":
        return el.type === "checkbox" || el.type === "radio" ? { checked: el.checked, value: el.value } : { value: el.value };
      case "TEXTAREA":
        return { value: el.value };
      case "SELECT":
        return { value: el.value, selectedIndex: el.selectedIndex };
      case "DETAILS":
        return { open: el.open };
    }
    return {};
  }
  function forward(e) {
    var target = idOf(e.target);
    if (!target) return;
    var ev = state(e.target);
    ev.type = e.type === "focusin" ? "focus" : e.type;
    ev.target = target;
    if (e.type === "keydown") {
      ev.key = e.key;
      ev.shiftKey = e.shiftKey;
      ev.ctrlKey = e.ctrlKey;
      ev.altKey = e.altKey;
      ev.metaKey = e.metaKey;
    }
    post({ ev: ev });
  }
  D.addEventListener(
    "click",
    function (e) {
      var t = e.target && e.target.closest ? e.target.closest("a, area, form, [type=submit], [type=image]") : null;
      if (t) e.preventDefault();
      forward(e);
    },
    true,
  );
  D.addEventListener("submit", function (e) { e.preventDefault(); }, true);
  ["input", "change", "keydown", "toggle", "focusin"].forEach(function (t) {
    D.addEventListener(t, forward, true);
  });
  // What the card draws decides the frame's height: the body's height (a flow-root, so floats count) goes to the page
  // whenever it changes, once a frame at most. The page holds it between its least and most (ui/HtmlCard.tsx).
  var lastH = -1;
  var asked = false;
  function reportHeight() {
    asked = false;
    var h = Math.ceil(D.body.getBoundingClientRect().height);
    if (h !== lastH) {
      lastH = h;
      post({ h: h });
    }
  }
  if (typeof ResizeObserver === "function") {
    new ResizeObserver(function () {
      if (!asked) {
        asked = true;
        requestAnimationFrame(reportHeight);
      }
    }).observe(D.body);
  }
  post({ ready: true });
})();
