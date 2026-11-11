// Messages typed while the agent works and not yet taken (Session.queued), above the composer instead of in the
// chat (iOS QueueStrip). "Pull back to edit" pulls them back out of Claude (like ↑ in the terminal), puts that one in the
// composer and re-queues the others in order; "Delete" drops it the same way; "Move up / down" re-queues them all in the
// new order (#6088). A Claude takes them at its next step; "Send now" gives them to it at once, with Claude Code's own
// send-now (a command it runs goes on in the background), once confirmed. A queued message is only text, with what it
// carried written into it (#3032): rows show its words and a mark per kind of attachment, and pulling it back puts the
// words under what is typed, its photos back in the tray (read from the machine), its contexts back on their chips and
// a file back as its path.
// In a Persona chat (#4342) the queue is the headless Claude's: the node takes messages back over stream-json, and its
// answer carries their photos, which go back in the tray as they were sent. Messages from other conversations that the
// assistant holds until the turn ends come last (`queued_relayed`) and are only shown and copied.
import { useId, useMemo, useState } from "react";
import { api, errorText, fileDownloadURL, sessionPath } from "../../lib/api";
import { imagePayload, MAX_IMAGE, quotePath } from "../../lib/attachments";
import { sendDraft, type DraftImage } from "../../lib/drafts";
import { addContext, newContext, type ContextRef } from "../../lib/links";
import { agentLabel } from "../../lib/mentions";
import { keepAllBut, moved, parseQueued, queuedDraft, queuedMarks, type QueuedParts } from "../../lib/queue";
import type { Session } from "../../lib/types";
import { Button, Collapse, copyText, cx, Dialog, Menu, Spinner, toast, useMenu, type MenuItem } from "../../ui/atoms";
import { Icon, type IconName } from "../../ui/icons";
import { t } from "../../i18n";

/** A photo of a queued message, read back from the session's machine for the composer's tray; null when it can't be. */
export async function readPhoto(mid: string, path: string, cwd?: string): Promise<DraftImage | null> {
  try {
    const r = await fetch(fileDownloadURL(mid, path, cwd, { inline: true }));
    const blob = r.ok ? await r.blob() : null;
    if (!blob || !blob.type.startsWith("image/") || blob.size > MAX_IMAGE) return null;
    const name = path.split("/").pop() || "image.png";
    return { name, ...(await imagePayload(new File([blob], name, { type: blob.type }))) };
  } catch {
    return null;
  }
}

/** A photo a Persona chat's queue gave back with its message (#4342), as the composer's tray holds it. */
export interface QueuedImage {
  mime: string;
  data: string; // base64
}

/** Puts a pulled-back message in the composer of `draftKey`, read apart. Photos the machine no longer gives back go in
 *  as their paths, so nothing that was sent is lost; `images` are photos that came back with it (a Persona chat's). */
export async function restoreQueued(draftKey: string, session: Session, p: QueuedParts, images: QueuedImage[] = []) {
  const text = queuedDraft(p);
  if (text) sendDraft(draftKey, { type: "append", text });
  // a kind this composer has no chip of its own for is shown as a quote and sent back as it came
  for (const c of p.contexts) addContext(draftKey, { ...newContext(c.kind as ContextRef["kind"], c.title, c.url, c.text), ...(c.attrs ? { attrs: c.attrs } : {}) });
  images.forEach((img, i) => sendDraft(draftKey, { type: "image", image: { name: `photo-${i + 1}.${img.mime.split("/")[1] || "png"}`, ...img } }));
  if (!p.photos.length) return;
  const read = await Promise.all(p.photos.map((path) => readPhoto(session.machine_id, path, session.cwd)));
  const lost: string[] = [];
  read.forEach((img, i) => {
    if (!img || !sendDraft(draftKey, { type: "image", image: img })) lost.push(p.photos[i]);
  });
  if (lost.length) {
    sendDraft(draftKey, { type: "append", text: lost.map(quotePath).join(" ") });
    toast(t("chat-queuestrip.photos_as_paths", { count: lost.length }), "error");
  }
}

/** One line, "N queued", that opens the messages; `compact` (a phone on its side) is the tighter size. `persona` is the
 *  persona's name in a Persona chat (#4342); `onWithdrawn` hears a message taken out of the queue, so its sending bubble
 *  goes too; `photosOf` says how many photos a queued message went with when this browser sent it (a Persona chat's
 *  queue holds only the words). */
