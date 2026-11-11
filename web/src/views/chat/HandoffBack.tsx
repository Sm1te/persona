// The strip at the top of an Agent's page opened from a conversation's hand-off card (#5096, cindy's HandoffSourcePill):
// "Came from <conversation> · click to return", with ✕ to put it away. Returning goes back in the history, so the
// conversation's chat is where it was left (lib/handoff.ts).
import { type HandoffFrom } from "../../lib/handoff";
import { navigate, parse } from "../../lib/router";
import { useStore, type State } from "../../lib/store";
import { IconButton } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { conversationTitle } from "../assistant/conversations";
import { t } from "../../i18n";

/** What the conversation at `path` is called now: its title, Persona for the main one. */
export function fromTitle(s: Pick<State, "assistant">, path: string): string {
  const [p, q] = path.split("?");
  const r = parse(p, q ? `?${q}` : "");
  const main = s.assistant?.main ?? "";
  const sid = r.name === "persona" ? r.sid || main : "";
  const row = s.assistant?.conversations.find((c) => c.session_id === sid);
  return conversationTitle(row ?? { title: "" }, sid === main);
}

export function HandoffBack({ from, onDismiss }: { from: HandoffFrom; onDismiss: () => void }) {
  const title = useStore((s) => fromTitle(s, from.path));
  // the entry before this one is the conversation (the card pushed this one); a page opened on its own has none to go to
  const back = () => (history.length > 1 ? history.back() : navigate(from.path));
  return (
    <div data-handoff-back className="shrink-0 px-4 pt-3">
      <div className="mx-auto flex h-10 w-full max-w-[880px] items-center rounded-xl border border-line bg-surface text-[13px] leading-none text-ink-2">
        <button
          type="button"
          onClick={back}
          className="flex h-full min-w-0 flex-1 items-center gap-2 rounded-l-xl px-3 text-left transition-colors hover:text-ink focus-visible:ring-2 focus-visible:ring-ink focus-visible:outline-none"
        >
          <Icon name="cornerUpLeft" size={14} stroke={2} className="shrink-0" />
          <span className="min-w-0 truncate">{t("chat-chatview.handoff_from", { title })}</span>
        </button>
        <IconButton icon="x" label={t("chat-chatview.handoff_dismiss")} onClick={onDismiss} size={28} iconSize={14} className="mr-2" />
      </div>
    </div>
  );
}
