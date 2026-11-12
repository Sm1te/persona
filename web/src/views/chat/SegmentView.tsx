import { type CSSProperties, Fragment, memo, type ReactNode, useContext, useLayoutEffect, useMemo, useRef, useState } from "react";
import { clock } from "../../lib/format";
import { stripCloseout } from "../../lib/closeout";
import { getSession, getState } from "../../lib/store";
import { withChildSessions } from "../../lib/child-cards";
import { agentStatus, isNestedSubagentStep, nestedSubagentStatus, nestingKey, subagentFor, withNestedSubagents } from "../../lib/tasks";
import { turnImages } from "../../lib/turn-images";
import { TurnImages } from "./TurnImages";
import { exchangeSteps, segmentIdentity, isLoopTick, isOwnMessage, type Context, type Segment, type UserMessage } from "../../lib/turns";
import type { Item } from "../../lib/types";
import { planCardStep, workBlocks, type ToolStep, type WorkNode } from "../../lib/work-groups";
import { NO_WORKFLOW_CALLS } from "../../lib/workflows";
import { channelOf, useChannels } from "../../lib/channels";
import { splitQuotes } from "../../lib/drafts";
import { EASE } from "../../ui/atoms";
import { ChannelMark, OriginMark } from "../../ui/ChannelMark";
import { externalFrom, type ExternalOrigin } from "../../lib/conversation-origins";
import { Markdown } from "../../ui/Markdown";
import { CardConversation, CardItem } from "../../ui/AssistantCard";
import { cardReply } from "../../lib/assistant-blocks";
import { InjectedHint, LoopTick, MergedBubble, PeerMessage } from "./Messages";
import { ModelChangeDivider } from "./ModelChangeDivider";
import { SourceMessage } from "./SourceMessage";
import { ContextGroup } from "./ContextGroup";
import { ForwardBlock } from "./ForwardCard";
import { AttachedPhotos } from "./AttachedPhotos";
import { useFullText } from "./FullText";
import { FileCards, ReplyFiles } from "./FileCards";
import { ReplyPorts } from "./PortChips";
import { MessageActions } from "./MessageActions";
import { QuoteStack } from "./Quote";
import { PathText } from "../../ui/PathLinks";
import { Thumb } from "./Steps";
import { useFoldScroll } from "./fold-scroll";
import { useChatContext } from "./chat-context";
import { useFreshWords } from "./fresh-words";
import { Icon } from "../../ui/icons";
import { WorkGroup, WorkNodes, type WorkProps } from "./WorkGroup";
import { useCallSeeker } from "./jump";
import { t } from "../../i18n";

/** A message longer than this many lines folds to ten of them (FOLDED), with "Expand" under it (the reference client's UserMessage). */
export const BUBBLE_MAX_LINES = 14;
/** Before it is measured: about this many characters to a line of the 488 px bubble. */
const CHARS_PER_LINE = 30;
/** A folded message: ten lines high, scrolling inside (the chat scrolls on at its top and end). */
const FOLDED = "max-h-[10lh] overflow-y-auto";
/** Characters to a line of a bubble narrower than any drawn (a phone's holds about 17 wide ones): a message within
 *  BUBBLE_MAX_LINES even there is short in any bubble, and is not measured. */
const NARROWEST_CHARS = 10;

/** How many lines a message takes in its bubble, by its text (the bubble measures itself once drawn). */
export function bubbleLines(text: string, perLine = CHARS_PER_LINE): number {
  return text.split("\n").reduce((n, l) => n + Math.max(1, Math.ceil([...l].length / perLine)), 0);
}

/** ModelChangeDivider's height as drawn (Chrome, 2026-11-12): its 8 px above and below, the chip's 4 px padding, 1 px
 *  border and 16.5 px line (11 px text, the inherited 1.5). The exchange's 14 px gap comes on top. */
export const MODEL_DIVIDER_HEIGHT = 42.5;