export function QueueStrip({ session, draftKey, canEdit, compact, persona, onWithdrawn, photosOf }: { session: Session; draftKey: string; canEdit: boolean; compact?: boolean; persona?: string; onWithdrawn?: (text: string) => void; photosOf?: (raw: string) => number }) {
  const queued = session.queued ?? [];
  // the user's own come first; the rest came from other conversations and are only shown (#4342)
  const own = queued.length - Math.min(Math.max(session.queued_relayed ?? 0, 0), queued.length);
  const agent = persona ?? agentLabel(session);
  const [busy, setBusy] = useState(false);
  const [open, setOpen] = useState(false);
  const listId = useId();

  const [confirming, setConfirming] = useState(false);
  // a Claude takes queued messages at its next step, and can be given them now (#6088); so does a Persona chat's (#4342)
  const claude = session.agent === "claude" || (persona !== undefined && own > 0);
  const path = sessionPath(session.machine_id, session.session_id);

  const run = async (work: () => Promise<void>) => {
    if (busy) return;
    setBusy(true);
    try {
      await work();
    } catch (e) {
      toast(t("chat-queuestrip.failed", { error: errorText(e) }), "error");
    } finally {
      setBusy(false);
    }
  };
  const act = (index: number, del: boolean) =>
    run(async () => {
      const r = await api<{ queue?: string[]; images?: QueuedImage[][] }>(`${path}/queue`, { method: "POST", json: { keep: keepAllBut(own, index) } });
      const pulled = r.queue ?? [];
      toast(del ? t("chat-queuestrip.deleted") : t("chat-queuestrip.pulled_back"), "ok");
      if (pulled[index] !== undefined) onWithdrawn?.(pulled[index]);
      const images = r.images?.[index] ?? [];
      if (!del && pulled[index] !== undefined) await restoreQueued(draftKey, session, parseQueued(pulled[index], images.length), images);
    });
  const move = (index: number, by: number) => {
    const keep = moved(own, index, by);
    if (keep) void run(async () => {
      await api(`${path}/queue`, { method: "POST", json: { keep } });
      toast(t("chat-queuestrip.moved"), "ok");
    });
  };
  const sendNow = () => {
    setConfirming(false);
    void run(async () => {
      await api(`${path}/queue/send-now`, { method: "POST", json: {} });
      toast(t("chat-queuestrip.sent_now", { agent }), "ok");
    });
  };

  if (!queued.length) return null;
  const rows = queued.map((q, i) => {
    const mine = canEdit && i < own;
    return (
      <QueueRow key={`${i}:${q}`} n={i + 1} raw={q} photos={i < own ? (photosOf?.(q) ?? 0) : 0} disabled={busy} canEdit={mine}
        note={i < own ? undefined : t("chat-queuestrip.relayed_note")} onEdit={() => void act(i, false)} onDelete={() => void act(i, true)}
        onUp={mine && i > 0 ? () => move(i, -1) : undefined} onDown={mine && i < own - 1 ? () => move(i, 1) : undefined} />
    );
  });
  const head = (
    <>
      <Icon name="inbox" size={13} />
      <span className="min-w-0 flex-1 truncate">
        {t(claude ? "chat-queuestrip.summary_steer" : "chat-queuestrip.summary", { count: queued.length, agent })}
      </span>
      {busy && <Spinner />}
    </>
  );
  // Folded to the one-line summary by default; opened, about three rows show and the rest scroll.
  return (
    <div data-queue-strip className={cx("flex flex-col rounded-[12px] bg-surface shadow-card", compact ? "p-1" : "p-1.5")} style={{ animation: "fade-up 200ms ease-out both" }}>
      <div className="flex items-center gap-1">
        <button
          type="button"
          aria-expanded={open}
          aria-controls={listId}
          onClick={() => setOpen((o) => !o)}
          className={cx("flex min-w-0 flex-1 items-center gap-1.5 rounded-[8px] px-1.5 text-left text-[11.5px] font-medium text-ink-3 transition-colors hover:bg-hover", compact ? "h-7" : "h-8")}
        >
          {head}
          <Icon name="chevron" size={12} stroke={2.2} className={cx("shrink-0 transition-transform duration-200", open && "rotate-180")} />
        </button>
        {canEdit && claude && own > 0 && (
          <button
            type="button"
            disabled={busy}
            onClick={() => setConfirming(true)}
            data-send-now
            className={cx("flex shrink-0 items-center gap-1 rounded-[8px] px-2 text-[11.5px] font-medium whitespace-nowrap text-accent-ink transition-colors hover:bg-hover disabled:opacity-60", compact ? "h-7" : "h-8")}
          >
            <Icon name="send" size={12} />
            {t("chat-queuestrip.send_now")}
          </button>
        )}
      </div>
      <Collapse open={open}>
        <div id={listId} className="flex max-h-[120px] flex-col gap-1.5 overflow-y-auto overscroll-contain px-1 pt-1 pb-0.5">
          {rows}
        </div>
      </Collapse>
      <Dialog
        open={confirming}
        onClose={() => setConfirming(false)}
        title={t("chat-queuestrip.send_now_title", { count: own })}
        footer={
          <>
            <Button onClick={() => setConfirming(false)}>{t("common.cancel")}</Button>
            <Button tone="primary" icon="send" onClick={sendNow}>{t("chat-queuestrip.send_now")}</Button>
          </>
        }
      >
        <p className="text-[13px] leading-5 text-ink-2" data-send-now-body>
          {t(persona !== undefined ? "chat-queuestrip.send_now_body_persona" : "chat-queuestrip.send_now_body", { agent })}
        </p>
      </Dialog>
    </div>
  );
}

