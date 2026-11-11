import { storedValue } from "../../lib/compatibility";
// Beautiful UI's Prompt Bar, wired to a session: multi-line input, / commands, @ files and sessions, image attachments
// (paste, drop, pick; click one to mark it up), files uploaded to the session's machine, send — and stop while the
// agent works and nothing is typed. Quotes, edits and edited images from the chat arrive as draft events (drafts.ts).
import { memo, Suspense, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { useQuickPrompts } from "../../lib/extension-declared";
import { promptCommands, type ComposerCommand as Command } from "../../lib/extension-prompts";
import { LOOP_TEXT, loopHint } from "../../lib/loop-command";
import { ApiError, errorText } from "../../lib/api";
import { composerEntryDrop, dragHasEntry, droppedEntry } from "../../lib/entry-drag";
import { uuid } from "../../lib/format";
import { lazyPage } from "../../lib/lazy-page";
import { enterSends, hasFinePointer, useMedia } from "../../lib/viewport";
import { useModalFocus } from "../../ui/overlay";
import { COMPACT_COMPOSER, growLines, lineCount, offersExpand } from "./composer-grow";
import { INPUT_MAX } from "./dock";
import { imagePayload, MAX_IMAGE, quotePath, releaseAttachments, sendFile } from "../../lib/attachments";
import { activateComposer, addContext, getContexts, newContext, registerComposer, removeContext, useContexts, withContexts, type ComposerHandle } from "../../lib/links";
import { addQuote, applyDraft, getQuotes, onDraftEvent, parseQuotes, removeQuote, replaceNeedsConfirm, sendDraft, setQuotes, splitQuotes, storeDraft, useQuotes, withQuoteBlock, withUnsent, type DraftImage } from "../../lib/drafts";
import { browseTarget, mentionAt, replaceToken, sessionQuote, workspaceContext } from "../../lib/mentions";
import { machineWithHub } from "../../lib/mesh";
import { getState } from "../../lib/store";
import { AttachmentTray } from "../../ui/AttachmentTray";
import { cx, EASE, Spinner, toast } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { ContextChips } from "../link/ContextChips";
import { ForwardBars } from "./ForwardCard";
import { QuoteBars } from "./Quote";
import { MentionMenu, useMentionRows, type MentionRow, type MentionSource } from "./MentionMenu";
import { HistorySearch } from "./HistorySearch";
import { useSendHistory } from "./useSendHistory";
import { isModelCommand, requestModelPicker } from "../../lib/model-switch";
import { MAX_IMAGES } from "../../lib/composer-commands";
import { escapeCancelsEdit, escapeCountsForEdit, upStartsEdit, type EditLastBox } from "../../lib/edit-last-key";
import { t } from "../../i18n";
import type { VoiceOffer } from "./voice/VoiceParts";

// Drawn only while an attached image is edited: its own chunk, fetched by the idle preload or the first edit (lib/lazy-page.ts).
const ImageEditor = lazyPage(() => import("./ImageEditor").then((m) => m.ImageEditor), { idle: true });
/** How long "Not sent yet" stays under the box after Enter was pressed while blocked. */
const NUDGE_MS = 4000;

export interface Attachment {
  id: string;
  name: string;
  mime: string;
  data: string; // base64
  url: string; // object URL for the preview
}

export interface Outgoing {
  text: string;
  images: { mime: string; data: string }[];
  /** An edit of the last message, sent after its rewind (#4605): the old prompt, which Claude may have put back in its
   *  box, cleared exactly before typing. A refusal leaves the edit in the box unsent, not as a failed bubble. */
  edit?: { clearDraft: string };
}

/** The box is editing the last message (#4503; ChatView.editLastMessage). */
export interface EditingLast {
  /** Runs before the edit is sent: the rewind to before the old message. False: it did not happen, the edit stays.
   *  `clearDraft`: the old prompt the send asks the node to clear from Claude's box (#4605). */
  commit: () => Promise<boolean | { clearDraft: string }>;
  /** The edit was cancelled, or the box moved on to other words. */
  cancel: () => void;
}

export const AGENT_COMMANDS: Command[] = [
  { word: t("chat-composer.cmd_continue_word"), desc: t("chat-composer.cmd_continue_desc"), text: t("chat-composer.cmd_continue_text") },
  { word: t("chat-composer.cmd_commit_word"), desc: t("chat-composer.cmd_commit_desc"), text: t("chat-composer.cmd_commit_text") },
  { word: t("chat-composer.cmd_report_word"), desc: t("chat-composer.cmd_report_desc"), text: t("chat-composer.cmd_report_text") },
  { word: "/compact", desc: t("chat-composer.cmd_compact_desc"), text: "/compact" },
  { word: "/context", desc: t("chat-composer.cmd_context_desc"), text: "/context" },
  { word: "/model", desc: t("chat-composer.cmd_model_desc"), text: "/model " },
  { word: "/loop", desc: t("chat-composer.cmd_loop_desc"), text: LOOP_TEXT },
  { word: "/clear", desc: t("chat-composer.cmd_clear_desc"), text: "/clear", confirm: t("chat-composer.cmd_clear_confirm") },
];

function parseSlash(draft: string) {
  const m = /^\/(\S*)$/.exec(draft);
  return m ? m[1].toLowerCase() : null;
}

async function readImage(file: File): Promise<Attachment> {
  return { ...await imagePayload(file), id: uuid(), name: file.name || "image.png", url: URL.createObjectURL(file) };
}

function fromDraftImage(img: DraftImage, id = uuid()): Attachment {
  const bin = atob(img.data);
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
  return { ...img, id, url: URL.createObjectURL(new Blob([bytes], { type: img.mime })) };
}

// memo: a chat renders again for each transcript item, and this box takes none of them (ChatView keeps its props stable)
export const Composer = memo(function Composer({
  draftKey,
  placeholder,
  machineId,
  uploadDest,
  working,
  needsYou,
  disabled,
  commands = AGENT_COMMANDS,
  onSend,
  onStop,
  onEditLastMessage,
  editingLast,
  loadingDraft,
  stopping,
  autoFocus,
  footer,
  suggestion,
  mention,
  onFocusChange,
  blocked,
  voice,
}: {
  draftKey: string;
  placeholder: string;
  machineId?: string;
  /** Where a file sent to machineId lands: "health" keeps it in the health space's reports/ (docs/assistant/health.md "Checkup reports"). */
  uploadDest?: "health";
  working?: boolean;
  /** Waits on a question, plan or approval card: no suggestion pill then (iOS status.isActive). */
  needsYou?: boolean;
  disabled?: boolean;
  commands?: Command[];
  /** False: it did not go and nothing shows it (the chat keeps one that failed as a failed bubble, true), so the words go
   *  back into the box. */
  onSend: (m: Outgoing) => Promise<boolean>;
  onStop?: () => void;
  /** Starts editing the last Claude prompt in this box (↑ in it while empty, Esc twice; #4503, #2945). */
  onEditLastMessage?: () => void;
  /** Set while the box edits the last message: a bar says so, Esc or × cancels, and a send rewinds first. */
  editingLast?: EditingLast;
  /** A rewind is fetching the draft: keep receiving draft events but prevent typing or sending until it finishes. */
  loadingDraft?: boolean;
  /** A stop is on its way: the button shows it and takes no more presses. */
  stopping?: boolean;
  autoFocus?: boolean;
  footer?: React.ReactNode;
  suggestion?: string;
  /** Turns on the @ menu: the chat's files and folder, and other sessions. */
  mention?: MentionSource;
  /** The text box gains / loses focus (on a phone: the keyboard comes up / goes down). */
  onFocusChange?: (focused: boolean) => void;
  /** The main conversation cannot answer yet (docs/assistant/onboarding.md 4.4): the send button says what to do first and opens
   *  it; the box still takes typing and keeps the draft, nothing is sent or queued. */
  blocked?: { label: string; onClick?: () => void; onNudge?: () => void };
  /** Voice chat (docs/web/web.md "Voice"): with an empty box and an idle agent it takes the send button's place, round;
   *  otherwise it is a plain icon before send / stop. "explain": its click says why a call cannot start here. */
  voice?: VoiceOffer;
}) {
  const [draft, setDraft] = useState(() => storedValue(`draft:${draftKey}`) ?? "");
  const [atts, setAtts] = useState<Attachment[]>([]);
  // The suggestion just sent with its arrow: it stays on the hub until the agent starts, and each tap would be a new message.
  const [sentPill, setSentPill] = useState<string | null>(null);
  useEffect(() => {
    if (working) setSentPill(null);
  }, [working]);
  // "Quote" and comments (lib/comments.ts): bars above the input, sent as `> ` blocks in front of the message (lib/drafts.ts)
  const quotes = useQuotes(draftKey).list;
  // files still on their way to the machine, per conversation: the spinner belongs to the one they were picked in
  const [uploads, setUploads] = useState<Record<string, number>>({});
  const uploading = uploads[draftKey] ?? 0;
  const [dismissed, setDismissed] = useState(false);
  const [active, setActive] = useState(0);
  const [dragging, setDragging] = useState(false);
  const [rowBox, setRowBox] = useState<{ top: number; height: number } | null>(null);
  const [caret, setCaret] = useState(0);
  const [focused, setFocused] = useState(false);
  const [mActive, setMActive] = useState(0);
  const [editing, setEditing] = useState<Attachment | null>(null);
  // Enter while blocked (#4480): a line says why nothing went, the button and the card light up; it fades after a while
  const [nudge, setNudge] = useState(0);
  useEffect(() => {
    if (!nudge) return;
    const timer = setTimeout(() => setNudge(0), NUDGE_MS);
    return () => clearTimeout(timer);
  }, [nudge]);
  const input = useRef<HTMLTextAreaElement>(null);
  const lastEscape = useRef<number | null>(null);
  // the edit now, for the draft events' and the send's closures
  const editNow = useRef(editingLast);
  editNow.current = editingLast;
  useEffect(() => { lastEscape.current = null; }, [draftKey, working, disabled, onEditLastMessage]);
  const box = useRef<HTMLDivElement>(null);
  // Phones (#5688): the box grows to a line limit from the visible height, then scrolls and offers Expand, the draft
  // full screen. `grow` is what the last measure found: the limit, a line's height, the padding, and whether it is past it.
  const compact = useMedia(COMPACT_COMPOSER);
  const visible = useVisibleHeight(compact);
  const [grow, setGrow] = useState({ limit: 0, line: 0, pad: 0, over: false });
  const [expanded, setExpanded] = useState(false);
  // the caret to put back in the box when the full editor closes
  const collapsedSel = useRef<[number, number] | null>(null);
  const fileInput = useRef<HTMLInputElement>(null);
  const rowRefs = useRef<(HTMLButtonElement | null)[]>([]);
  // text picked in the link panel waits here (lib/links.ts), and "AI edit" sends through this composer
  const contexts = useContexts(draftKey);
  const sendNow = useRef<(text: string) => Promise<boolean>>(async () => false);
  sendNow.current = async (text) => (disabled || blocked || loadingDraft ? false : onSend({ text, images: [] }));
  const canTake = useRef(!disabled);
  canTake.current = !disabled && !blocked;
  const handle = useMemo<ComposerHandle>(() => ({ key: draftKey, send: (text) => sendNow.current(text), focus: () => input.current?.focus({ preventScroll: true }), ready: () => canTake.current }), [draftKey]);
  useEffect(() => registerComposer(handle), [handle]);

  const quote = quotes.length > 0;
  // the pictures in the box right now and the conversation shown, read by the cleanups and by a send that finishes
  // after the box moved on: their object URLs are freed when they leave the box without being sent
  const attsNow = useRef(atts);
  attsNow.current = atts;
  // pictures being read for this box, counted against MAX_IMAGES before they land
  const reading = useRef(0);
  const roomForImage = () => attsNow.current.length + reading.current < MAX_IMAGES;
  // appends at once to the ref as well, so a second paste or draft image in the same tick sees it
  const appendImage = (a: Attachment) => {
    attsNow.current = [...attsNow.current, a];
    setAtts((cur) => [...cur, a]);
  };
  // one word for the pictures turned away in the same tick (a paste of several, a burst of draft images)
  const refusing = useRef(false);
  const refuseImages = () => {
    if (refusing.current) return;
    refusing.current = true;
    queueMicrotask(() => (refusing.current = false));
    toast(t("chat-composer.too_many_images", { max: MAX_IMAGES }), "error");
  };
  const keyNow = useRef(draftKey);
  keyNow.current = draftKey;
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      releaseAttachments(attsNow.current);
    };
  }, []);

  // a different conversation: its own draft
  useEffect(() => {
    setDraft(storedValue(`draft:${draftKey}`) ?? "");
    releaseAttachments(attsNow.current);
    setAtts([]);
    // not on a touch screen alone: there the focus brings the keyboard up over the conversation just opened
    if (autoFocus && hasFinePointer()) setTimeout(() => input.current?.focus({ preventScroll: true }), 0);
  }, [draftKey, autoFocus]);

  // "Quote" / "Edit" / a queued message pulled back / an edited picture, from the chat (lib/drafts.ts)
  const draftNow = useRef(draft);
  draftNow.current = draft;
  useEffect(
    () =>
      onDraftEvent(draftKey, (e) => {
        // a send from an earlier box of this conversation failed: its words come back even while sending is off
        if (e.type === "unsent") {
          setDraft((cur) => withUnsent(cur, e.text));
          return;
        }
        if (!canTake.current) return;
        if (e.type === "image") {
          if (!roomForImage()) {
            refuseImages();
            return;
          }
          appendImage(fromDraftImage(e.image));
          input.current?.focus({ preventScroll: true });
          return;
        }
        if (e.type === "quote") {
          // the input keeps what is typed; the bar shows the quote, a new one in place of the last
          addQuote(draftKey, { text: e.text.trim(), from: e.from, ts: e.ts, task: e.task }, false);
          requestAnimationFrame(() => input.current?.focus({ preventScroll: true }));
          return;
        }
        // other words in place of an edit's (Edit as new, a rewind's message): no longer an edit
        const startsEdit = e.type === "replace" && Boolean(e.edit);
        if (e.type === "replace" && !startsEdit) editNow.current?.cancel();
        // "Edit" a message sent with a quote: the quote goes back to the bar, the words to the input
        const split = e.type === "replace" ? splitQuotes(e.text) : null;
        const nextQuotes = e.type === "replace" ? (split ? parseQuotes(split.quotes) : []) : null;
        const next = split ? split.rest : applyDraft(draftNow.current, e);
        // "Edit" or a rewind over unsent words, quotes or comments puts the message in at once, with no question first:
        // the notice's Undo brings back what was typed, the quotes on the bar and where the caret was (#2450). An empty
        // bar, or one that would send the same blocks, loses nothing.
        const before = getQuotes(draftKey).list;
        const dropsQuotes = before.length > 0 && withQuoteBlock(before, "") !== withQuoteBlock(nextQuotes ?? [], "");
        const kept =
          nextQuotes && (replaceNeedsConfirm(draftNow.current, next) || dropsQuotes)
            ? { text: draftNow.current, quotes: before, caret: input.current?.selectionStart ?? draftNow.current.length }
            : null;
        if (kept) {
          toast(t("chat-composer.replaced"), "ok", {
            label: t("chat-composer.undo"),
            run: () => {
              if (startsEdit) editNow.current?.cancel();
              setQuotes(draftKey, kept.quotes);
              // the box went away or shows another conversation: the words go back to this one's stored draft
              if (!mounted.current || keyNow.current !== draftKey) {
                storeDraft(draftKey, kept.text);
                return;
              }
              setDraft(kept.text);
              setDismissed(false);
              requestAnimationFrame(() => {
                const el = input.current;
                if (!el) return;
                el.focus({ preventScroll: true });
                el.setSelectionRange(kept.caret, kept.caret);
                setCaret(kept.caret);
              });
            },
          });
        }
        if (nextQuotes) setQuotes(draftKey, nextQuotes);
        setDraft(next);
        setDismissed(false);
        requestAnimationFrame(() => {
          const el = input.current;
          if (!el) return;
          el.focus({ preventScroll: true });
          el.setSelectionRange(next.length, next.length);
          setCaret(next.length);
        });
      }),
    [draftKey],
  );

  // What is typed goes into the conversation's stored draft 250 ms after the last key; one still waiting is written at
  // once when the box goes away or moves to another conversation, and when the page is hidden (closed, reloaded,
  // left), so the last words typed before switching are not lost (#4591).
  const unsaved = useRef<{ key: string; text: string } | null>(null);
  const saveDraft = useCallback(() => {
    const u = unsaved.current;
    if (!u) return;
    unsaved.current = null;
    storeDraft(u.key, u.text);
  }, []);
  // The render that moves the box to another conversation still holds the old words: they are not that one's draft
  // (the box reads its own next, and this runs again if it differs).
  const savedFor = useRef(draftKey);
  useEffect(() => {
    if (savedFor.current !== draftKey) {
      savedFor.current = draftKey;
      return;
    }
    unsaved.current = { key: draftKey, text: draft };
    const t = setTimeout(saveDraft, 250);
    return () => clearTimeout(t);
  }, [draft, draftKey, saveDraft]);
  useEffect(() => {
    addEventListener("pagehide", saveDraft);
    return () => {
      removeEventListener("pagehide", saveDraft);
      // only this conversation's: on a switch the box still holds the old words under the new key for one render
      if (unsaved.current?.key === draftKey) saveDraft();
    };
  }, [draftKey, saveDraft]);

  useLayoutEffect(() => {
    const el = input.current;
    if (!el) return;
    // Measuring collapses the box to 0 for one layout: its parent holds its height meanwhile. Otherwise the chat above
    // grows by the box's height for that layout, the browser clamps the chat's scroll offset to the taller view, and
    // with the box back the chat sits that much above its newest line (#5375). Under the cap the chat's own
    // ResizeObserver brought the end back; at the cap the chat's height no longer changes, so every key left it there.
    const holder = el.parentElement;
    const held = holder?.style.minHeight ?? "";
    if (holder) holder.style.minHeight = `${holder.getBoundingClientRect().height}px`;
    el.style.height = "0px";
    const h = el.scrollHeight;
    // the CSS max-height (INPUT_MAX) caps it: 240px, less when the visible height is small (a keyboard, a phone on
    // its side), so the send button stays on screen; past it the box scrolls (overflow-y-auto: when the keyboard
    // lowers the cap, no new draft comes to say so)
    el.style.height = `${Math.max(h, 24)}px`;
    if (holder) holder.style.minHeight = held;
    if (!compact) return;
    const css = getComputedStyle(el);
    const line = parseFloat(css.lineHeight) || 24;
    const pad = (parseFloat(css.paddingTop) || 0) + (parseFloat(css.paddingBottom) || 0);
    const limit = growLines(visible, line);
    const over = offersExpand(lineCount(h - pad, line), limit);
    setGrow((g) => (g.limit === limit && g.line === line && g.pad === pad && g.over === over ? g : { limit, line, pad, over }));
  }, [draft, compact, visible, grow.over]);
  const showExpand = compact && grow.over && !disabled;
  useEffect(() => {
    if (!compact || disabled) setExpanded(false);
  }, [compact, disabled]);
  // Back from the full editor: the caret where it was there, in the box.
  useEffect(() => {
    if (expanded || !collapsedSel.current) return;
    const el = input.current;
    const [a, b] = collapsedSel.current;
    collapsedSel.current = null;
    if (!el) return;
    el.focus({ preventScroll: true });
    el.setSelectionRange(a, b);
  }, [expanded]);

  const quickPrompts = useQuickPrompts();
  const allCommands = useMemo(() => [...commands, ...promptCommands(quickPrompts)], [commands, quickPrompts]);
  const query = dismissed ? null : parseSlash(draft);
  // A /loop draft gets a line under the box saying what it will do (only where the menu offers /loop: Claude agents).
  const loop = commands.some((c) => c.text === LOOP_TEXT) ? loopHint(draft) : null;
  const rows = useMemo(() => (query === null ? [] : allCommands.filter((c) => c.word.slice(1).toLowerCase().startsWith(query) || c.desc.includes(query))), [query, allCommands]);
  useEffect(() => setActive(0), [query]);
  useLayoutEffect(() => {
    const el = rowRefs.current[active];
    setRowBox(el ? { top: el.offsetTop, height: el.offsetHeight } : null);
  }, [active, rows.length]);

  // @: the token just before the caret (lib/mentions.ts)
  const token = mention && focused && !dismissed && query === null && caret <= draft.length ? mentionAt(draft, caret) : null;
  const { rows: mRows, loading: mLoading } = useMentionRows(token, mention, machineId);
  useEffect(() => setMActive(0), [token?.query]);
  const mOpen = Boolean(token);

  const trackCaret = () => {
    const el = input.current;
    if (el) setCaret(el.selectionStart);
  };

  // Editing the last message (#4503): the words the box got when it began, so an untouched edit counts as the history
  // entry shown (↑ goes on to older messages, ↓ back to an empty box); each of those ends the edit, its quotes with it.
  const editStart = useRef<string | null>(null);
  if (!editingLast) editStart.current = null;
  else if (editStart.current === null) editStart.current = draft;
  const leaveEdit = () => {
    if (!editNow.current) return;
    setQuotes(draftKey, []);
    editNow.current.cancel();
  };
  // ↑ / ↓ recall and Ctrl+R search of what was sent (useSendHistory.ts); desktop only
  const history = useSendHistory({
    draftKey,
    draft,
    setDraft: (text) => {
      leaveEdit();
      setDraft(text);
    },
    setCaret,
    input,
    disabled,
    editing: editStart.current,
  });
  // Esc or × while editing: the box as it was before, empty; nothing was asked of the computer
  const cancelEdit = () => {
    const edit = editNow.current;
    if (!edit) return;
    setDraft("");
    draftNow.current = "";
    unsaved.current = null;
    storeDraft(draftKey, "");
    setQuotes(draftKey, []);
    getContexts(draftKey).forEach((r) => removeContext(draftKey, r.id));
    releaseAttachments(attsNow.current);
    attsNow.current = [];
    setAtts([]);
    edit.cancel();
    requestAnimationFrame(() => input.current?.focus({ preventScroll: true }));
  };
  // what ↑ and Esc Esc may start an edit from (lib/edit-last-key.ts); the page's dialogs are looked up only for those keys
  const editBox = (): EditLastBox => ({
    offered: Boolean(onEditLastMessage),
    busy: Boolean(working || needsYou || disabled || blocked || loadingDraft),
    holds: Boolean(draft || atts.length || uploading || reading.current || contexts.length || quotes.length),
    menu: Boolean(rows.length || mOpen || history.searching || document.querySelector("[role=dialog],[role=menu]")),
  });

  const pickMention = (r: MentionRow) => {
    if (!token) return;
    if (r.kind === "session" || r.kind === "workspace") {
      // a session goes with the message as a quote, not as text (PromptItems.quote); a workspace as a context the hub fills in
      if (r.kind === "session") {
        const q = sessionQuote(r.session);
        addContext(draftKey, newContext("context", q.title, q.url, q.text));
      } else {
        const w = workspaceContext(r.workspace, (id) => machineWithHub(id, getState().machines));
        addContext(draftKey, { ...newContext("workspace", w.title, w.url, w.text), attrs: w.attrs });
      }
      const out = replaceToken(draft, token, "");
      setDraft(out.text);
      setCaret(out.caret);
      requestAnimationFrame(() => input.current?.setSelectionRange(out.caret, out.caret));
      return;
    }
    const out = replaceToken(draft, token, r.insert);
    setDraft(out.text);
    setCaret(out.caret);
    requestAnimationFrame(() => {
      input.current?.focus({ preventScroll: true });
      input.current?.setSelectionRange(out.caret, out.caret);
    });
  };

  const typeAt = () => {
    const el = input.current;
    const at = el ? Math.min(el.selectionStart, draft.length) : draft.length;
    const before = draft.slice(0, at);
    const ins = before && !/\s$/.test(before) ? " @" : "@";
    const next = before + ins + draft.slice(at);
    setDraft(next);
    setDismissed(false);
    setCaret(at + ins.length);
    requestAnimationFrame(() => {
      input.current?.focus({ preventScroll: true });
      input.current?.setSelectionRange(at + ins.length, at + ins.length);
    });
  };

  // A Files panel entry dropped on the box: `@path ` at the caret, as the @ menu types it
  const dropEntry = (e: React.DragEvent): boolean => {
    const entry = droppedEntry(e.dataTransfer);
    if (!entry) return false;
    if (disabled) return true;
    const drop = composerEntryDrop(entry, { mid: machineId, cwd: mention?.cwd });
    if (drop.action === "refuse") {
      toast(drop.toast);
      return true;
    }
    const el = input.current;
    const at = el ? Math.min(el.selectionStart, draft.length) : draft.length;
    const before = draft.slice(0, at);
    const lead = before && !/\s$/.test(before) ? " " : "";
    setDraft(before + lead + drop.text + draft.slice(at));
    const caret = at + lead.length + drop.text.length;
    setCaret(caret);
    requestAnimationFrame(() => {
      input.current?.focus();
      input.current?.setSelectionRange(caret, caret);
    });
    return true;
  };

  const addFiles = async (files: FileList | File[]) => {
    if (disabled) return;
    // at most MAX_IMAGES pictures a message, as on the phone: the ones past it are turned away with a word
    // (the count takes in pictures still being read by an earlier paste or drop)
    let refused = false;
    for (const f of Array.from(files)) {
      if (f.type.startsWith("image/")) {
        if (!roomForImage()) {
          refused = true;
          continue;
        }
        if (f.size > MAX_IMAGE) {
          toast(t("chat-composer.image_too_large", { name: f.name }), "error");
          continue;
        }
        reading.current++;
        let a: Attachment;
        try {
          a = await readImage(f);
        } finally {
          reading.current--;
        }
        // read after the box closed or moved to another conversation: freed, not put into that one
        if (mounted.current && keyNow.current === draftKey) appendImage(a);
        else releaseAttachments([a]);
      } else if (machineId) {
        void upload(f);
      } else {
        toast(t("chat-composer.images_only"), "error");
      }
    }
    if (refused) refuseImages();
  };

  // a file goes to the session's machine; its path lands in the input (docs/protocol/protocol.md "Sending files")
  const upload = async (f: File) => {
    const count = (key: string, by: number) =>
      setUploads((u) => {
        const { [key]: n = 0, ...rest } = u;
        return n + by > 0 ? { ...rest, [key]: n + by } : rest;
      });
    count(draftKey, 1);
    try {
      const path = await sendFile(machineId!, f, { dest: uploadDest });
      const p = quotePath(path);
      // finished after the box closed or moved to another conversation: into the draft it was picked for (its open
      // box, else the stored draft), not into the one shown now
      if (!mounted.current || keyNow.current !== draftKey) sendDraft(draftKey, { type: "append", text: p });
      else {
        const el = input.current;
        setDraft((d) => {
          const at = el && document.activeElement === el ? Math.min(el.selectionStart, d.length) : d.length;
          const before = d.slice(0, at),
            after = d.slice(at);
          return `${before}${before && !before.endsWith(" ") ? " " : ""}${p}${after.startsWith(" ") || !after ? "" : " "}${after}`;
        });
      }
      toast(t("chat-composer.uploaded", { path }), "ok");
    } catch (e) {
      toast(e instanceof ApiError && e.code === "too_large" ? t("chat-composer.file_too_large") : t("chat-composer.upload_failed", { error: errorText(e) }), "error");
    } finally {
      if (mounted.current) count(draftKey, -1);
    }
  };

  const canSend = (draft.trim().length > 0 || atts.length > 0 || contexts.length > 0 || quote) && !disabled && !loadingDraft;
  // Claude's suggested next message (iOS SuggestionPill): while the box is empty and the agent waits for a message
  // (not working, not on a card); a tap fills it
  // in, "Send now" sends it as it is (the node drops it once the agent works again)
  const pill = suggestion && suggestion !== sentPill && !editingLast && !blocked && !draft && !atts.length && !contexts.length && !quote && !working && !needsYou && !disabled ? suggestion : null;
  const fillSuggestion = (text: string) => {
    setDraft(text);
    setDismissed(true);
    setCaret(text.length);
    requestAnimationFrame(() => {
      const el = input.current;
      if (!el) return;
      el.focus({ preventScroll: true });
      el.setSelectionRange(text.length, text.length);
    });
  };
  const showStop = working && !draft.trim() && !atts.length && !contexts.length && !quote && onStop;
  // an empty box and an idle agent: voice takes the send button's place (it would be off anyway), as on iOS
  const roundVoice = voice?.mode === "start" && !canSend && !showStop && !blocked && !disabled;

  const submit = async (text = draft) => {
    const refs = getContexts(draftKey);
    const q = getQuotes(draftKey).list;
    if (disabled || blocked || loadingDraft || (!text.trim() && !atts.length && !refs.length && !q.length)) return;
    // An edit of the last message: the rewind to before the old one first, the box kept as it is (read-only meanwhile).
    // Refused or not possible: nothing changed on the computer, and the edit stays in the box (ChatView says why).
    const edit = editNow.current;
    let committed: Outgoing["edit"];
    if (edit) {
      const done = await edit.commit();
      if (!done) return;
      if (typeof done === "object") committed = done;
      // the box went away or shows another conversation: the words wait in this one's stored draft
      if (!mounted.current || keyNow.current !== draftKey) return;
    }
    // The message shows in the chat at once (a sending bubble), so the box empties at once too: waiting for the hub to
    // hand it to the agent (seconds when it starts up) left the same words in both places. A send that never went
    // (onSend false) puts them back; one that failed stays in the chat as a failed bubble.
    const sent = atts;
    setDraft("");
    draftNow.current = "";
    unsaved.current = null;
    storeDraft(draftKey, "");
    setAtts([]);
    refs.forEach((r) => removeContext(draftKey, r.id));
    setQuotes(draftKey, []);
    // Ready for the next message now, not when the hub answers (the chat sends them in order). Back to the box only with
    // a precise pointer and if the focus has not gone elsewhere: on a touch screen it would raise the keyboard and fold
    // the waiting cards (dock.ts).
    const at = document.activeElement;
    if (hasFinePointer() && (!at || at === document.body || box.current?.contains(at))) input.current?.focus({ preventScroll: true });
    // picked contexts ride in front as <persona-context> tags (docs/protocol/protocol.md "Tags in messages"), the quote as `> ` lines
    const ok = await onSend({ text: withContexts(refs, withQuoteBlock(q, text), sent.length > 0), images: sent.map((a) => ({ mime: a.mime, data: a.data })), ...(committed ? { edit: committed } : {}) });
    if (ok) {
      history.record(text);
      releaseAttachments(sent);
    } else {
      // the box closed or shows another conversation now: the words go back to this conversation's draft (its open
      // box, or the stored one), not into the box; the pictures are not put back there, only freed
      if (mounted.current && keyNow.current === draftKey) {
        setDraft((cur) => withUnsent(cur, text));
        setAtts((cur) => [...sent, ...cur]);
      } else {
        if (text) sendDraft(draftKey, { type: "unsent", text });
        releaseAttachments(sent);
      }
      refs.forEach((r) => addContext(draftKey, r));
      // the quotes back above this conversation's box (kept for it when the box shows another one or closed)
      if (q.length && !getQuotes(draftKey).list.length) setQuotes(draftKey, q);
    }
  };

  const pick = (c: Command) => {
    if (c.confirm && !confirm(c.confirm)) return;
    setDraft(c.text);
    setDismissed(true);
    input.current?.focus({ preventScroll: true });
  };

  return (
    <div
      ref={box}
      className="relative"
      inert={loadingDraft || undefined}
      onDragOver={(e) => {
        if (e.dataTransfer.types.includes("Files") || dragHasEntry(e.dataTransfer)) {
          e.preventDefault();
          setDragging(true);
        }
      }}
      onDragLeave={(e) => {
        if (!e.currentTarget.contains(e.relatedTarget as Node)) setDragging(false);
      }}
      onDrop={(e) => {
        e.preventDefault();
        setDragging(false);
        if (dropEntry(e)) return;
        void addFiles(e.dataTransfer.files);
      }}
    >
      {rows.length > 0 && (
        <div className="absolute inset-x-0 bottom-full z-10 mb-2 rounded-[10px] bg-surface p-1 shadow-raised" style={{ animation: `pop-in 180ms ${EASE} both`, transformOrigin: "bottom center" }}>
          <div className="relative">
            <span aria-hidden className="pointer-events-none absolute inset-x-0 rounded-[6px] bg-hover" style={{ top: rowBox?.top ?? 0, height: rowBox?.height ?? 0, opacity: rowBox ? 1 : 0, transition: `top 200ms ${EASE}, height 200ms ${EASE}` }} />
            {rows.map((c, i) => (
              <button
                key={c.key ?? c.word}
                ref={(el) => {
                  rowRefs.current[i] = el;
                }}
                type="button"
                onMouseDown={(e) => e.preventDefault()}
                onMouseEnter={() => setActive(i)}
                onClick={() => pick(c)}
                className="relative z-10 flex h-9 w-full items-center gap-2.5 rounded-[6px] px-2 text-left"
              >
                <span className={cx("shrink-0 font-mono text-[12.5px] font-medium text-ink", c.key && "max-w-[45%] truncate")}>{c.word}</span>
                <span className="min-w-0 flex-1 truncate text-[12px] text-ink-3">{c.desc}</span>
                {c.text.startsWith("/") ? null : <span className="max-w-[45%] shrink truncate text-[11px] text-ink-3">{t("chat-composer.command_text", { text: c.text })}</span>}
              </button>
            ))}
          </div>
          <div className="mt-1 border-t border-line px-2 pt-1.5 pb-1 text-[11px] text-ink-3">{t("chat-composer.keys_hint")}</div>
        </div>
      )}

      {history.searching && <HistorySearch onPick={history.pick} onClose={history.closeSearch} />}

      {mOpen && (
        <MentionMenu rows={mRows} active={Math.min(mActive, Math.max(mRows.length - 1, 0))} loading={mLoading} browsing={Boolean(token && browseTarget(token.query, mention?.cwd))} onHover={setMActive} onPick={pickMention} />
      )}

      {pill && <SuggestionPill text={pill} onFill={() => fillSuggestion(pill)} onSend={() => { setSentPill(pill); void submit(pill); }} />}

      <div
        className={cx(
          "relative flex flex-col gap-1.5 rounded-[14px] border bg-surface p-1.5 shadow-card transition-[border-color,box-shadow] duration-150 focus-within:border-line-strong",
          dragging ? "border-accent shadow-[0_0_0_3px_var(--accent-tint)]" : "border-line",
        )}
      >
        {editingLast && (
          <div role="status" data-testid="composer-editing" className="flex min-w-0 items-center gap-1.5 pt-0.5 pr-0.5 pl-2 text-[11.5px] whitespace-nowrap" style={{ animation: "fade-in 160ms ease-out both" }}>
            <Icon name="pencil" size={12} stroke={2.2} className="shrink-0 text-accent-ink" />
            <span className="shrink-0 font-medium text-ink-2">{t("rewind.editing")}</span>
            {hasFinePointer() && <span className="min-w-0 truncate text-ink-3">{t("rewind.editing_hint")}</span>}
            <span className="flex-1" />
            <button
              type="button"
              aria-label={t("rewind.cancel_editing")}
              title={t("rewind.cancel_editing")}
              disabled={loadingDraft}
              onClick={cancelEdit}
              className="touch-hit flex size-6 shrink-0 items-center justify-center rounded-full text-ink-3 transition-colors hover:bg-hover-2 hover:text-ink-2"
            >
              <Icon name="x" size={12} stroke={2.4} />
            </button>
          </div>
        )}
        <ContextChips draftKey={draftKey} />
        <ForwardBars draftKey={draftKey} onRemove={() => input.current?.focus()} />
        {quote && (
          <QuoteBars
            draftKey={draftKey}
            quotes={quotes}
            onRemove={(id) => {
              removeQuote(draftKey, id);
              input.current?.focus();
            }}
          />
        )}
        {atts.length > 0 && (
          <div className="px-0.5 pt-0.5">
            <AttachmentTray attachments={atts} onOpen={(id) => setEditing(atts.find((a) => a.id === id) ?? null)} onRemove={(id) => {
              const att = atts.find((a) => a.id === id);
              if (att) URL.revokeObjectURL(att.url);
              setAtts((cur) => cur.filter((a) => a.id !== id));
            }} />
          </div>
        )}
        {/* a field that turns disabled loses focus with no blur event: the keyboard is down, say so */}
        <DisabledBlur disabled={Boolean(disabled)} focused={focused} onBlur={() => { setFocused(false); onFocusChange?.(false); }} />
        <div className="relative">
        <textarea
          ref={input}
          rows={1}
          value={draft}
          disabled={disabled}
          readOnly={loadingDraft}
          aria-busy={loadingDraft || undefined}
          onFocus={() => {
            activateComposer(handle);
            setFocused(true);
            onFocusChange?.(true);
          }}
          onBlur={() => {
            lastEscape.current = null;
            setFocused(false);
            onFocusChange?.(false);
          }}
          onChange={(e) => {
            setDraft(e.target.value);
            setDismissed(false);
            setCaret(e.target.selectionStart);
          }}
          onSelect={trackCaret}
          onClick={trackCaret}
          onPaste={(e) => {
            const files = Array.from(e.clipboardData.files);
            if (files.length) {
              e.preventDefault();
              void addFiles(files);
            }
          }}
          onKeyDown={(e) => {
            const previousEscape = lastEscape.current;
            lastEscape.current = null;
            if (loadingDraft) {
              e.preventDefault();
              e.stopPropagation();
              return;
            }
            if (e.nativeEvent.isComposing || e.keyCode === 229) {
              // the IME's own Escape (cancelling a candidate) must not reach ChatView's interrupt on the window
              if (e.key === "Escape") e.stopPropagation();
              return;
            }
            // `/model` alone opens the model popover instead of sending (where the agent has one)
            if (enterSends(e, hasFinePointer()) && isModelCommand(draft) && requestModelPicker(draftKey)) {
              e.preventDefault();
              setDraft("");
              return;
            }
            // ↑ in an empty box edits the last message where Edit last message is on offer (#4503); else the history's
            if (e.key === "ArrowUp" && onEditLastMessage && upStartsEdit(e, editBox())) {
              e.preventDefault();
              onEditLastMessage();
              return;
            }
            if (history.onKeyDown(e)) return;
            if (mOpen) {
              if ((e.key === "ArrowDown" || e.key === "ArrowUp") && mRows.length) {
                e.preventDefault();
                setMActive((a) => (a + (e.key === "ArrowDown" ? 1 : mRows.length - 1)) % mRows.length);
                return;
              }
              if ((e.key === "Enter" || e.key === "Tab") && mRows.length) {
                e.preventDefault();
                pickMention(mRows[Math.min(mActive, mRows.length - 1)]);
                return;
              }
              if (e.key === "Escape") {
                e.preventDefault();
                e.stopPropagation();
                setDismissed(true);
                return;
              }
            }
            if (rows.length) {
              if (e.key === "ArrowDown" || e.key === "ArrowUp") {
                e.preventDefault();
                setActive((a) => (a + (e.key === "ArrowDown" ? 1 : rows.length - 1)) % rows.length);
                return;
              }
              if (e.key === "Enter" || e.key === "Tab") {
                e.preventDefault();
                pick(rows[active]);
                return;
              }
              if (e.key === "Escape") {
                // closing the menu only: keep it from ChatView's interrupt on the window
                e.preventDefault();
                e.stopPropagation();
                setDismissed(true);
                return;
              }
            }
            // Esc while editing cancels it; kept from ChatView's interrupt on the window
            if (escapeCancelsEdit(e, Boolean(editingLast))) {
              e.preventDefault();
              e.stopPropagation();
              cancelEdit();
              return;
            }
            if (e.key === "Escape" && onEditLastMessage && escapeCountsForEdit(e, editBox())) {
              e.preventDefault();
              e.stopPropagation();
              const now = performance.now();
              if (previousEscape !== null && now - previousEscape <= 500) onEditLastMessage();
              else lastEscape.current = now;
              return;
            }
            if (e.key === "Tab" && pill) {
              e.preventDefault();
              fillSuggestion(pill);
              return;
            }
            // a touch keyboard's return is a new line there; the send button sends (a hardware keyboard with a
            // trackpad, an iPad's, sends on Enter)
            if (enterSends(e, hasFinePointer())) {
              e.preventDefault();
              // any Enter while blocked says why (also an empty box or attachments only): submit() would do nothing
              if (blocked) {
                setNudge((n) => n + 1);
                // held down, the key repeats: one fresh ask, not one per repeat
                if (!e.repeat) blocked.onNudge?.();
              } else void submit();
            }
          }}
          placeholder={pill && hasFinePointer() ? t("chat-composer.suggestion", { suggestion: pill }) : placeholder}
          aria-label={t("chat-composer.message")}
          title={hasFinePointer() ? (onEditLastMessage ? t("composer.history_hint_edit") : t("composer.history_hint")) : undefined}
          style={{ maxHeight: compact && grow.line ? `${grow.limit * grow.line + grow.pad}px` : INPUT_MAX }}
          className={cx("min-h-6 w-full resize-none overflow-y-auto bg-transparent px-2 pt-1.5 text-[14px] leading-[21px] text-ink outline-none [@media(pointer:coarse)]:text-[16px] [@media(pointer:coarse)]:leading-[24px] [overflow-wrap:anywhere] placeholder:text-ink-3 disabled:opacity-50", showExpand && "pr-8")}
        />
        {showExpand && (
          <button
            type="button"
            data-testid="composer-expand"
            aria-label={t("chat-composer.expand")}
            title={t("chat-composer.expand")}
            onClick={() => setExpanded(true)}
            className="touch-hit absolute top-0.5 right-0 flex size-7 items-center justify-center rounded-[8px] text-ink-3 transition-colors hover:bg-hover hover:text-ink"
          >
            <Icon name="expand" size={14} stroke={2} />
          </button>
        )}
        </div>
        {loop && (
          <div role="status" className={cx("flex items-center gap-1.5 px-2 text-[11.5px]", loop.missing ? "text-ink-3" : "text-ink-2")}>
            <Icon name="loop" size={12} stroke={2.2} className="shrink-0" />
            <span className="min-w-0 truncate">{loop.text}</span>
          </div>
        )}
        {nudge > 0 && blocked && (
          <div role="status" data-testid="composer-nudge" className="flex items-center gap-1.5 px-2 text-[11.5px] text-ink-2" style={{ animation: "fade-in 160ms ease-out both" }}>
            <Icon name="info" size={12} stroke={2.2} className="shrink-0 text-accent-ink" />
            <span className="min-w-0 truncate">{t("chat-composer.blocked_hint", { label: blocked.label })}</span>
          </div>
        )}
        <div className="flex items-center gap-1 [@media(pointer:coarse)]:gap-2">
          <input ref={fileInput} type="file" multiple hidden onChange={(e) => e.target.files && void addFiles(e.target.files).finally(() => (e.target.value = ""))} />
          <button
            type="button"
            aria-label={machineId ? t("chat-composer.add_image_or_file") : t("chat-composer.add_image")}
            title={machineId ? t("chat-composer.add_hint") : t("chat-composer.add_image")}
            onClick={() => fileInput.current?.click()}
            disabled={disabled}
            className="touch-hit flex size-7 items-center justify-center rounded-[8px] text-ink-3 transition-[background-color,color,transform] duration-150 hover:bg-hover hover:text-ink active:scale-[0.94]"
          >
            {uploading ? <Spinner /> : <Icon name="plus" size={16} stroke={2} />}
          </button>
          {mention && (
            <button
              type="button"
              aria-label={t("chat-composer.mention")}
              title={t("chat-composer.mention_hint")}
              disabled={disabled}
              onClick={typeAt}
              className="touch-hit flex size-7 items-center justify-center rounded-[8px] text-ink-3 transition-colors hover:bg-hover hover:text-ink"
            >
              <Icon name="at" size={15} />
            </button>
          )}
          {allCommands.length > 0 && (
          <button
            type="button"
            aria-label={t("chat-composer.commands")}
            disabled={disabled}
            title={t("chat-composer.commands")}
            onClick={() => {
              setDraft("/");
              setDismissed(false);
              input.current?.focus({ preventScroll: true });
            }}
            className="touch-hit flex h-7 items-center justify-center rounded-[8px] px-1.5 font-mono text-[13px] text-ink-3 transition-colors hover:bg-hover hover:text-ink"
          >
            /
          </button>
          )}
          {/* not overflow-hidden: the chips' tap areas reach past this row (touch-hit); the footer truncates its own text */}
          <div className="min-w-0 flex-1 px-1 text-[11.5px] whitespace-nowrap text-ink-3">{footer}</div>
          {voice && !roundVoice && (
            <button
              type="button"
              data-testid="composer-voice"
              data-voice-mode={voice.mode}
              aria-label={t("voice.voice_chat")}
              title={voice.label}
              onClick={voice.onClick}
              className={cx("touch-hit flex size-7 shrink-0 items-center justify-center rounded-[8px] transition-colors hover:bg-hover hover:text-ink", voice.mode === "start" ? "text-ink-2" : "text-ink-3")}
            >
              <Icon name="voice" size={15} stroke={2} />
            </button>
          )}
          {roundVoice ? (
            <button
              type="button"
              data-testid="composer-voice"
              data-voice-mode="start"
              aria-label={t("voice.voice_chat")}
              title={voice!.label}
              onClick={voice!.onClick}
              className="touch-hit flex size-7 shrink-0 items-center justify-center rounded-[8px] bg-ink text-surface transition-[opacity,transform] duration-200 hover:opacity-90 active:scale-[0.94]"
              style={{ animation: `pop-in 200ms ${EASE} both` }}
            >
              <Icon name="voice" size={15} stroke={2.2} />
            </button>
          ) : showStop ? (
            <button
              type="button"
              aria-label={stopping ? t("chat-composer.stopping") : t("chat-composer.interrupt")}
              title={stopping ? t("chat-composer.stopping") : t("chat-composer.interrupt_hint")}
              onClick={onStop}
              disabled={stopping}
              className="touch-hit flex size-7 shrink-0 items-center justify-center rounded-[8px] bg-danger-fill text-white transition-transform duration-150 enabled:active:scale-[0.94] disabled:opacity-60"
              style={{ animation: `pop-in 200ms ${EASE} both` }}
            >
              {stopping ? <Spinner size={12} /> : <Icon name="stop" size={12} />}
            </button>
          ) : blocked ? (
            <button
              key={nudge}
              type="button"
              data-testid="composer-blocked"
              onClick={blocked.onClick}
              disabled={!blocked.onClick}
              className="touch-hit flex h-7 max-w-[60%] shrink-0 items-center gap-1 rounded-[8px] bg-ink px-2.5 text-[12px] font-medium whitespace-nowrap text-surface transition-[opacity,transform] duration-150 enabled:hover:opacity-90 enabled:active:scale-[0.96] disabled:opacity-45"
              style={{ animation: nudge ? `nudge-x 420ms ease-out both` : `pop-in 200ms ${EASE} both` }}
            >
              <span className="truncate">{blocked.label}</span>
              <Icon name="chevronRight" size={11} stroke={2.4} className="shrink-0" />
            </button>
          ) : (
            <button
              type="button"
              aria-label={t("chat-composer.send")}
              title={t("chat-composer.send_hint")}
              disabled={!canSend}
              onClick={() => void submit()}
              className="touch-hit flex size-7 shrink-0 items-center justify-center rounded-[8px] transition-[background-color,color,transform] duration-200 enabled:active:scale-[0.94]"
              style={{ background: canSend ? "var(--ink)" : "var(--line-strong)", color: canSend ? "var(--surface)" : "var(--ink-2)" }}
            >
              <Icon name="up" size={16} stroke={2.4} />
            </button>
          )}
        </div>
      </div>
      {expanded && (
        <ExpandedComposer
          value={draft}
          placeholder={placeholder}
          selection={[input.current?.selectionStart ?? draft.length, input.current?.selectionEnd ?? draft.length]}
          onChange={(v) => {
            setDraft(v);
            setDismissed(false);
          }}
          onCollapse={(sel) => {
            collapsedSel.current = sel;
            setExpanded(false);
          }}
          canSend={Boolean(canSend) && !blocked}
          onSend={(sel) => {
            collapsedSel.current = sel;
            setExpanded(false);
            void submit();
          }}
        />
      )}
      {editing && (
        <Suspense fallback={null}>
          <ImageEditor
            src={editing.url}
            onCancel={() => setEditing(null)}
            onDone={(img) => {
              const old = editing;
              setEditing(null);
              setAtts((cur) => cur.map((a) => (a.id === old.id ? fromDraftImage(img, a.id) : a)));
              URL.revokeObjectURL(old.url);
            }}
          />
        </Suspense>
      )}
    </div>
  );
});

