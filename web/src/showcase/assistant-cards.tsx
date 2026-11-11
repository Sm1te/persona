// Dev only (assistant-cards.html, not built): the assistant's cards (docs/assistant/assistant-views.md) in a made-up conversation,
// for screenshots: a decision answered from another device, a form to fill in, a live card at its second version, a
// card that changed under a submit, an html card (its fallback; ?html=run runs it, #4215), a card with a component the web does not draw
// (its fallback), and the message a card sent. The hub is faked
// (fetch): a submit answers the record it would keep. All data is made up.
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { initI18n } from "../i18n";
import "../styles.css";

await initI18n();
const [{ CardConversation, CardItem, HtmlRuntime, LiveCard, VIEWS_FEATURE }, cards, { getState }, { Markdown }, { UserBubble }, { Toasts }] = await Promise.all([
  import("../ui/AssistantCard"),
  import("../lib/assistant-views"),
  import("../lib/store"),
  import("../ui/Markdown"),
  import("../views/chat/SegmentView"),
  import("../ui/atoms"),
]);

const EXAMPLES = import.meta.glob("../../../schema/extension/examples/assistant/*.json", { eager: true, import: "default" }) as Record<string, Record<string, unknown>>;
const example = (name: string) => Object.entries(EXAMPLES).find(([p]) => p.endsWith(`/${name}.json`))![1];

// ?html=run: html cards run in the web runtime (#4215), as they will once it passed its security cases; the fake hub
// takes their bridge calls
const runHtml = new URLSearchParams(location.search).get("html") === "run";
const realFetch = window.fetch.bind(window);
window.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
  const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
  // the runtime's own WebAssembly, served by Vite
  if (!url.includes("/api/")) return realFetch(input, init);
  const h = /\/conversations\/([^/]+)\/views\/([^/]+)\/html-actions$/.exec(url);
  if (h) {
    const body = JSON.parse(String(init?.body ?? "{}")) as { kind: string; component_key: string; text?: string; url?: string; client_msg_id: string; expected_version: number };
    if (body.kind === "open") return new Response(JSON.stringify({ open: body.url, version: body.expected_version }), { status: 200, headers: { "Content-Type": "application/json" } });
    const action = { key: decodeURIComponent(h[2]), action: body.component_key, kind: "html", reply: body.text, values: {}, client_msg_id: body.client_msg_id, status: "pending", revision: 1, at: new Date().toISOString() };
    return new Response(JSON.stringify({ action }), { status: 200, headers: { "Content-Type": "application/json" } });
  }
  const m = /\/conversations\/([^/]+)\/views\/([^/]+)\/actions$/.exec(url);
  if (m) {
    const body = JSON.parse(String(init?.body ?? "{}")) as { action: string; values?: Record<string, string>; client_msg_id: string };
    const action = { key: decodeURIComponent(m[2]), action: body.action, values: body.values ?? {}, client_msg_id: body.client_msg_id, status: "pending", revision: 1, at: new Date().toISOString() };
    return new Response(JSON.stringify({ action }), { status: 200, headers: { "Content-Type": "application/json" } });
  }
  return new Response("{}", { status: 404 });
};

const C = "demo-conversation";
cards.resetCards("demo-hub");
Object.assign(getState(), { features: runHtml ? [VIEWS_FEATURE, "assistant_html_bridge"] : [VIEWS_FEATURE] });
const fence = (v: unknown) => "```persona-view\n" + JSON.stringify(v, null, 2) + "\n```\n";
const now = new Date().toISOString();
const live = (id: string, version: number, view: Record<string, unknown>) => ({ id, conversation: C, version, view: view as never, created_at: now, updated_at: now });

const replyA = "三个方案里 B 最划算。\n\n" + fence(example("decision")); // i18n-ignore: made-up sample data (dev-only page)
const replyB = "需要几项信息才能安排：\n\n" + fence(example("form")); // i18n-ignore: made-up sample data (dev-only page)
const replyC = "这是交互说明（网页端显示文字版）：\n\n" + fence(example("html")); // i18n-ignore: made-up sample data (dev-only page)
const future = { title: "排期", fallback: "排期：周一定方案，周三联调，周五上线。", sections: [{ items: [{ type: "timeline", events: [] }] }] }; // i18n-ignore: made-up sample data (dev-only page)
const replyD = "排期如下：\n\n" + fence(future); // i18n-ignore: made-up sample data (dev-only page)
cards.applyBlock(C, "msg-a/0");
cards.applyBlock(C, "msg-b/0");
cards.applyBlock(C, "msg-c/0");
cards.applyAction(C, { key: "msg-a/0", action: "choose-b", values: {}, client_msg_id: "other-device", status: "sent", revision: 2, at: now });
cards.updateDraft(C, "msg-b/0", (d) => ({ ...d, values: { task: "整理读书笔记" } })); // i18n-ignore: made-up sample data (dev-only page)
cards.applyView(C, live("view_progress", 2, example("progress-updated")));
cards.applyView(C, live("view_compare", 5, example("compare")));
cards.updateDraft(C, "view_compare", (d) => ({ ...d, changed: true }));

const width = Number(new URLSearchParams(location.search).get("width")) || 720;
const sent = { text: '选 B：队列去重\n<persona-action view="msg-a/0" action="choose-b">{"values":{}}</persona-action>', images: [], contexts: [], ts: now }; // i18n-ignore: made-up sample data (dev-only page)

function Answer({ id, text }: { id: string; text: string }) {
  return (
    <CardItem.Provider value={{ id, text }}>
      <Markdown text={text} />
    </CardItem.Provider>
  );
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <main className="min-h-dvh bg-page p-4 text-ink" style={{ width }}>
      <HtmlRuntime.Provider value={runHtml}>
      <CardConversation.Provider value={C}>
        <div className="mx-auto flex max-w-[720px] flex-col gap-6">
          <Answer id="msg-a" text={replyA} />
          <UserBubble msg={sent as never} mid="demo-machine" sid={C} />
          <Answer id="msg-b" text={replyB} />
          <LiveCard conversation={C} id="view_progress" />
          <LiveCard conversation={C} id="view_compare" />
          <Answer id="msg-c" text={replyC} />
          <Answer id="msg-d" text={replyD} />
        </div>
      </CardConversation.Provider>
      </HtmlRuntime.Provider>
      <Toasts />
    </main>
  </StrictMode>,
);