type MarkKind = ReturnType<typeof queuedMarks>[number]["kind"];
const MARK_ICON: Record<MarkKind, IconName> = { photo: "image", video: "video", file: "file", context: "quote" };
const markWords = (kind: MarkKind, count: number) =>
  kind === "photo"
    ? t("chat-queuestrip.mark_photo", { count })
    : kind === "video"
      ? t("chat-queuestrip.mark_video", { count })
      : kind === "file"
        ? t("chat-queuestrip.mark_file", { count })
        : t("chat-queuestrip.mark_context", { count });

/** One queued message. `photos`: ones it went with beside its words (a Persona chat's, when this browser sent it);
 *  `note`: a line under it (a message from another conversation arrives when the turn ends). */
function QueueRow({ n, raw, photos, note, disabled, canEdit, onEdit, onDelete, onUp, onDown }: { n: number; raw: string; photos: number; note?: string; disabled: boolean; canEdit: boolean; onEdit: () => void; onDelete: () => void; onUp?: () => void; onDown?: () => void }) {
  const menu = useMenu();
  const parts = useMemo(() => parseQueued(raw, photos), [raw, photos]);
  const marks = queuedMarks(parts, photos);
  // the words, then what came with them in words (for the label and the tooltip: "2 photos, 1 context")
  const carried = marks.map((m) => markWords(m.kind, m.count)).join(t("chat-queuestrip.mark_join"));
  const words = parts.text;
  const said = [words, carried, note].filter(Boolean).join(" · ");
  const items: MenuItem[] = [
    ...(onUp ? [{ key: "up", label: t("chat-queuestrip.move_up"), icon: "up" as const, onSelect: onUp }] : []),
    ...(onDown ? [{ key: "down", label: t("chat-queuestrip.move_down"), icon: "arrowDown" as const, onSelect: onDown }] : []),
    ...(canEdit ? [{ key: "edit", label: t("chat-queuestrip.pull_back"), icon: "pencil" as const, onSelect: onEdit }] : []),
    ...(words ? [{ key: "copy", label: t("chat-queuestrip.copy"), icon: "copy" as const, onSelect: () => void copyText(words) }] : []),
    ...(canEdit ? [{ key: "delete", label: t("chat-queuestrip.delete"), icon: "trash" as const, danger: true, onSelect: onDelete }] : []),
  ];
  return (
    <>
      <button
        type="button"
        onClick={menu.open}
        disabled={disabled}
        title={said}
        aria-label={t("chat-queuestrip.row_label", { n, text: said })}
        aria-haspopup="menu"
        className="flex w-full items-start gap-2 rounded-[8px] bg-inset px-2.5 py-1.5 text-left transition-colors hover:bg-hover disabled:opacity-60"
      >
        <span className="mt-px flex size-[18px] shrink-0 items-center justify-center rounded-full bg-accent-tint text-[10.5px] font-semibold text-accent-ink tabular-nums">{n}</span>
        <span className="flex min-w-0 flex-1 flex-col">
          <span className="line-clamp-2 text-[12.5px] leading-[18px] whitespace-pre-wrap text-ink [overflow-wrap:anywhere]">
            {marks.map((m) => (
              <span key={m.kind} data-queue-mark={m.kind} aria-hidden className="mr-1.5 inline-flex items-center gap-0.5 align-[-2px] text-ink-3">
                <Icon name={MARK_ICON[m.kind]} size={13} />
                {m.count > 1 && <span className="text-[11px] font-medium tabular-nums">{m.count}</span>}
              </span>
            ))}
            {/* a message with nothing typed (photos, a context) says what it carries */}
            {words || <span className="text-ink-3">{carried}</span>}
          </span>
          {note && (
            <span data-queue-note aria-hidden className="text-[11px] leading-4 text-ink-3">
              {note}
            </span>
          )}
        </span>
        <Icon name="more" size={14} className="mt-0.5 shrink-0 text-ink-3" />
      </button>
      {menu.anchor && <Menu anchor={menu.anchor} onClose={menu.close} align="right" items={items} width={160} />}
    </>
  );
}
