// Above the composer while the host stopped the session's turn on an error it will not retry by itself (a botmux
// notice, iOS FailureNote): the reason, "Retry" (sends "Continue" alone, not what is in the box) and "Open in Lark".
import { attentionReason, externalLink, failureReason, sessionCan, stateOf } from "../../lib/format";
import type { Session } from "../../lib/types";
import { Icon } from "../../ui/icons";
import { t } from "../../i18n";

/** Whether the note shows: a notice the session can be retried from (not working, not on a card, not view-only),
 *  and not the one "Retry" was already pressed on (`retried`: the chat keeps it while the hub catches up). */
export function showsFailureNote(session: Session, o: { retried?: string } = {}): boolean {
  const reason = failureReason(session);
  if (!reason || reason === o.retried) return false;
  if (attentionReason(session) || stateOf(session) === "needs_you") return false;
  return sessionCan(session, "send");
}

export function FailureNote({ session, onRetry }: { session: Session; onRetry: () => void }) {
  const reason = failureReason(session);
  if (!reason) return null;
  const link = externalLink(session);
  return (
    <div role="status" data-testid="chat-failure" className="flex flex-col gap-1.5 rounded-[12px] bg-red-tint px-3 py-2 text-[12.5px]" style={{ animation: "fade-up 200ms ease-out both" }}>
      <div className="flex items-start gap-2">
        <span className="flex h-[18px] shrink-0 items-center text-red"><Icon name="warning" size={13} stroke={2.2} /></span>
        <span className="line-clamp-3 min-w-0 flex-1 text-ink-2">{reason}</span>
      </div>
      <div className="flex items-center gap-3 pl-[21px]">
        <button
          type="button"
          data-testid="chat-failure-retry"
          onClick={onRetry}
          className="flex shrink-0 items-center gap-1 whitespace-nowrap font-medium text-accent hover:underline"
        >
          <Icon name="retry" size={12} stroke={2.2} />
          {t("chat-chatview.retry")}
        </button>
        {link && (
          <a href={link} target="_blank" rel="noopener noreferrer" data-testid="chat-failure-lark" className="flex shrink-0 items-center gap-1 whitespace-nowrap font-medium text-accent hover:underline">
            <Icon name="external" size={12} />
            {t("chat-chatview.open_in_lark")}
          </a>
        )}
      </div>
    </div>
  );
}
