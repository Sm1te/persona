// A forwarded message as a card (#3899; iOS QuoteViews.swift QuoteBarRow / QuoteBlock with the forward arrow): over the
// composer, the message about to go with the line saying which computer it comes from and where it goes, × to drop it;
// at the top of a sent bubble, "Forwarded from Claude · chat", the computer and time and the first lines, a click opening
// the original chat at that message (or the whole text, when this page does not have that chat). Drawn as the quote's
// bar and card (Quote.tsx) with the forward arrow before the heading, so a forward reads as a quote of another chat.
import { useLayoutEffect, useRef, useState } from "react";
import { t } from "../../i18n";
import { forwardHeading, forwardMeta, forwardOrigin, forwardSummary, jumpWhenOpen } from "../../lib/forward";
import { removeContext, useContexts } from "../../lib/links";
import { navigate } from "../../lib/router";
import { getSession, getState } from "../../lib/store";
import type { Context } from "../../lib/turns";
import { Icon } from "../../ui/icons";
import { Markdown } from "../../ui/Markdown";
import { ForwardBar } from "./ForwardBar";

/** The forwards waiting above a composer's input (lib/links.ts addContext); other contexts are ContextChips'. */
export function ForwardBars({ draftKey, onRemove }: { draftKey: string; onRemove?: () => void }) {
  const forwards = useContexts(draftKey).filter((r) => r.kind === "forward");
  if (!forwards.length) return null;
  return (
    <div className="flex flex-col gap-1.5 px-0.5 pt-0.5" data-forwards>
      {forwards.map((r) => (
        <ForwardBar
          key={r.id}
          forward={r}
          lines={forwards.length === 1 ? 2 : 1}
          onRemove={() => {
            removeContext(draftKey, r.id);
            onRemove?.();
          }}
        />
      ))}
    </div>
  );
}

/** Opens the chat a forward came from, at the message, when this page has that chat; false otherwise. */
export function openForwarded(c: Pick<Context, "url" | "attrs">): boolean {
  const o = forwardOrigin(c.url ?? "", c.attrs?.msg);
  if (!o) return false;
  const st = getState();
  const main = st.assistant?.machine_id === o.machine && st.assistant?.main === o.session;
  const session = getSession(o.machine, o.session);
  if (!main && !session) return false;
  if (o.item) jumpWhenOpen(o.machine, o.session, o.item);
  if (main) navigate({ name: "persona" });
  else if (session?.assistant) navigate({ name: "persona", sid: o.session });
  else navigate({ name: "session", mid: o.machine, sid: o.session, view: "chat" });
  return true;
}

/** A sent forward at the top of its bubble: the arrow and heading, the computer and time, four lines (iOS QuoteBlock.forwardLines); a click
 *  opens the original (openForwarded), or, when this page does not have that chat, the whole text in place. */
export function ForwardBlock({ c }: { c: Context }) {
  const clampRef = useRef<HTMLSpanElement>(null);
  const [open, setOpen] = useState(false);
  const [long, setLong] = useState(false);
  const heading = forwardHeading(c.attrs);
  const meta = forwardMeta(c.attrs);
  const summary = forwardSummary(c.text);
  useLayoutEffect(() => {
    const el = clampRef.current;
    if (el && !open) setLong(el.scrollHeight > el.clientHeight + 1);
  }, [summary, open]);
  const go = () => {
    if (!openForwarded(c)) setOpen(true);
  };
  return (
    <div className="flex min-w-0 gap-2 rounded-[8px] bg-ink/[0.05] py-1.5 pr-2.5 pl-2 whitespace-normal" data-forward>
      <span aria-hidden className="w-[3px] shrink-0 self-stretch rounded-full bg-ink/20" />
      <div className="flex min-w-0 flex-1 flex-col items-start gap-0.5">
        <button
          type="button"
          onClick={go}
          title={t("chat-forward.open_original")}
          aria-label={[heading, meta, summary].filter(Boolean).join(": ")}
          // a grid track of minmax(0, auto): the heading's one line never widens the bubble past the chat; it truncates
          className="grid w-full min-w-0 grid-cols-[minmax(0,auto)] gap-0.5 rounded-[6px] text-left"
        >
          <span className="flex max-w-full min-w-0 items-center gap-1 text-[12px] font-medium whitespace-nowrap text-ink-3">
            <Icon name="forward" size={12} className="shrink-0" />
            <span className="min-w-0 truncate">{heading}</span>
          </span>
          {meta && <span className="max-w-full truncate text-[11.5px] text-ink-3">{meta}</span>}
          {!open && (
            <span ref={clampRef} className="line-clamp-4 text-[13px] leading-[1.55] whitespace-pre-line text-ink-2 [overflow-wrap:anywhere]">
              {summary}
            </span>
          )}
        </button>
        {open && (
          <div className="max-h-[320px] w-full overflow-y-auto text-ink-2 [overflow-wrap:anywhere]" data-scroll-box>
            <Markdown text={c.text} small compact />
          </div>
        )}
        {(long || open) && (
          <button
            type="button"
            onClick={() => setOpen((o) => !o)}
            aria-expanded={open}
            className="-ml-1 rounded-[6px] px-1 text-[12px] font-medium whitespace-nowrap text-ink-3 transition-colors hover:bg-hover-2 hover:text-ink-2"
          >
            {open ? t("chat-forward.collapse") : t("chat-forward.show_all")}
          </button>
        )}
      </div>
    </div>
  );
}
