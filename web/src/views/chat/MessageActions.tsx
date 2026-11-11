// What you can do with a message (iOS MessageMenu.swift: "Copy" / "Select" / "Quote" / "Edit"): with a mouse, a small bar under it
// on hover and the same items on right-click; on a touch screen, a long press opens them, or a reply's ⋯ beside its copy button
// (and "Select" shows the text to select, since the press no longer selects). "Quote" puts it as a quote in front of the draft, "Edit" (your own messages)
// puts it back in the composer to change and send again. "Hand to Persona" sends an Agent message to the main chat;
// "Use as context" attaches it to this chat's next message. Sharing as a card is the phone's. A reply's bar ends in a ⋯
// whose menu has "Branch to new chat" (#2137: a new conversation with what was said up to this reply) when the session can.
// Beside your own bubbles, the two context actions live in ⋯ to keep the hover bar compact.
import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import { t } from "../../i18n";
import { clock } from "../../lib/format";
import { sendDraft, type QuotedTask } from "../../lib/drafts";
import { errorText } from "../../lib/api";
import { contextXml } from "../../lib/kb";
import { addContext, newContext } from "../../lib/links";
import { askPersona } from "../../lib/outbox";
import { Button, copyText, cx, Dialog, Menu, toast, type MenuItem } from "../../ui/atoms";
import { Icon, type IconName } from "../../ui/icons";
import type { RewindRestore } from "../../lib/rewind";
import { isNoHover, NO_HOVER, useMedia } from "../../ui/media-query";
import { useChatActions, type ChatActions } from "./chat-actions";


function pointAnchor(x: number, y: number): HTMLElement {
  const el = document.createElement("span");
  el.style.cssText = `position:fixed;left:${x}px;top:${y}px;width:0;height:0;pointer-events:none`;
  document.body.appendChild(el);
  return el;
}

/** The actions of one message; empty when there is nothing to act on. */
export interface Action extends Pick<MenuItem, "hint" | "disabled" | "danger" | "confirm" | "inline" | "children"> {
  key: string;
  label: string;
  icon: IconName;
  onSelect: () => void | Promise<unknown>;
}

function useActions(text: string, isUser: boolean, ts: string | undefined, task: QuotedTask | undefined, select: () => void): Action[] {
  const chat = useChatActions();
  if (!text.trim()) return [];
  const out: Action[] = [{ key: "copy", label: t("chat-messageactions.copy"), icon: "copy", onSelect: () => void copyText(text) }];
  if (isNoHover()) out.push({ key: "select", label: t("chat-messageactions.select_text"), icon: "read", onSelect: select });
  if (chat?.canCompose) {
    out.push({ key: "quote", label: t("chat-messageactions.quote"), icon: "quote", onSelect: () => void sendDraft(chat.draftKey, { type: "quote", text, from: isUser ? t("chat-messageactions.me") : chat.speaker, ts, task }) });
  }
  out.push(...messageContextActions(text, chat));
  if (chat?.canCompose && isUser) out.push({ key: "edit", label: t("chat-messageactions.edit"), icon: "pencil", onSelect: () => void sendDraft(chat.draftKey, { type: "replace", text }) });
  return out;
}

/** The message goes to Persona now, or to its own conversation's next message (Android MessageMenu). */
export function messageContextActions(text: string, chat: ChatActions | null): Action[] {
  if (!chat || !text.trim()) return [];
  const title = t("chat-messageactions.context_title", { title: chat.title });
  const out: Action[] = [];
  if (chat.isAgent) out.push({
    key: "handToPersona", label: t("chat-messageactions.hand_to_persona"), icon: "spark",
    onSelect: async () => {
      try {
        await askPersona(`${contextXml({ kind: "context", title, url: "", text })}\n\n${t("chat-messageactions.handoff_ask")}`);
      } catch (e) {
        toast(t("lib-agent-actions.send_failed", { error: errorText(e) }), "error");
      }
    },
  });
  if (chat.canCompose) out.push({
    key: "context", label: t("chat-messageactions.use_as_context"), icon: "clip",
    onSelect: () => addContext(chat.draftKey, newContext("context", title, "", text)),
  });
  return out;
}

/** What the reply's ⋯ holds (also at the end of its right-click menu): "Branch to new chat" when the session can fork and the
 *  reply is a transcript line of its own (`<uuid>#<n>`; lines without a uuid are `line<seq>`, which the node cannot find). */
export function moreActions(itemId: string | undefined, fork: ((itemId: string) => void) | undefined): Action[] {
  if (!itemId || !fork || itemId.startsWith("line") || !itemId.includes("#")) return [];
  return [{ key: "fork", label: t("chat-messageactions.fork"), icon: "fork", onSelect: () => fork(itemId) }];
}

/** "Delete", last and in red, on a message only on this device (#4626, the apps' long press): in the right-click / long-press
 *  menu, and for your own bubbles in ⋯. */
export function deleteActions(onDelete: (() => void) | undefined): Action[] {
  return onDelete ? [{ key: "delete", label: t("chat-outboxbubbles.delete"), icon: "trash", danger: true, onSelect: onDelete }] : [];
}