/** The visible height (above the keyboard), followed while `on`: the grow limit comes from it. */
function useVisibleHeight(on: boolean): number {
  const read = () => (typeof window === "undefined" ? 0 : Math.round(window.visualViewport?.height ?? window.innerHeight));
  const [h, setH] = useState(read);
  useEffect(() => {
    if (!on) return;
    const vv = window.visualViewport;
    const update = () => setH(read());
    update();
    vv?.addEventListener("resize", update);
    addEventListener("resize", update);
    return () => {
      vv?.removeEventListener("resize", update);
      removeEventListener("resize", update);
    };
  }, [on]);
  return h;
}

/** The draft full screen on a phone (#5688): collapse at the top right, the text filling the visible height, send at the
 *  bottom right. It edits the box's own draft; closing hands back the caret so the box picks up where this left off. */
function ExpandedComposer({ value, placeholder, selection, onChange, onCollapse, canSend, onSend }: {
  value: string;
  placeholder: string;
  selection: [number, number];
  onChange: (v: string) => void;
  onCollapse: (sel: [number, number]) => void;
  canSend: boolean;
  onSend: (sel: [number, number]) => void;
}) {
  const panel = useRef<HTMLDivElement>(null);
  const field = useRef<HTMLTextAreaElement>(null);
  const start = useRef(selection);
  useModalFocus(panel);
  useLayoutEffect(() => {
    const el = field.current;
    if (!el) return;
    el.focus({ preventScroll: true });
    el.setSelectionRange(start.current[0], start.current[1]);
  }, []);
  const sel = (): [number, number] => [field.current?.selectionStart ?? value.length, field.current?.selectionEnd ?? value.length];
  return (
    <div
      ref={panel}
      role="dialog"
      aria-modal="true"
      aria-label={t("chat-composer.message")}
      tabIndex={-1}
      data-testid="composer-expanded"
      className="fixed inset-x-0 z-50 flex flex-col bg-surface outline-none"
      style={{ top: "var(--kb-top)", height: "var(--kb-h)", animation: `fade-in 160ms ease-out both` }}
      onKeyDown={(e) => {
        if (e.key === "Escape" && !e.nativeEvent.isComposing) {
          e.preventDefault();
          e.stopPropagation();
          onCollapse(sel());
        }
      }}
    >
      <div className="flex justify-end px-2 pt-[max(env(safe-area-inset-top),8px)]">
        <button
          type="button"
          data-testid="composer-collapse"
          aria-label={t("chat-composer.collapse")}
          title={t("chat-composer.collapse")}
          onClick={() => onCollapse(sel())}
          className="touch-hit flex size-9 items-center justify-center rounded-[10px] text-ink-2 transition-colors hover:bg-hover hover:text-ink"
        >
          <Icon name="collapse" size={18} stroke={2} />
        </button>
      </div>
      <textarea
        ref={field}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        aria-label={t("chat-composer.message")}
        className="min-h-0 w-full flex-1 resize-none bg-transparent px-5 py-1 text-[16px] leading-[24px] text-ink outline-none [overflow-wrap:anywhere] placeholder:text-ink-3"
      />
      <div className="flex justify-end px-3 pt-2 pb-[max(env(safe-area-inset-bottom),12px)]">
        <button
          type="button"
          data-testid="composer-expanded-send"
          aria-label={t("chat-composer.send")}
          disabled={!canSend}
          onClick={() => onSend(sel())}
          className="touch-hit flex size-9 shrink-0 items-center justify-center rounded-full transition-[background-color,color,transform] duration-200 enabled:active:scale-[0.94]"
          style={{ background: canSend ? "var(--ink)" : "var(--line-strong)", color: canSend ? "var(--surface)" : "var(--ink-2)" }}
        >
          <Icon name="up" size={18} stroke={2.4} />
        </button>
      </div>
    </div>
  );
}

