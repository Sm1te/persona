// Dev only (html-runtime-lab.html, not built): one assistant html card at a time, run in the web runtime (#4215) as it
// will be once the runtime passed its security cases, in a made-up conversation against a fake hub (fetch). For
// web/e2e/html-runtime-security.mjs: window.lab.card(html) draws a new card (a new instance: the last one is torn
// down), window.lab.posts counts the bridge's calls that reached the hub, window.lab.pwned what anything posted to this
// page that isn't the card frame's own (an attack that ran outside the card's runtime would show here).
import { StrictMode } from "react";
import { flushSync } from "react-dom";
import { createRoot } from "react-dom/client";
import { initI18n } from "../i18n";
import FRAME_SCRIPT from "../lib/html-runtime/frame.js?raw";
import { FRAME_SANDBOX, frameNonce, frameShell } from "../lib/html-runtime/sanitize";
import "../styles.css";

await initI18n();
const [{ CardConversation, CardItem, HtmlRuntime }, cards, { getState }, { Markdown }, { Toasts }] = await Promise.all([
  import("../ui/AssistantCard"),
  import("../lib/assistant-views"),
  import("../lib/store"),
  import("../ui/Markdown"),
  import("../ui/atoms"),
]);

const lab = {
  posts: [] as { kind: string; text?: string; url?: string }[],
  pwned: [] as string[],
  card: (_html: string, _opts?: { height?: number; title?: string }) => {},
  rawFrame: (_head: string, _body: string) => {},
};
(window as unknown as { lab: typeof lab }).lab = lab;
window.addEventListener("message", (e) => {
  const d = e.data as unknown;
  const s = typeof d === "string" ? d : JSON.stringify(d ?? null);
  if (s.includes("pwned")) lab.pwned.push(s.slice(0, 200));
});

const realFetch = window.fetch.bind(window);
window.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
  const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
  if (!url.includes("/api/")) return realFetch(input, init);
  const h = /\/conversations\/([^/]+)\/views\/([^/]+)\/html-actions$/.exec(url);
  if (h) {
    const body = JSON.parse(String(init?.body ?? "{}")) as { kind: string; component_key: string; text?: string; url?: string; client_msg_id: string; expected_version: number };
    lab.posts.push({ kind: body.kind, ...(body.text ? { text: body.text } : {}), ...(body.url ? { url: body.url } : {}) });
    if (body.kind === "open") return new Response(JSON.stringify({ open: body.url, version: body.expected_version }), { status: 200, headers: { "Content-Type": "application/json" } });
    const action = { key: decodeURIComponent(h[2]), action: body.component_key, kind: "html", reply: body.text, values: {}, client_msg_id: body.client_msg_id, status: "pending", revision: 1, at: new Date().toISOString() };
    return new Response(JSON.stringify({ action }), { status: 200, headers: { "Content-Type": "application/json" } });
  }
  return new Response("{}", { status: 404 });
};

const C = "lab-conversation";
cards.resetCards("lab-hub");
Object.assign(getState(), { features: ["assistant_views", "assistant_html_bridge"] });
const width = Number(new URLSearchParams(location.search).get("width")) || 720;
const root = createRoot(document.getElementById("root")!);
let n = 0;

lab.card = (html, opts = {}) => {
  const id = `lab-${++n}`;
  const view = { title: opts.title ?? "Lab", fallback: "The card's words, shown where its page doesn't run.", sections: [{ items: [{ type: "html", key: "page", html, ...(opts.height ? { height: opts.height } : {}) }] }] };
  const text = "```persona-view\n" + JSON.stringify(view) + "\n```\n";
  cards.applyBlock(C, `${id}/0`);
  // at once: when this returns, the last card is gone and this one is on the page (the e2e reads its state next)
  flushSync(() => root.render(
    <StrictMode>
      <main className="min-h-dvh bg-page p-4 text-ink" style={{ width }}>
        <HtmlRuntime.Provider value>
          <CardConversation.Provider value={C}>
            <CardItem.Provider key={id} value={{ id, text }}>
              <Markdown text={text} />
            </CardItem.Provider>
          </CardConversation.Provider>
        </HtmlRuntime.Provider>
        <Toasts />
      </main>
    </StrictMode>,
  ));
};
// The display frame alone, the sanitizer bypassed: its sandbox, its CSP and its own script are all that stand between
// what is written into it and the network. The real path never writes the card's HTML into the frame (sanitize.ts); this
// is how the e2e checks that layer by itself.
lab.rawFrame = (head, body) => {
  document.getElementById("raw-frame")?.remove();
  const f = document.createElement("iframe");
  f.id = "raw-frame";
  f.setAttribute("sandbox", FRAME_SANDBOX);
  f.style.cssText = "width:400px;height:200px;border:1px solid gray";
  const shell = frameShell(false, frameNonce(), FRAME_SCRIPT);
  const cspEnd = shell.indexOf(">", shell.indexOf('http-equiv="Content-Security-Policy"')) + 1;
  const withHead = shell.slice(0, cspEnd) + head + shell.slice(cspEnd);
  f.srcdoc = withHead.replace("<body>", "<body>" + body);
  document.body.append(f);
};
document.documentElement.dataset.labReady = "1";