type Rewind = NonNullable<ChatActions["rewind"]>;

/** "Rewind to here" (#2436) on a message of yours: "Conversation only" / "Code and conversation", the second asking again (it puts files back too). Off
 *  with the reason as its hint while the session cannot ("Claude only", "Working"…). Each choice waits for the node inside the
 *  menu (it drives Claude's own /rewind, up to a minute) and shows the failure there. */
export function rewindActions(messageId: string | undefined, text: string, rewind: { block: string; run: Rewind["run"] } | undefined): Action[] {
  if (!messageId || !rewind || !text.trim()) return [];
  const go = (restore: RewindRestore) => () => rewind.run(messageId, text, restore);
  return [
    {
      key: "rewind",
      label: t("rewind.menu"),
      icon: "undo",
      hint: rewind.block || undefined,
      disabled: Boolean(rewind.block),
      onSelect: () => {},
      children: [
        { key: "rewind-conversation", label: t("rewind.conversation"), inline: true, onSelect: go("conversation") },
        { key: "rewind-code", label: t("rewind.code_and_conversation"), danger: true, confirm: t("rewind.confirm_code"), inline: true, onSelect: go("code_and_conversation") },
      ],
    },
  ];
}

const NO_STORE = () => () => {};

/** `live`'s value while `open` (a menu that shows it), `closed` otherwise: a closed menu does not render again when it
 *  changes (why "Rewind to here" is off changes each time the session starts or stops working). */
function useWhileOpen(live: Rewind["block"] | undefined, open: boolean, closed: string): string {
  const on = open ? live : undefined;
  const get = on ? on.get : () => closed;
  return useSyncExternalStore(on ? on.subscribe : NO_STORE, get, get);
}

/** Wraps a message: right-click / long press opens its menu. The hover bar goes under it (with the time), or beside it
 *  (`side`, your own bubbles: they had no row under them, so the layout does not move; the time stays the tooltip), or
 *  over its top right corner (`corner`, a task's card: no row under it either). `task`: "Quote" quotes it as that task. */
