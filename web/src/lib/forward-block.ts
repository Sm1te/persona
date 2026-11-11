// Writing a forward's block (#3899, iOS ForwardMessage.ref / body / route; docs/protocol/protocol.md "Forwarded messages",
// spec/fixtures/forward): what of a message it carries, its link back and its metadata, and the line that says where it
// comes from and goes. Only Forward to (views/chat/ForwardDialog.tsx) needs it, so it is not part of the chat's first
// screen; lib/forward.ts has what the chat draws. No DOM here.
import { t } from "../i18n";
import { uuid } from "./format";
import { FORWARD_KIND, forwardHeading, type ForwardSource } from "./forward";
import type { ContextRef } from "./links";
import { parseUserText, type Context } from "./turns";

/** The most of the original a forward carries; a longer one is cut with a line saying so. */
export const FORWARD_LIMIT = 20_000;

/** `time` as written: ISO 8601 to the second with the sender's offset ("2026-11-11T10:24:00+08:00"; "Z" in UTC), as
 *  iOS's ISO8601DateFormatter writes it. "" for a time that does not read. */
export function isoWithOffset(iso: string, at = new Date(iso)): string {
  if (Number.isNaN(at.getTime())) return "";
  const pad = (n: number) => String(Math.abs(Math.trunc(n))).padStart(2, "0");
  const off = -at.getTimezoneOffset();
  const zone = off === 0 ? "Z" : `${off > 0 ? "+" : "-"}${pad(off / 60)}:${pad(off % 60)}`;
  return `${at.getFullYear()}-${pad(at.getMonth() + 1)}-${pad(at.getDate())}T${pad(at.getHours())}:${pad(at.getMinutes())}:${pad(at.getSeconds())}${zone}`;
}

// What URLComponents leaves as it is in a path segment / a query value (iOS writes the link): the rest is %-escaped,
// `#` included, so a Claude item id ("<uuid>#12") stays in the query.
const PATH_SAFE = /%(21|24|26|27|28|29|2A|2B|2C|3A|3B|3D|40)/gi;
const QUERY_SAFE = /%(21|24|26|27|28|29|2A|2B|2C|2F|3A|3B|3D|3F|40)/gi;
const esc = (s: string, safe: RegExp) => encodeURIComponent(s).replace(safe, (m) => decodeURIComponent(m));

/** The original message's address: `persona://session/<machine>/<session>?at=<item>`. */
export function forwardURL(mid: string, sid: string, item: string): string {
  return `persona://session/${esc(mid, PATH_SAFE)}/${esc(sid, PATH_SAFE)}${item ? `?at=${esc(item, QUERY_SAFE)}` : ""}`;
}

/** The line under a forward over the box: which computer it comes from and where it goes. `to`: the target's Agent
 *  ("Codex") and computer, or none for Persona. */
export function forwardRoute(from: string | undefined, to?: { agent: string; machine?: string }): string {
  if (!to) return from ? t("chat-forwardto.route_from_to_persona", { from }) : t("chat-forwardto.route_to_persona");
  if (!to.machine) return t("chat-forwardto.route_to_agent", { agent: to.agent });
  if (from && from !== to.machine) return t("chat-forwardto.route_from_to_agent_on", { from, agent: to.agent, machine: to.machine });
  return t("chat-forwardto.route_to_agent_on", { agent: to.agent, machine: to.machine });
}

const QUOTE_KINDS = new Set(["quote", "context", "edit", FORWARD_KIND]);

/** What a quote or forward block a message opened with says about itself, as its `> ` lines are led by. */
function quoteHeading(c: Context): string {
  if (c.kind === FORWARD_KIND) return forwardHeading(c.attrs);
  return t("chat-forwardto.quoting", { label: c.title || c.url || t("chat-forward.messages") });
}

/** What of `raw` (a message as the transcript has it) a forward carries: the words as the chat shows them, without the
 *  hub's metadata and transport blocks; the quotes and forwards it opened with as `> ` lines led by their heading; files
 *  on its computer by name; cut at FORWARD_LIMIT. */
export function forwardBody(raw: string, machine?: string): string {
  const p = parseUserText(raw);
  const parts: string[] = [];
  for (const c of p.contexts) {
    if (!QUOTE_KINDS.has(c.kind)) continue;
    parts.push([quoteHeading(c), ...c.text.split("\n")].map((l) => `> ${l}`).join("\n"));
  }
  if (p.text) parts.push(p.text);
  for (const f of p.files) parts.push(machine ? t("chat-forwardto.file_on", { machine, name: f.name }) : t("chat-forwardto.file", { name: f.name }));
  const text = parts.join("\n\n");
  const chars = [...text];
  if (chars.length <= FORWARD_LIMIT) return text;
  return `${chars.slice(0, FORWARD_LIMIT).join("")}\n\n${t("chat-forwardto.cut")}`;
}

/** The forward block for a message whose text, as the transcript has it, is `raw`. */
export function forwardRef(raw: string, s: ForwardSource): ContextRef {
  const attrs: Record<string, string> = { msg: s.item, role: s.role, author: s.role === "user" ? t("chat-forward.me") : s.author, chat: s.chat };
  if (s.project) attrs.project = s.project;
  if (s.machine) attrs.machine = s.machine;
  const time = s.time ? isoWithOffset(s.time) : "";
  if (time) attrs.time = time;
  return { id: uuid(), kind: FORWARD_KIND, title: forwardHeading(attrs), url: forwardURL(s.mid, s.sid, s.item), text: forwardBody(raw, s.machine), attrs };
}
