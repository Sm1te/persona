// Writing `<persona-context>` tags (docs/protocol/protocol.md "Tags in messages"), as the phone writes them: lib/links.ts
// re-exports these with the contexts waiting in a composer. Kept apart from it, as it needs no store or router.
import { t } from "../i18n";
import { contextBody } from "./context-body";
import type { ContextRef } from "./links";

function escAttr(s: string) {
  return s.replace(/&/g, "&amp;").replace(/"/g, "&quot;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

/** A forward's attributes after kind / title / url, in the order every client writes them (spec/fixtures/forward): `msg`
 *  and `part`, the metadata, then any others by name. Other kinds keep the order they were given. */
const LEAD_ATTRS = ["msg", "part"];
const FORWARD_ATTRS = ["role", "author", "chat", "project", "machine", "time"];

function attrOrder(kind: string, names: string[]): string[] {
  if (kind !== "forward") return names;
  const known = [...LEAD_ATTRS, ...FORWARD_ATTRS];
  return [...known.filter((n) => names.includes(n)), ...names.filter((n) => !known.includes(n)).sort()];
}

/** `<persona-context kind="…" title="…" url="…">\ntext\n</persona-context>`, as the phone writes it. */
export function contextXML(r: Pick<ContextRef, "kind" | "title" | "url" | "text" | "attrs">): string {
  const body = contextBody(r.text);
  const attrs = r.attrs ?? {};
  const names = Object.keys(attrs).filter((k) => /^[a-z_]+$/.test(k) && k !== "kind" && k !== "title" && k !== "url");
  const extra = attrOrder(r.kind, names)
    .map((k) => ` ${k}="${escAttr(attrs[k])}"`)
    .join("");
  return `<persona-context kind="${escAttr(r.kind)}" title="${escAttr(r.title)}" url="${escAttr(r.url)}"${extra}>\n${body}\n</persona-context>`;
}

/** A message with its picked contexts in front (ChatView.swift sendText). With nothing typed, a forward says to look at it. */
export function withContexts(refs: Pick<ContextRef, "kind" | "title" | "url" | "text" | "attrs">[], text: string, hasImages = false): string {
  const typed = text.trim();
  if (!refs.length) return typed;
  const body = typed || (hasImages ? t("lib-links.see_images") : refs.some((r) => r.kind === "forward") ? t("lib-links.see_forward") : t("lib-links.see_context"));
  return `${refs.map(contextXML).join("\n")}\n\n${body}`;
}