export function MessageActions({
  text,
  isUser,
  ts,
  side,
  corner,
  task,
  itemId,
  messageId,
  onDelete,
  children,
  className,
}: {
  text: string;
  isUser?: boolean;
  ts?: string;
  side?: boolean;
  corner?: boolean;
  task?: QuotedTask;
  /** The reply's transcript item: where "Branch to new chat" branches off. */
  itemId?: string;
  /** Your message (UserMessage.id): where "Rewind to here" goes back to. */
  messageId?: string;
  /** A message only on this device (a failed send): "Delete" ends its menus (#4626). */
  onDelete?: () => void;
  children: React.ReactNode;
  className?: string;
}) {
  const [selecting, setSelecting] = useState(false);
  const touch = useMedia(NO_HOVER);
  const actions = useActions(text, Boolean(isUser), ts, task, () => setSelecting(true));
  const chat = useChatActions();
  const [moreAnchor, setMoreAnchor] = useState<HTMLElement | null>(null);
  const [anchor, setAnchor] = useState<HTMLElement | null>(null);
  const rewind = isUser ? chat?.rewind : undefined;
  const rewindBlock = useWhileOpen(rewind?.block, Boolean(anchor || moreAnchor), "");
  const extras = [
    ...moreActions(itemId, chat?.fork),
    ...rewindActions(messageId, text, rewind && { block: rewindBlock, run: rewind.run }),
    ...deleteActions(onDelete),
  ];
  const contextActions = side ? actions.filter((a) => a.key === "handToPersona" || a.key === "context") : [];
  const more: MenuItem[] = [...contextActions, ...extras].map((a, i) => ({ ...a, divider: i === contextActions.length && i > 0 }));
  const anchorRef = useRef<HTMLElement | null>(null);
  const press = useRef<{ timer: number; x: number; y: number } | null>(null);
  const opened = useRef(0);

  const openAt = (x: number, y: number) => {
    anchorRef.current?.remove();
    const el = pointAnchor(x, y);
    anchorRef.current = el;
    setAnchor(el);
  };
  const close = () => {
    anchorRef.current?.remove();
    anchorRef.current = null;
    setAnchor(null);
  };
  useEffect(() => () => anchorRef.current?.remove(), []);

  const cancelPress = () => {
    if (press.current) clearTimeout(press.current.timer);
    press.current = null;
  };

  if (!actions.length) return <div className={className}>{children}</div>;

  const items: MenuItem[] = [...actions, ...extras].map((a, i) => ({ ...a, divider: i === actions.length && i > 0 }));

  return (
    <div className={cx("group/msg", (side || corner) && "relative", className)}>
      <div
        className="[@media(hover:none)]:select-none [@media(hover:none)]:[-webkit-touch-callout:none]"
        onContextMenu={(e) => {
          if (Date.now() - opened.current < 1000) {
            e.preventDefault(); // the long press already opened ours
            return;
          }
          // a selection inside the message (the browser's menu copies it), a link, a picture or code: the browser's own
          const sel = getSelection();
          if (sel && !sel.isCollapsed && sel.anchorNode && e.currentTarget.contains(sel.anchorNode)) return;
          if ((e.target as Element).closest?.("a, img, pre")) return;
          e.preventDefault();
          openAt(e.clientX, e.clientY);
        }}
        onPointerDown={(e) => {
          if (e.pointerType !== "touch") return;
          cancelPress();
          const x = e.clientX,
            y = e.clientY;
          press.current = {
            x,
            y,
            timer: window.setTimeout(() => {
              press.current = null;
              opened.current = Date.now();
              openAt(x, y);
            }, 450),
          };
        }}
        onPointerMove={(e) => {
          const p = press.current;
          if (p && Math.hypot(e.clientX - p.x, e.clientY - p.y) > 8) cancelPress();
        }}
        onPointerUp={cancelPress}
        onPointerCancel={cancelPress}
        // the finger lifting after the press opened the menu: no mouse events after it, whose mousedown would land
        // outside the menu and close it at once
        onTouchEnd={(e) => {
          if (Date.now() - opened.current < 1000) e.preventDefault();
        }}
      >
        {children}
      </div>
      <div
        className={cx(
          "hidden h-6 items-center gap-0.5 opacity-0 transition-opacity duration-200 group-hover/msg:opacity-100 focus-within:opacity-100 [@media(hover:hover)]:flex",
          moreAnchor && "opacity-100", // the ⋯ menu is open: its bar stays while the pointer is in the menu
          side ? "absolute right-full bottom-0.5 mr-1.5" : corner ? "absolute top-2 right-2 rounded-[8px] bg-surface px-0.5 shadow-hairline" : "mt-1.5",
        )}
      >
        {actions
          .filter((a) => a.key !== "select" && !contextActions.includes(a))
          .map((a) => (
            <button
              key={a.key}
              type="button"
              aria-label={a.key === "copy" ? (isUser ? t("chat-messageactions.copy_message") : t("chat-messageactions.copy_reply")) : a.label}
              title={a.label}
              onClick={a.onSelect}
              className="flex size-6 items-center justify-center rounded-[6px] text-ink-3 transition-colors hover:bg-hover-2 hover:text-ink-2"
            >
              <Icon name={a.icon} size={14} />
            </button>
          ))}
        {more.length > 0 && (
          <button
            type="button"
            aria-label={t("chat-messageactions.more")}
            title={t("chat-messageactions.more")}
            aria-haspopup="menu"
            aria-expanded={Boolean(moreAnchor)}
            onClick={(e) => setMoreAnchor(moreAnchor ? null : e.currentTarget)}
            className={cx("flex size-6 items-center justify-center rounded-[6px] text-ink-3 transition-colors hover:bg-hover-2 hover:text-ink-2", moreAnchor && "bg-hover-2 text-ink-2")}
          >
            <Icon name="more" size={14} />
          </button>
        )}
        {!side && !corner && ts && <span className="ml-1 text-[11px] text-ink-3">{clock(ts)}</span>}
      </div>
      {touch && !isUser && !side && !corner && (
        <div className="mt-1 flex h-6 items-center gap-5">
          <button type="button" aria-label={t("chat-messageactions.copy_reply")} title={t("chat-messageactions.copy")} onClick={() => void copyText(text)} className="touch-hit flex size-6 items-center justify-center rounded-[6px] text-ink-3">
            <Icon name="copy" size={14} />
          </button>
          <button
            type="button"
            aria-label={t("chat-messageactions.more")}
            title={t("chat-messageactions.more")}
            aria-haspopup="menu"
            aria-expanded={Boolean(anchor)}
            onClick={(e) => (anchor ? close() : setAnchor(e.currentTarget))}
            className="touch-hit flex size-6 items-center justify-center rounded-[6px] text-ink-3"
          >
            <Icon name="more" size={14} />
          </button>
        </div>
      )}
      {anchor && <Menu anchor={anchor} onClose={close} items={items} width={items.some((i) => i.hint) ? 200 : 168} />}
      {moreAnchor && <Menu anchor={moreAnchor} onClose={() => setMoreAnchor(null)} items={more} side="top" width={more.some((a) => a.hint) ? 200 : 168} />}
      <Dialog
        open={selecting}
        onClose={() => setSelecting(false)}
        title={t("chat-messageactions.select_text")}
        footer={
          <>
            <Button tone="quiet" onClick={() => setSelecting(false)}>
              {t("chat-messageactions.done")}
            </Button>
            <Button
              tone="primary"
              onClick={() => {
                void copyText(text);
                setSelecting(false);
              }}
            >
              {t("chat-messageactions.copy_all")}
            </Button>
          </>
        }
      >
        <div className="max-h-[50vh] overflow-y-auto text-[14px] leading-relaxed whitespace-pre-wrap text-ink select-text [overflow-wrap:anywhere]">{text}</div>
      </Dialog>
    </div>
  );
}