/** Claude's suggestion over the box (iOS SuggestionPill): the row fills it in, the arrow sends it as it is. */
function SuggestionPill({ text, onFill, onSend }: { text: string; onFill: () => void; onSend: () => void }) {
  return (
    <div data-testid="composer-suggestion" className="mb-2 flex items-center gap-1 rounded-full bg-surface p-1 pl-1 shadow-card" style={{ animation: `fade-up 200ms ${EASE} both` }}>
      <button
        type="button"
        onMouseDown={(e) => e.preventDefault()}
        onClick={onFill}
        aria-label={t("chat-composer.suggestion_label", { suggestion: text })}
        title={hasFinePointer() ? t("chat-composer.suggestion_use_hint") : undefined}
        className="touch-hit flex min-w-0 flex-1 items-center gap-2 rounded-full py-1 pr-1 pl-2.5 text-left transition-colors hover:bg-hover"
      >
        <Icon name="sparkles" size={13} stroke={2} className="shrink-0 text-accent" />
        <span className="min-w-0 flex-1 truncate text-[12.5px] text-ink-2">{text}</span>
        <span className="shrink-0 whitespace-nowrap text-[12px] font-medium text-accent">{t("chat-composer.suggestion_use")}</span>
      </button>
      <button
        type="button"
        onMouseDown={(e) => e.preventDefault()}
        onClick={onSend}
        aria-label={t("chat-composer.suggestion_send")}
        title={t("chat-composer.suggestion_send")}
        className="touch-hit flex size-7 shrink-0 items-center justify-center rounded-full text-ink-3 transition-[background-color,color,transform] duration-150 hover:bg-hover hover:text-ink active:scale-[0.94]"
      >
        <Icon name="send" size={14} stroke={2} />
      </button>
    </div>
  );
}

/** Reports the blur a focused field misses when it is disabled (browsers send none). */
function DisabledBlur({ disabled, focused, onBlur }: { disabled: boolean; focused: boolean; onBlur: () => void }) {
  const blur = useRef(onBlur);
  blur.current = onBlur;
  useEffect(() => {
    if (disabled && focused) blur.current();
  }, [disabled, focused]);
  return null;
}
