// The assistant's html component on the web (docs/assistant/assistant-views.md "Free-form HTML" and "Security boundaries every client must verify").
//
// A plain `iframe sandbox="allow-scripts"` with a CSP is not a network-denying sandbox (the frame's own navigation,
// meta refresh and the like are not all stopped by default-src), and a ready timeout cannot stop an endless loop on
// the page's thread. So the web runs a card's code in QuickJS inside a worker it can terminate, and draws what the
// code made, checked against an allowlist, into a frame of an opaque origin that runs only its own script
// (lib/html-runtime/, #4215).
//
// What is here is what a card needs to decide (whether to run, whether a view has html), and so is on the first screen.
// The bridge's rules (which messages count, how often to ask, the request) are lib/html-bridge.ts, loaded with the card.

/**
 * Whether the web's html runtime passed the security cases: web/e2e/html-runtime-security.mjs in Chromium and WebKit
 * (#4215, the runs attached to the change that turned this on). A card still runs only where the hub allows it
 * (assistant_html_bridge: persona_view and html_views on) and has a key.
 */
export const WEB_HTML_RUNTIME_VERIFIED = true;

/** The hub's hello feature for the bridge: …/html-actions is there, with persona_view and html_views both on. */
export const HTML_BRIDGE_FEATURE = "assistant_html_bridge";

export type HtmlRunDecision = { run: true } | { run: false; reason: "runtime_unverified" | "disabled" | "no_key" };

/** Whether an html component would run: the hub allows it (hello feature), the component has a stable key, and this
 *  client's runtime passed. The last is false on the web, so this always says fallback here. */
export function htmlDecision(o: { hubAllows: boolean; componentKey?: string; verified?: boolean }): HtmlRunDecision {
  if (!(o.verified ?? WEB_HTML_RUNTIME_VERIFIED)) return { run: false, reason: "runtime_unverified" };
  if (!o.hubAllows) return { run: false, reason: "disabled" };
  if (!o.componentKey) return { run: false, reason: "no_key" };
  return { run: true };
}

/** Whether a view (its root or any named page) has an html component. */
export function hasHtml(raw: unknown): boolean {
  const pages = (v: unknown): unknown[] => {
    if (!v || typeof v !== "object") return [];
    const p = (v as { pages?: unknown }).pages;
    return [v, ...(p && typeof p === "object" ? Object.values(p) : [])];
  };
  return pages(raw).some((page) => {
    const sections = (page as { sections?: unknown }).sections;
    return Array.isArray(sections) && sections.some((s) => Array.isArray(s?.items) && s.items.some((it: unknown) => (it as { type?: unknown })?.type === "html"));
  });
}
