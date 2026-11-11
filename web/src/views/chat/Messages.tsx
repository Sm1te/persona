// Messages that are not plain bubbles (iOS MergedUserBubble / PeerMessage / LoopTickRow): a finished exchange's several
// messages in one bubble, a message another Claude session sent, and a /loop waking the agent again.
import { openTask } from "../../lib/handoff";
import { useState } from "react";
import { clock } from "../../lib/format";
import { splitQuotes } from "../../lib/drafts";
import { taskOfInjected } from "../../lib/injected";
import { getSession, useStore } from "../../lib/store";
import type { Injected } from "../../lib/types";
import type { UserMessage } from "../../lib/turns";
import { cx } from "../../ui/atoms";
import { Icon, type IconName } from "../../ui/icons";
import { AttachedPhotos } from "./AttachedPhotos";
import { FileCards } from "./FileCards";
import { MessageActions } from "./MessageActions";
import { QuoteStack } from "./Quote";
import { Markdown } from "../../ui/Markdown";
import { PathText } from "../../ui/PathLinks";
import { Thumb } from "./Steps";
import { FoldedReport, isHandbackText } from "./FoldedReport";
import { t } from "../../i18n";

export function MergedBubble({ msg, mid, sid }: { msg: UserMessage; mid: string; sid: string }) {
  const parts = msg.parts ?? [msg.text];
  return (
    <div className="flex flex-col items-end gap-1.5 pl-[12%]">
      {msg.images.length > 0 && (
        <div className="flex flex-wrap justify-end gap-1.5">
          {msg.images.map((im) => (
            <Thumb key={im.id} item={im} mid={mid} sid={sid} size={96} />
          ))}
        </div>
      )}
      {msg.photos && <AttachedPhotos paths={msg.photos} mid={mid} sid={sid} />}
      {msg.files && <FileCards files={msg.files} mid={mid} />}
      <MessageActions text={parts.join("\n\n")} isUser ts={msg.ts} side className="max-w-full">
        <div className="flex max-w-full flex-col gap-2 rounded-[14px] bg-user-bubble px-3.5 py-2 text-[length:var(--chat-secondary)] leading-[1.55] text-ink" title={clock(msg.ts)} aria-label={t("chat-messages.message_count", { count: parts.length })}>
          {parts.map((part, i) => (
            <div key={i} className="flex flex-col gap-2">
              {i > 0 && <span aria-hidden className="mx-auto h-[2px] w-6 rounded-full bg-ink/20" />}
              <MergedPart text={part} mid={mid} sid={sid} />
            </div>
          ))}
        </div>
      </MessageActions>
    </div>
  );
}

/** One of the merged messages; one sent with "Quote" starts with its quote card (Quote.tsx). */
function MergedPart({ text, mid, sid }: { text: string; mid: string; sid: string }) {
  const split = splitQuotes(text);
  const words = split ? split.rest : text;
  return (
    <>
      {split && <QuoteStack blocks={split.quotes} mid={mid} sid={sid} />}
      {words && (
        <span className="whitespace-pre-wrap [overflow-wrap:anywhere]">
          <PathText text={words} mid={mid} cwd={getSession(mid, sid)?.cwd} />
        </span>
      )}
    </>
  );
}

// Its words are Markdown like an answer: agents write Markdown, and the hub turns a teammate's JSON status notes into it.
// A single line break stays one, as in the message it was (iOS: markdownSoftBreakMode(.lineBreak)).
// A subagent's report that a node from before #6381 handed on as a peer's message (Claude Code's hand-back frame at its
// start) starts folded like the other reports; another session's own message is shown as it was.
export function PeerMessage({ msg }: { msg: UserMessage }) {
  const words = <Markdown text={keepLineBreaks(msg.text)} small />;
  return (
    <div className="flex min-w-0 flex-col gap-1.5 rounded-card bg-inset px-3.5 py-2.5 shadow-hairline sm:mr-[12%]" title={clock(msg.ts)}>
      <span className="flex items-center gap-1.5 text-[12px] font-medium whitespace-nowrap text-ink-3">
        <Icon name="loop" size={12} />
        {t("chat-messages.from", { name: msg.from ?? "" })}
      </span>
      {isHandbackText(msg.text) ? (
        <FoldedReport id={`peer:${msg.id}`} text={msg.text} className="text-[13px] leading-relaxed">
          {words}
        </FoldedReport>
      ) : (
        words
      )}
    </div>
  );
}

