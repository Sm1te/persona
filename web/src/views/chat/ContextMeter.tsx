// The session header's thin context bar (#2430): `▬▬ 61%`, the tooltip "61% context left" ("39% used" with Settings ›
// Appearance › Show context as used). The model it runs is the composer's model pill (#5032), so the header no longer names
// it. Always shown in a wide header, like Moshi's session header (the user asked to follow Moshi); "Show details in Agent
// rows" only gates the sidebar rows' second line. Where the header is narrower than 800px (a phone, a side panel open
// beside the chat) the header hides it so the title fits (#3532, #3670). Where the header is 1000px or wider the number
// says what it is ("Context 61% left", #3668); between 800 and 1000px it stays the bare share so the title keeps its room. The tooltip
// adds what happens when the window fills and, once close, what to do about it. A tablet has no hover, so a click (a tap)
// opens the same words as a popover, with the token count (#4054). Where the header is 680–800px wide (an iPad in landscape
// with the sidebar open, #4443) it is `compact`: the bare share alone, coloured as it fills, opening the same popover.
// Nothing while the window is unknown (no reply yet, a model the node doesn't know).
import { useEffect } from "react";
import { contextHeaderLabel, contextHint, contextLabel, contextNotes, contextShown, contextTier, contextTooltip, contextUsage, useContextAsUsed, type ContextTier } from "../../lib/context-usage";
import { cx, EASE, Menu, useMenu } from "../../ui/atoms";
import type { Session } from "../../lib/types";

const TIER_BAR: Record<ContextTier, string> = { ok: "bg-accent", warn: "bg-orange", danger: "bg-orange" };
const TIER_TEXT: Record<ContextTier, string> = { ok: "", warn: "font-medium text-orange", danger: "font-medium text-orange" };

export function ContextMeter({ session, compact = false }: { session: Session; compact?: boolean }) {
  const asUsed = useContextAsUsed();
  const info = useMenu();
  const usage = contextUsage(session);
  const known = usage !== null;
  // the window went unknown (a /clear) while it was open: close it, or it would come back by itself on the next reply
  useEffect(() => {
    if (!known) info.close();
    // eslint-disable-next-line react-hooks/exhaustive-deps -- only a change of `known` closes it
  }, [known]);
  if (!usage) return null;
  const tier = contextTier(usage);
  const tip = contextTooltip(usage, asUsed);
  const title = [tip, contextHint(usage)].join("\n");
  return (
    <>
      <button
        type="button"
        onClick={info.open}
        aria-haspopup="dialog"
        aria-expanded={Boolean(info.anchor)}
        // the visible words are in the name ("Context 61% left", or the bare "61%" in a narrower header; the compact one says
        // "Context 61% left" around its bare share) and the explanation
        // is the description, open or not; the tooltip goes while the popover shows the same words
        aria-label={contextHeaderLabel(usage, asUsed)}
        aria-description={contextHint(usage)}
        title={info.anchor ? undefined : title}
        className={cx("touch-hit -mx-1.5 flex shrink-0 items-center gap-1.5 rounded-chip px-1.5 text-[11.5px] whitespace-nowrap text-ink-3 transition-colors", info.anchor && "bg-hover")}
      >
        {compact ? (
          <span className={cx("tabular-nums", TIER_TEXT[tier])}>{contextLabel(usage, asUsed)}</span>
        ) : (
          <>
            <span
              className="block h-1 w-10 overflow-hidden rounded-full bg-hover-2"
              role="progressbar"
              aria-label={tip}
              aria-valuenow={contextShown(usage, asUsed)}
              aria-valuemin={0}
              aria-valuemax={100}
            >
              <span className={cx("block h-full rounded-full transition-[width] duration-300", TIER_BAR[tier])} style={{ width: `${contextShown(usage, asUsed)}%`, transitionTimingFunction: EASE }} />
            </span>
            <span className={cx("tabular-nums @min-[1000px]:hidden", TIER_TEXT[tier])}>{contextLabel(usage, asUsed)}</span>
            <span className={cx("hidden tabular-nums @min-[1000px]:inline", TIER_TEXT[tier])}>{contextHeaderLabel(usage, asUsed)}</span>
          </>
        )}
      </button>
      {info.anchor && <Menu anchor={info.anchor} onClose={info.close} align="right" width={300} items={contextNotes(usage, asUsed)} />}
    </>
  );
}
