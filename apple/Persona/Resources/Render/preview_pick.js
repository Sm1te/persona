// Called on the element picked in a preview (docs/web/preview.md "Picking an element"), as `this`: what the comment the
// agent gets says about it. Returns plain data only.
function () {
  const el = this && this.nodeType === 1 ? this : this && this.parentElement;
  if (!el) return null;
  const esc = (s) => (window.CSS && CSS.escape ? CSS.escape(s) : s);
  // class names a build tool generated say nothing to a reader: css-1x2y3z, sc-abc, svelte-xyz, jsx-123…
  const generated = /^(css|sc|jsx|svelte|emotion|tw)-|^[a-z]{1,3}[A-Za-z0-9_-]*[0-9][A-Za-z0-9_-]{4,}$/;
  const part = (e) => {
    let s = e.localName;
    if (e.id) return s + "#" + esc(e.id);
    const cls = Array.from(e.classList).filter((c) => !generated.test(c)).slice(0, 2);
    if (cls.length) s += "." + cls.map(esc).join(".");
    const p = e.parentElement;
    if (p) {
      const same = Array.from(p.children).filter((c) => c.localName === e.localName);
      if (same.length > 1) s += ":nth-of-type(" + (same.indexOf(e) + 1) + ")";
    }
    return s;
  };
  const path = [];
  for (let e = el; e && e.nodeType === 1 && path.length < 8; e = e.parentElement) {
    path.unshift(part(e));
    if (e.id || e.localName === "body") break;
    try {
      if (document.querySelectorAll(path.join(" > ")).length === 1) break;
    } catch (_) {}
  }
  // the component that drew it, and where its code is, when a dev build says (React 18's fibers, Vue 3, Svelte)
  let component = "";
  let source = null;
  try {
    const key = Object.keys(el).find((k) => k.startsWith("__reactFiber$") || k.startsWith("__reactInternalInstance$"));
    for (let f = key ? el[key] : null, i = 0; f && i < 40; f = f.return, i++) {
      if (!source && f._debugSource) source = { file: String(f._debugSource.fileName || ""), line: f._debugSource.lineNumber || 0 };
      const t = f.type;
      const name = t && typeof t !== "string" ? t.displayName || t.name : "";
      if (name) {
        component = name;
        break;
      }
    }
    const vue = el.__vueParentComponent;
    if (!component && vue && vue.type) {
      component = vue.type.__name || vue.type.name || "";
      if (vue.type.__file) source = { file: String(vue.type.__file), line: 0 };
    }
    const sv = el.__svelte_meta;
    if (!source && sv && sv.loc) source = { file: String(sv.loc.file || ""), line: (sv.loc.line || 0) + 1 };
  } catch (_) {}
  const r = el.getBoundingClientRect();
  let html = el.outerHTML || "";
  if (html.length > 6000) html = html.slice(0, 6000);
  const text = String(el.innerText || el.textContent || "").trim().replace(/\s+/g, " ").slice(0, 300);
  return {
    selector: path.join(" > "),
    tag: el.localName,
    text,
    html,
    role: el.getAttribute("role") || "",
    label: el.getAttribute("aria-label") || "",
    component,
    source,
    rect: { x: Math.round(r.x), y: Math.round(r.y), w: Math.round(r.width), h: Math.round(r.height) },
  };
}
