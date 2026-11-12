// An html component's bridge calls to the hub (#4215): what was confirmed goes to …/html-actions; a reply is the card's
// answer, an open comes back with what to open; the same 409s as a declared reply.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { conversationCards, resetCards } from "./assistant-views";
import type { HtmlActionRequest } from "./html-bridge";
import { htmlActionsPath, postHtmlAction } from "./html-actions";

function respond(status: number, body: unknown) {
  const f = vi.fn(async (..._: unknown[]) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } }));
  vi.stubGlobal("fetch", f);
  return f;
}
const req = (o: Partial<HtmlActionRequest> = {}): HtmlActionRequest => ({ kind: "reply", component_key: "page", expected_version: 2, text: "go on", client_msg_id: "m1", confirmed: true, ...o });
const action = { key: "k/0", action: "page", kind: "html", client_msg_id: "m1", status: "pending", revision: 1, at: "2026-11-12T00:00:00Z" };

beforeEach(() => {
  resetCards("hub-a");
  vi.stubGlobal("localStorage", { getItem: () => "t", setItem: () => {}, removeItem: () => {} });
});
afterEach(() => vi.unstubAllGlobals());

describe("an html component's bridge call", () => {
  it("posts what the user confirmed to its card's html-actions, the key escaped once", async () => {
    const f = respond(200, { action });
    expect(await postHtmlAction("c1", "k/0", req())).toEqual({ ok: true });
    const [url, init] = f.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toContain(htmlActionsPath("c1", "k/0"));
    expect(url).toContain("/views/k%2F0/html-actions");
    expect(JSON.parse(String(init.body))).toEqual({ kind: "reply", component_key: "page", expected_version: 2, text: "go on", client_msg_id: "m1", confirmed: true });
    // the reply is the card's answer now
    expect(conversationCards("c1").actions["k/0"]?.client_msg_id).toBe("m1");
  });

  it("says what an open should open", async () => {
    respond(200, { open: "https://example.com/next", version: 2 });
    expect(await postHtmlAction("c1", "k/0", req({ kind: "open", text: undefined, url: "https://example.com/next" }))).toEqual({ ok: true, open: "https://example.com/next" });
  });

  it("takes the other device's answer, and a newer card, as a declared reply does", async () => {
    respond(409, { error: "answered", code: "already_answered", action: { ...action, client_msg_id: "other" } });
    expect(await postHtmlAction("c1", "k/0", req())).toEqual({ ok: false, outcome: "answered_elsewhere" });
    expect(conversationCards("c1").actions["k/0"]?.client_msg_id).toBe("other");
    respond(409, { error: "changed", code: "view_changed" });
    expect(await postHtmlAction("c1", "k/1", req())).toEqual({ ok: false, outcome: "changed" });
    respond(500, { error: "down" });
    expect(await postHtmlAction("c1", "k/2", req())).toMatchObject({ ok: false, outcome: "failed" });
  });
});