/** A finished exchange's height in px before it is first drawn (content-visibility, docs/web/web.md "Long chats"), by
 *  its text the way bubbleLines counts it: its bubble (folded past BUBBLE_MAX_LINES), its words at 2.5 times the
 *  bubble's characters to a line (the column is wider, and Markdown's short and blank lines take less than a line), a
 *  row for each other block, and the model change divider when it has one (`modelChange`). Once drawn it keeps its
 *  own height. */
export function segmentHeight(user: UserMessage | undefined, nodes: WorkNode[], modelChange = false): number {
  // gap: the exchange's blocks stand 14 px apart (`gap-3.5`, cindy's msg-stream-items)
  const line = 24, gap = 14, row = 28;
  const lines = user ? bubbleLines(user.text) : 0;
  let h = user ? (lines > BUBBLE_MAX_LINES ? 10 : lines) * line + 20 + (user.images.length ? 102 : 0) : 0;
  for (const n of nodes) h += n.kind === "step" && n.step.kind === "text" ? bubbleLines(n.step.item.text ?? "", CHARS_PER_LINE * 2.5) * line : row;
  if (modelChange) h += MODEL_DIVIDER_HEIGHT;
  return Math.round(h + gap * Math.max(0, nodes.length + (user ? 1 : 0) + (modelChange ? 1 : 0) - 1));
}

/** `sendState`: a message not in the transcript yet (a sending bubble, the outbox's), marked for the sync scenarios
 *  (spec/fixtures/sync); the transcript's own bubbles have none. `starting`: the first message of a conversation still
 *  starting (the new-conversation page, #1697): drawn as it will be once sent, without actions (there is nothing yet to
 *  act on) and without "Sending…" (the page says it is preparing). */
/** `onDelete`: a message only on this device (a failed send): its menu ends in Delete, and it has no Rewind (it is not in
 *  the transcript to go back to). */
export function UserBubble({ msg, mid, sid, pending, sendState, starting, onDelete }: { msg: UserMessage; mid: string; sid: string; pending?: boolean; sendState?: "sending" | "sent" | "failed"; starting?: boolean; onDelete?: () => void }) {
  const own = isOwnMessage(msg);
  // the message an extension opened the conversation with (#3758)
  const { opening } = useChatContext();
  const openedBy = opening && opening.id === msg.id ? opening.origin : undefined;
  // forwards (#3899) are cards at the top of the bubble; the other contexts fold in the row above it
  const forwards = msg.contexts.filter((c) => c.kind === "forward");
  const contexts = forwards.length ? msg.contexts.filter((c) => c.kind !== "forward") : msg.contexts;
  return (
    <div className="ml-auto flex max-w-[488px] min-w-0 flex-col items-end gap-1.5 pl-[12%]" style={pending ? { animation: `fade-up 300ms ${EASE} both` } : undefined} data-user-bubble data-send-state={sendState} data-items={pending || starting ? undefined : [msg.id, ...msg.images.map((i) => i.id)].join(" ")}>
      {msg.images.length > 0 && (
        <div className="flex flex-wrap justify-end gap-1.5">
          {msg.images.map((im) => (
            <Thumb key={im.id} item={im} mid={mid} sid={sid} size={96} />
          ))}
        </div>
      )}
      {msg.photos && <AttachedPhotos paths={msg.photos} mid={mid} sid={sid} />}
      {contexts.length > 0 && <ContextGroup contexts={contexts} />}
      {msg.files && <FileCards files={msg.files} mid={mid} />}
      {(msg.text || forwards.length > 0) &&
        (starting ? (
          <BubbleText text={msg.text} forwards={forwards} mid={mid} sid={sid} own={own} />
        ) : pending ? (
          <BubbleText text={msg.text} forwards={forwards} mid={mid} sid={sid} own={own} pending />
        ) : (
          <MessageActions text={msg.text} isUser ts={msg.ts} side messageId={onDelete ? undefined : msg.id} onDelete={onDelete}
            images={msg.images} photos={msg.photos} className="max-w-full">
            <BubbleText text={msg.text} forwards={forwards} ts={msg.ts} mid={mid} sid={sid} own={own} />
          </MessageActions>
        ))}
      {(pending || msg.queued) && <span className="text-[11px] text-ink-3">{pending ? t("chat-segmentview.sending") : t("chat-segmentview.queued")}</span>}
      {!pending && !starting && <ChannelSource clientMsgId={msg.client_msg_id} openedBy={openedBy} />}
    </div>
  );
}

