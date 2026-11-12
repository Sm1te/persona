// What an opened child session card shows (#5120): what the child was asked, what it said last and its last tool call,
// read from the child's own transcript only when the card is opened. One page of its last exchange with the work folded
// (`turns=1&fold_work=1`): a child is seldom more than one exchange, and that page starts with what it was asked. A longer
// child's first lines are read for its first message; a last tool folded into the work, the fold's last few lines.
import { api, sessionPath } from "./api";
import { parseUserText } from "./turns";
import type { Item, ItemsPage } from "./types";

/** The lines read for a long child's first message: past the runtime's own opening lines, short of its work. */
export const HEAD_LINES = 40;
/** The last lines of folded work read for its last tool call: the call, its result and a thought or two around them. */
export const FOLD_TAIL_LINES = 8;

export interface ChildDetail {
  /** Its first message's words (the hub's context tags taken out); "" when none was found. */
  prompt: string;
  /** Its latest words, as an item (a long one cut by the hub has its "View full text"). */
  result?: Item;
  /** The tool its last call was to. */
  lastTool?: string;
}

/** The first message in `items` the child was sent. */
export function firstAsk(items: readonly Item[]): string {
  for (const it of items) {
    if (it.role !== "user" || it.kind !== "text" || it.tool_use_id) continue;
    const text = parseUserText(it.text ?? "").text.trim();
    if (text) return text;
  }
  return "";
}

/** The latest words of the child's own in `items`. */
export function latestWords(items: readonly Item[]): Item | undefined {
  return items.findLast((it) => it.role === "assistant" && it.kind === "text" && Boolean(it.text?.trim()));
}

/** The tool of the last call in `items`. */
export function lastToolName(items: readonly Item[]): string | undefined {
  return items.findLast((it) => it.kind === "tool_use" && it.tool_name)?.tool_name;
}

/** The folded work after the last call the page shows, when there is one: the last call is in it. */
export function foldAfterLastCall(items: readonly Item[]): Item | undefined {
  const fold = items.findLast((it) => it.kind === "fold" && it.fold);
  const use = items.findLast((it) => it.kind === "tool_use");
  return fold && (!use || fold.seq > use.seq) ? fold : undefined;
}

export async function readChildDetail(mid: string, sid: string, signal?: AbortSignal): Promise<ChildDetail> {
  const path = `${sessionPath(mid, sid)}/items`;
  const page = await api<ItemsPage>(`${path}?turns=1&fold_work=1&lazy_images=1`, { signal });
  const items = page.items ?? [];
  const quiet = <T>(read: Promise<T>) => read.catch((e: unknown) => {
    if (signal?.aborted) throw e;
    return undefined;
  });
  const fold = foldAfterLastCall(items);
  const to = fold?.fold?.through_seq;
  const [head, tail] = await Promise.all([
    // the page is the last of several exchanges: what it was first asked is further back
    page.has_more ? quiet(api<ItemsPage>(`${path}?from_seq=0&through_seq=${HEAD_LINES}&lazy_images=1`, { signal })) : undefined,
    fold && to !== undefined ? quiet(api<ItemsPage>(`${path}?from_seq=${Math.max(fold.seq, to - FOLD_TAIL_LINES)}&through_seq=${to}&lazy_images=1`, { signal })) : undefined,
  ]);
  return {
    prompt: (head && firstAsk(head.items ?? [])) || firstAsk(items),
    result: latestWords(items),
    lastTool: (tail && lastToolName(tail.items ?? [])) || lastToolName(items),
  };
}
