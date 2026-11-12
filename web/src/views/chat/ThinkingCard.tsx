// While the agent is thinking right now (the screen's status line, `live.thinking`), the line at the end of the
// innermost live work group says Thinking with three dots and a clock (#5155). A thought itself is a row
// (ThinkingRow.tsx).
import { useState } from "react";
import { useStore } from "../../lib/store";
import { workDuration } from "../../lib/work-groups";
import { useNow } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { t } from "../../i18n";

/** The live line: Thinking, three dots, and the time since this line saw the thinking start. */
export function LiveThinking() {
  const [since] = useState(Date.now);
  const now = useNow(500, useStore((s) => s.conn === "online"));
  return (
    <div className="flex w-full min-w-0 items-center gap-1.5 px-2 py-0.5 select-none [@media(pointer:coarse)]:min-h-10" data-thinking="live" aria-live="polite">
      <span className="inline-flex h-[1lh] shrink-0 items-center text-tool-ink">
        <Icon name="spark" size={14} />
      </span>
      <span className="translate-y-[1px] text-[length:var(--chat-secondary)] text-tool-ink">{t("chat-thinkingcard.thinking")}</span>
      <span aria-hidden className="flex translate-y-[2px] items-center gap-[3px]">
        {[0, 200, 400].map((d) => (
          <span key={d} className="size-1 rounded-full bg-tool-ink animate-[breathe_1.2s_ease-in-out_infinite] busy-motion" style={{ animationDelay: `${d}ms` }} />
        ))}
      </span>
      <span className="flex-1" />
      <span className="font-mono text-[12px] text-tool-ink tabular-nums">{workDuration(Math.max(0, now - since))}</span>
    </div>
  );
}