/** The bubble's words, folded to ten lines past fourteen; the fold scrolls (the wheel, a trackpad, a finger), with a
 *  fade at its bottom while there is more (fold-scroll.ts). `pending` (still being sent): dimmed, the same fold. A
 *  leading `> ` block ("Quote") is a folded quote card at its top instead of `>` lines (Quote.tsx). */
function BubbleText({ text: full, forwards, ts, mid, sid, own, pending }: { text: string; forwards?: Context[]; ts?: string; mid: string; sid: string; own: boolean; pending?: boolean }) {
  const fill = own ? "bg-user-bubble" : "bg-neutral-bubble";
  // a reply sent from a card (in an assistant conversation): its words, without the hub's tag, marked "From a card"
  const fromCard = useContext(CardConversation) ? cardReply(full) : undefined;
  const whole = fromCard ? fromCard.text : full;
  const split = useMemo(() => splitQuotes(whole), [whole]);
  const text = split ? split.rest : whole;
  const ref = useRef<HTMLDivElement>(null);
  const [long, setLong] = useState(() => bubbleLines(text) > BUBBLE_MAX_LINES);
  const [open, setOpen] = useState(false);
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el || open) return;
    // short however narrow: no layout read (opening a long chat read every bubble's, each a forced layout)
    if (bubbleLines(text, NARROWEST_CHARS) <= BUBBLE_MAX_LINES) {
      setLong(false);
      return;
    }
    // its full height in lines (a fold's scrollHeight is all of it)
    const line = parseFloat(getComputedStyle(el).lineHeight) || 24;
    setLong(el.scrollHeight / line > BUBBLE_MAX_LINES + 0.5);
  }, [text, open]);
  const folded = long && !open;
  const fold = useFoldScroll(ref, folded, [text]);
  const words = text && (
    <div
      ref={ref}
      {...fold}
      className={`text-[length:var(--chat-body)] leading-[1.6] whitespace-pre-wrap [overflow-wrap:anywhere] ${folded ? FOLDED : ""}`}
      data-folded={folded || undefined}
      data-bubble-text
    >
      <PathText text={text} mid={mid} cwd={getSession(mid, sid)?.cwd} />
    </div>
  );
  return (
    <div className="flex max-w-full flex-col items-end gap-1" data-from-card={fromCard ? fromCard.view : undefined}>
      {fromCard && (
        <span className="flex items-center gap-1 text-[11px] font-medium whitespace-nowrap text-accent">
          <Icon name="check" size={11} />
          {t("ui-assistantcard.from_card")}
        </span>
      )}
      {split || forwards?.length ? (
        <div className={`flex max-w-full flex-col gap-1.5 rounded-[12px] ${fill} px-4 py-2.5 text-ink ${pending ? "opacity-60" : ""}`} title={clock(ts)}>
          {forwards?.map((c, i) => <ForwardBlock key={i} c={c} />)}
          {split && <QuoteStack blocks={split.quotes} mid={mid} sid={sid} />}
          {words}
        </div>
      ) : (
        <div className={`max-w-full rounded-[12px] ${fill} px-4 py-2.5 text-ink ${pending ? "opacity-60" : ""}`} title={clock(ts)}>
          {words}
        </div>
      )}
      {long && (
        <button type="button" onClick={() => setOpen((o) => !o)} aria-expanded={open} className="rounded-[6px] px-1.5 py-0.5 text-[12px] font-medium whitespace-nowrap text-ink-3 transition-colors hover:bg-hover-2 hover:text-ink-2">
          {open ? t("chat-segmentview.collapse") : t("chat-segmentview.expand")}
        </button>
      )}
    </div>
  );
}

