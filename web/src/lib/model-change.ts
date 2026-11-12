// Where the chat marks that the replies' model changed (#5034, cindy's model-only switch divider): between two visible
// replies whose models are both known and differ. Only what the transcript says wrote a reply counts (Item.model), never
// the model picker; a reply whose model is unknown is skipped, and nothing goes inside an exchange.

import { modelShort } from "./context-usage";
import { modelName } from "./launcher-models";
import type { ChatLine, Segment } from "./turns";
import type { Item } from "./types";

/** The model the replies were written by before an exchange (`from`) and in it (`to`), as the transcript names them. */
export interface ModelChange {
  from: string;
  to: string;
}

/** A model id without what only says where it runs or which snapshot it is, its case kept: a gateway's vendor path
 *  ("vendor/glm-5.1"), Bedrock's region and vendor prefix and version suffix ("us.anthropic.…-v1:0"), Vertex's
 *  "@<date>", a trailing "-YYYYMMDD". A context-size suffix ("[1m]") stays: modelShort names it as another model. */
export function bareModel(id: string): string {
  let s = id.trim();
  s = s.slice(s.lastIndexOf("/") + 1);
  s = s.replace(/^(?:[a-z]+\.)?anthropic\./i, "");
  s = s.replace(/@[^@]*$/, "");
  s = s.replace(/-v\d+:\d+(?=(?:\[[^\]]*\])?$)/i, ""); // only with the colon: "deepseek-v3" is a model
  s = s.replace(/-\d{8}(?=(?:\[[^\]]*\])?$)/, "");
  return s || id.trim();
}

/** A model id as the chat names it: Claude's "claude-sonnet-4-5-20250929" → "Sonnet 4.5" (modelShort), a raw GPT id
 *  made readable (modelName), any other id as it is, each without its wrappers (bareModel). */
export function replyModelName(id: string): string {
  return modelIdentity(id).name;
}

/** What the chat compares and shows of a model id: the same id written another way (an alias, a pinned snapshot,
 *  Bedrock's or Vertex's form, another case) has the same `canonical`. */
export interface ModelIdentity {
  canonical: string;
  name: string;
}

// Every push builds the chat's lines again and asks this of each exchange's model: a few ids, asked over and over.
const IDENTITIES = new Map<string, ModelIdentity>();
const MAX_IDENTITIES = 256;

export function modelIdentity(id: string): ModelIdentity {
  const known = IDENTITIES.get(id);
  if (known) return known;
  const bare = bareModel(id);
  const short = modelShort(bare);
  const name = short && short !== bare ? short[0].toUpperCase() + short.slice(1) : modelName({ id: bare });
  const identity = { canonical: bare.toLowerCase(), name };
  if (IDENTITIES.size >= MAX_IDENTITIES) IDENTITIES.delete(IDENTITIES.keys().next().value!);
  IDENTITIES.set(id, identity);
  return identity;
}

function later(a: Item, b: Item): boolean {
  if (a.seq !== b.seq) return a.seq > b.seq;
  return Number(a.id.split("#")[1] ?? 0) > Number(b.id.split("#")[1] ?? 0);
}

/** The model that wrote an exchange's reply: that of its latest assistant item that names one, its answer and every
 *  step alike (a tool call included), so work a fallback model did after the last words counts. */
export function replyModel(seg: Segment): string | undefined {
  let latest: Item | undefined;
  const see = (it: Item | undefined) => {
    if (it?.role === "assistant" && it.model && (!latest || later(it, latest))) latest = it;
  };
  see(seg.answer);
  for (const st of seg.steps) see(st.kind === "tool" ? st.use : st.item);
  return latest?.model;
}

/** `lines` with each visible exchange whose reply's model differs from the previous visible reply's marked
 *  (Segment.modelChange). The same model written another way is no change (modelIdentity), nor are two ids the chat
 *  names alike. A folded round (supervision, background) is no visible reply; an exchange whose model is unknown
 *  neither gets a mark nor ends the run. */
export function markModelChanges(lines: ChatLine[]): ChatLine[] {
  let previous: { id: string; identity: ModelIdentity } | undefined;
  return lines.map((line) => {
    if (line.kind !== "segment") return line;
    const model = replyModel(line.seg);
    if (!model) return line;
    const from = previous;
    const identity = modelIdentity(model);
    previous = { id: model, identity };
    if (!from || from.identity.canonical === identity.canonical || from.identity.name === identity.name) return line;
    return { ...line, seg: { ...line.seg, modelChange: { from: from.id, to: model } } };
  });
}