// Markdown joins the lines of a paragraph; two spaces before the newline keep it a line break.
export function keepLineBreaks(text: string): string {
  return text.replace(/([^\n])\n(?=[^\n])/g, "$1  \n");
}

export function LoopTick({ msg }: { msg: UserMessage }) {
  const rest = msg.text.slice(5).trim();
  return (
    <div className="flex justify-end">
      <span className="flex max-w-full min-w-0 items-center gap-1.5 rounded-full bg-inset px-3 py-1 text-[12px] text-ink-2 shadow-hairline">
        <Icon name="loop" size={12} className="shrink-0" />
        <span className="shrink-0 font-medium whitespace-nowrap">{t("chat-messages.loop_tick")}</span>
        {msg.ts && <span className="shrink-0 tabular-nums">{clock(msg.ts)}</span>}
        {rest && <span className="min-w-0 truncate text-ink-3">{rest}</span>}
      </span>
    </div>
  );
}

function injectedIcon(n: Injected): { name: IconName; tone: string } {
  if (n.kind === "task_notice") {
    if (n.state === "needs") return { name: "hand", tone: "text-accent" };
    if (n.state === "interrupted" || n.state === "failed") return { name: "warning", tone: "text-red" };
    return { name: "check", tone: "text-green" };
  }
  if (n.kind === "followup") return n.state === "wake" ? { name: "bell", tone: "text-accent" } : { name: "clock", tone: "text-accent" };
  return { name: "info", tone: "text-ink-3" };
}

/** A message Persona typed in for the model (a task's notice, a scheduled run…): one centered line, never its words.
 *  A task's opens the task; a scheduled run's shows what the task asks. docs/protocol/protocol.md "Messages injected by Persona". */
export function InjectedHint({ msg, sid }: { msg: UserMessage; sid: string }) {
  const n = msg.injected!;
  const todos = useStore((s) => s.todos);
  const [open, setOpen] = useState(false);
  const todo = n.kind === "task_notice" ? taskOfInjected(n, todos, sid) : undefined;
  const icon = injectedIcon(n);
  const body = (
    <>
      <Icon name={icon.name} size={12} className={cx("shrink-0", icon.tone)} />
      <span className="min-w-0 truncate">{n.line}</span>
      {msg.ts && <span className="shrink-0 text-ink-3 tabular-nums">{clock(msg.ts)}</span>}
      {(todo || n.detail) && <Icon name={todo ? "chevronRight" : open ? "up" : "chevron"} size={11} className="shrink-0 text-ink-3" />}
    </>
  );
  const pill = "flex max-w-full min-w-0 items-center gap-1.5 rounded-full bg-inset px-3 py-1 text-[12px] text-ink-2 shadow-hairline";
  return (
    <div className="flex flex-col items-center gap-2" data-injected={n.kind}>
      {todo ? (
        <button type="button" className={cx(pill, "hover:bg-hover")} onClick={() => openTask(todo)} aria-label={t("chat-messages.open_task", { line: n.line })}>
          {body}
        </button>
      ) : n.detail ? (
        <button type="button" className={cx(pill, "hover:bg-hover")} onClick={() => setOpen((o) => !o)} aria-expanded={open}>
          {body}
        </button>
      ) : (
        <span className={pill}>{body}</span>
      )}
      {open && n.detail && (
        <div className="w-full max-w-[560px] rounded-card bg-inset px-3 py-2 text-[12.5px] leading-relaxed text-ink-2 shadow-hairline">
          <Markdown text={keepLineBreaks(n.detail)} small />
        </div>
      )}
    </div>
  );
}