/** "From WeChat" under a message that came from a channel (its client_msg_id is `<channel>-…`, docs/extensions/channels.md);
 *  else, under the message an extension opened the conversation with, "From Lark /agent" / "From an extension" (#3758). */
function ChannelSource({ clientMsgId, openedBy }: { clientMsgId?: string; openedBy?: ExternalOrigin }) {
  const via = channelOf(clientMsgId, useChannels());
  if (!via && openedBy) {
    return (
      <span data-opened-by={openedBy} className="flex items-center gap-1.5 whitespace-nowrap text-[11px] text-ink-3">
        <OriginMark origin={openedBy} size={14} decorative />
        {externalFrom(openedBy)}
      </span>
    );
  }
  if (!via) return null;
  return (
    <span className="flex items-center gap-1.5 whitespace-nowrap text-[11px] text-ink-3">
      <ChannelMark c={via} size={14} />
      {t("chat-segmentview.from", { name: via.name })}
    </span>
  );
}

/** An answer's prose at the chat's body size, 1.6 lines. Important: styles.css's `.prose-j` (14 / 1.7) is outside
 *  Tailwind's layers, so it would win over a plain utility. */
const ANSWER = "[&_.prose-j]:text-[length:var(--chat-body)]! [&_.prose-j]:leading-[1.6]!";

/** What the agent said, without a bubble, the chat's body size (15, 16 on a phone) / 1.6. `fade`: it just came in, its
 *  words fade in once. Still streaming, it has the same menu (copy what is there so far, quote it).
 *  Memo'd: the chat context changes with every streamed item, so each SegmentView re-renders; a finished answer's
 *  props stay the same and it is skipped (#3024). */
export const Answer = memo(function Answer({ item, streaming, mid, sid, fade, noCloseout }: { item?: Item; streaming?: string; mid?: string; sid?: string; fade?: boolean; /** The card above the transcript shows its closeout (#3612): drawn without the raw block. */ noCloseout?: boolean }) {
  // a long answer the hub cut for the page: its start, and "View full text" under it
  const full = useFullText(streaming === undefined ? item : undefined, mid, sid);
  const text = streaming ?? (noCloseout ? stripCloseout(full.text) : full.text);
  if (!text) return null;
  const cwd = mid && sid ? getSession(mid, sid)?.cwd : undefined;
  if (streaming !== undefined) {
    return (
      <MessageActions text={text}>
        <div className={ANSWER} data-answer="streaming">
          <Markdown text={text} mid={mid} cwd={cwd} streaming lineBreaks />
        </div>
      </MessageActions>
    );
  }
  return (
    <MessageActions text={text} ts={item?.ts} itemId={item?.id}>
      <div className={ANSWER} data-answer data-items={item?.id}>
        {/* the cards in it are this item's blocks (ui/AssistantCard.tsx): `<item id>/<n>` */}
        <CardItem.Provider value={item?.id ? { id: item.id, text } : null}>
          <Markdown text={text} mid={mid} cwd={cwd} fade={fade || undefined} lineBreaks />
        </CardItem.Provider>
      </div>
      {full.more}
      {mid && sid && <ReplyFiles text={text} mid={mid} cwd={cwd} />}
      {mid && sid && <ReplyPorts text={text} mid={mid} sid={sid} cwd={cwd} />}
    </MessageActions>
  );
});

/** Freshness reaches only the single visible persisted text, not every paragraph hidden in its execution summary: it
 *  reads its own id (fresh-words.ts), so a new reply renders these words, not every exchange. */
const LatestAnswer = memo(function LatestAnswer({ item, streaming, mid, sid, noCloseout }: { item?: Item; streaming?: string; mid: string; sid: string; noCloseout?: boolean }) {
  const persisted = streaming === undefined ? item?.id : undefined;
  const fresh = useFreshWords(mid, sid, persisted);
  return <Answer item={item} streaming={streaming} mid={mid} sid={sid} fade={persisted ? fresh : undefined} noCloseout={noCloseout} />;
});

export const SegmentView = memo(function SegmentView({
  seg,
  live,
  mid,
  sid,
  streaming,
  batchedWords,
  expandFold,
  beforeWords,
  trace,
  offscreen,
}: {
  seg: Segment;
  live: boolean;
  mid: string;
  sid: string;
  streaming?: string;
  /** The live words are an agent's as Claude shows them (docs/integrations/claude-integration.md "Live replies"): a batch
   *  of lines at a time, and still while a tool runs. The live work row stays under them; the headless assistant's
   *  words stream as they are written and stand for it. */
  batchedWords?: boolean;
  expandFold: (f: Item) => Promise<void>;
  /** Drawn between the work and the agent's last words (the wakes chip, #3783); above the work while it is live. */
  beforeWords?: ReactNode;
  /** An opened supervision round shows its original input instead of the injected hint. */
  trace?: boolean;
  /** A finished exchange before the last two: off screen it is not drawn (the `seg-cv` class), and the chat watches its
   *  height through this (ChatView `watchSegment`, returning the unwatch) to keep the view still when it changes. */
  offscreen?: (section: HTMLElement) => () => void;
}) {
  const { subagents, tasks, scope, workflowCalls = NO_WORKFLOW_CALLS, lineStyle, closeoutSegment, children } = useChatContext();
  const own = useMemo(() => exchangeSteps(seg), [seg]);
  // the subagents its subagents launched, each a card of its own by when it started (#5030); a trace shows the calls only.
  // Keyed by what places them, not the list: that changes with every subagent's tokens and steps.
  const nesting = trace ? "" : nestingKey(subagents);
  // the child sessions that started during this exchange, each a card among its steps (#5120, lib/child-cards.ts)
  const placed = trace ? undefined : children?.get(seg.id);
  const steps = useMemo(
    () => withChildSessions(nesting ? withNestedSubagents(own, subagents) : own, placed),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [own, nesting, placed],
  );
  const turnStart = seg.user?.ts ?? seg.start;
  // one plan card per exchange, where its latest list was written (cindy); the other updates are rows
  const plan = useMemo(() => planCardStep(steps), [steps]);
  const p = useMemo<WorkProps>(() => {
    const agent = (s: ToolStep) =>
      isNestedSubagentStep(s) ? nestedSubagentStatus(subagentFor(subagents, s.use)) : agentStatus(s.result, subagentFor(subagents, s.use), live, s.use.tool_use_id ? tasks.notes.get(s.use.tool_use_id) : undefined);
    return { mid, sid, scope, expandFold, agentStatus: agent, segment: seg.id, workflowCalls, trace, lineStyle, turnLive: live, plan };
  }, [mid, sid, scope, expandFold, subagents, tasks, live, seg.id, workflowCalls, trace, lineStyle, plan]);
  const identity = segmentIdentity(seg);
  const nodes = useMemo(
    () => workBlocks(steps, { live, turnStart, streaming: streaming !== undefined, identity }),
    [steps, live, turnStart, streaming !== undefined, identity],
  );
  useCallSeeker(scope, nodes);
  // the pictures the agent looked at this turn, between its work and its last words
  const images = useMemo(() => turnImages(seg, getState().machines.find((m) => m.id === mid)?.name), [seg, mid]);
  // The text slot is fixed during the run and after completion. Streamed text replaces persisted narration.
  let cut = nodes.length;
  const tail = nodes[cut - 1];
  if (tail?.kind === "step" && tail.step.kind === "text") cut--;
  const latest = nodes.at(-1);
  const latestItem = latest?.kind === "step" && latest.step.kind === "text" ? latest.step.item : undefined;
  // The card above the transcript shows this exchange's closeout (#3612): its last words are drawn without the raw
  // block, and not at all when the block was all they said.
  const noCloseout = closeoutSegment === seg.id && streaming === undefined;
  const onlyCloseout = noCloseout && Boolean(latestItem?.text?.trim()) && !latestItem?.full_size && stripCloseout(latestItem?.text ?? "") === "";
  const working = live && (streaming === undefined || batchedWords) && !nodes.some((n) => n.kind === "group" && n.live);
  const liveRow = working && <WorkGroup g={{ kind: "group", id: `work-live-${seg.id}`, children: [], live: true, startedAt: Date.parse(turnStart ?? "") || undefined }} p={p} />;
  const imageStrip = images.length > 0 && <TurnImages images={images} mid={mid} sid={sid} cwd={getSession(mid, sid)?.cwd} />;
  const box = useRef<HTMLElement>(null);
  useLayoutEffect(() => {
    const el = box.current;
    return offscreen && el ? offscreen(el) : undefined;
  }, [offscreen]);
  const divided = Boolean(seg.modelChange);
  const guess = useMemo(() => (offscreen ? ({ "--seg-est": `${segmentHeight(seg.user, nodes, divided)}px` } as CSSProperties) : undefined), [offscreen, seg.user, nodes, divided]);
  return (
    <section ref={box} className={offscreen ? "seg-cv flex flex-col gap-3.5" : "flex flex-col gap-3.5"} style={guess} data-segment>
      {trace && seg.user?.rawInput && !seg.user.promptHidden && !seg.user.sourceInTrace ? <RawInjectedInput items={seg.user.rawInput} mid={mid} sid={sid} /> : seg.user && (seg.user.source ? <SourceMessage msg={seg.user} mid={mid} sid={sid} /> : seg.user.injected ? <InjectedHint msg={seg.user} sid={sid} /> : seg.user.parts ? <MergedBubble msg={seg.user} mid={mid} sid={sid} /> : seg.user.from ? <PeerMessage msg={seg.user} /> : isLoopTick(seg.user) ? <LoopTick msg={seg.user} /> : <UserBubble msg={seg.user} mid={mid} sid={sid} />)}
      {/* the reply below came from another model than the one before it (cindy: after the message that started it) */}
      {seg.modelChange && <ModelChangeDivider from={seg.modelChange.from} to={seg.modelChange.to} />}
      {/* Work stays collapsed above the single latest-text slot. */}
      {live && <Fragment key="before-words">{beforeWords}</Fragment>}
      <WorkNodes nodes={nodes.slice(0, cut)} p={p} live={live} fade />
      {/* working with no run of steps going (its words just came, or nothing yet): the live group still shows, under an
          agent's live words while they are there (batchedWords) */}
      {streaming === undefined && liveRow}
      {imageStrip}
      {!live && <Fragment key="before-words">{beforeWords}</Fragment>}
      {streaming !== undefined && liveRow}
      {(latestItem || streaming !== undefined) && !onlyCloseout && <div key="latest-text" data-latest-text><LatestAnswer item={latestItem} streaming={streaming} mid={mid} sid={sid} noCloseout={noCloseout || undefined} /></div>}
    </section>
  );
});

function RawInjectedInput({ items, mid, sid }: { items: Item[]; mid: string; sid: string }) {
  return (
    <div className="flex min-w-0 flex-col gap-1" data-supervision-input>
      <span className="text-[length:var(--chat-secondary)] text-ink-3">{t("chat-lines.supervision_input")}</span>
      {items.map((item) => item.kind === "image" ? <Thumb key={item.id} item={item} mid={mid} sid={sid} /> : <RawInjectedText key={item.id} item={item} mid={mid} sid={sid} />)}
    </div>
  );
}

function RawInjectedText({ item, mid, sid }: { item: Item; mid: string; sid: string }) {
  const full = useFullText(item, mid, sid);
  return (
    <div data-items={item.id}>
      <pre className="rounded-card bg-inset p-3 text-[length:var(--chat-secondary)] whitespace-pre-wrap text-ink-2 [overflow-wrap:anywhere]">{full.text}</pre>
      {full.more}
    </div>
  );
}
