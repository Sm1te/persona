// The assistant's cards (docs/assistant/assistant-views.md), drawn with the extensions' components (views/extensions/ViewBody.tsx):
// a ```persona-view block in a reply (its key `<item id>/<n>`, lib/assistant-blocks.ts), a live card in the feed
// (view_show / view_update, drawn again in place at each version), and the older ```persona-card (drawn, nothing acts).
//
// What a press does is the card's: reply sends the declared text back (once per card, from any device: the hub keeps
// the answer, lib/assistant-views.ts), open opens inside the app, push / pop move through the card's named pages. A
// reply is only offered once the hub has the card (a live one, or a block it registered from a trusted reply) and says
// it takes answers (feature assistant_views); while a reply is still being written the card is drawn, nothing sends.
// An html component runs only where the web's runtime may (lib/assistant-html.ts, ui/HtmlCard.tsx, #4215): otherwise
// the card shows its plain-text fallback.
import { createContext, lazy, Suspense, useContext, useMemo, useState } from "react";
import { assistantView, cardText } from "../lib/assistant-view";
import { blockKey, blockOf, cardKnown, checkCard, openTarget, type OpenTarget } from "../lib/assistant-blocks";
import { hasHtml, HTML_BRIDGE_FEATURE, WEB_HTML_RUNTIME_VERIFIED } from "../lib/assistant-html";
import { conversationCards, draftKey, submitAction, updateDraft, useCards, type CardDraft } from "../lib/assistant-views";
import type { AssistantView, ViewAction } from "../lib/extension-wire";
import { fieldValues, numberProblem, readView, type UIAction, type UIItem, type UIView } from "../lib/extension-ui";
import { openLink } from "../lib/links";
import { useLinkManager } from "../lib/manager-choice";
import { openPanel } from "../lib/panel";
import { openTask } from "../lib/handoff";
import { navigate } from "../lib/router";
import { hubHas, useStore } from "../lib/store";
import { INERT, ViewSections } from "../views/extensions/view-sections-lazy";
import { t } from "../i18n";
import { Button, cx, Dialog, toast } from "./atoms";
import { Icon } from "./icons";

/** The fenced blocks drawn as cards. */
export const CARD_LANGUAGES = ["persona-view", "persona-card"];

/** The hub feature that says card answers, live cards and registered blocks are there (docs/assistant/assistant-views.md). */
export const VIEWS_FEATURE = "assistant_views";

/** The assistant conversation the cards drawn below belong to (none: an agent's chat, where nothing acts). */
export const CardConversation = createContext<string>("");
/** The text item a block is drawn from (none while it streams: drawn, nothing acts). */
export const CardItem = createContext<{ id: string; text: string } | null>(null);

/** How deep push goes (docs/assistant/assistant-views.md "Actions and multiple pages"). */
const STACK_MOST = 4;

/** Whether html components run here: WEB_HTML_RUNTIME_VERIFIED (dev pages and tests may set it for themselves). */
export const HtmlRuntime = createContext<boolean>(WEB_HTML_RUNTIME_VERIFIED);

const HtmlCard = lazy(() => import("./HtmlCard"));

/** A card's html components run when the runtime may, the hub has the bridge (persona_view and html_views on), and the
 *  card is one the hub keeps (a conversation and a key). Otherwise the whole card is its fallback. */
function useHtmlRuns(keyed: boolean): boolean {
  const verified = useContext(HtmlRuntime);
  const hubAllows = useStore((s) => hubHas(s.features, HTML_BRIDGE_FEATURE));
  return verified && hubAllows && keyed;
}

export function AssistantCard({ lang, code, fallback }: { lang: string; code: string; fallback: React.ReactNode }) {
  const conversation = useContext(CardConversation);
  const item = useContext(CardItem);
  const block = lang === "persona-card" || !item ? undefined : blockOf(item.text, code);
  const htmlRuns = useHtmlRuns(!!conversation && !!block && !!item);
  if (lang === "persona-card") return <LegacyCard lang={lang} code={code} fallback={fallback} />;
  const checked = checkCard(code);
  if ("problem" in checked) return checked.fallback ? <FallbackCard text={checked.fallback} /> : <>{fallback}</>;
  // which block of its reply this is: only then can an answer be kept for it
  const fb = String(checked.raw.fallback);
  if (block && (!block.complete || block.index >= 3)) return <FallbackCard text={fb} />;
  if (hasHtml(checked.raw) && !htmlRuns) return <HtmlFallback text={fb} title={typeof checked.raw.title === "string" ? checked.raw.title : undefined} />;
  return <CardBody raw={checked.raw} conversation={conversation} cardKey={block && item ? blockKey(item.id, block.index) : ""} version={1} source="text" htmlRuns={htmlRuns} />;
}

/** A live card of the feed (kind view): the newest version the hub sent, drawn in place. */
export function LiveCard({ conversation, id, first }: { conversation: string; id: string; first?: AssistantView }) {
  const stored = useCards((s) => conversationCards(conversation, s).views[id]);
  const htmlRuns = useHtmlRuns(!!conversation && !!id);
  const v = stored && (!first || stored.version >= first.version) ? stored : first;
  if (!v) return null;
  const raw = v.view as unknown as Record<string, unknown>;
  const fb = typeof raw.fallback === "string" ? raw.fallback : "";
  if (!cardKnown(raw)) return <FallbackCard text={fb} />;
  if (hasHtml(raw) && !htmlRuns) return <HtmlFallback text={fb} title={typeof raw.title === "string" ? raw.title : undefined} />;
  return <CardBody raw={raw} conversation={conversation} cardKey={v.id} version={v.version} source="live" htmlRuns={htmlRuns} />;
}

function LegacyCard({ lang, code, fallback }: { lang: string; code: string; fallback: React.ReactNode }) {
  const view = useMemo(() => assistantView(lang, code), [lang, code]);
  // A card no app draws (an unknown type…) reads as its words, never its JSON; one still being written stays code.
  const words = useMemo(() => (view ? null : cardText(code)), [view, code]);
  if (!view) return words ? <FallbackCard text={words} /> : <>{fallback}</>;
  return (
    <div className="card-ui my-3 flex flex-col gap-3 text-[13px] leading-normal" data-assistant-card={lang}>
      {view.title && <p className="px-1 text-[13px] font-semibold text-ink">{view.title}</p>}
      <ViewSections view={view} {...INERT} />
    </div>
  );
}

/** A card that cannot be drawn here: its plain-text fallback. */
function FallbackCard({ text }: { text: string }) {
  return (
    <div className="card-ui my-3 rounded-card bg-surface px-4 py-3 shadow-card" data-assistant-card="fallback">
      <p className="text-[13px] leading-relaxed whitespace-pre-wrap break-words text-ink select-text">{text}</p>
    </div>
  );
}

/** A card with an html component: the frame says the assistant drew it and that the web shows its words instead. */
function HtmlFallback({ text, title }: { text: string; title?: string }) {
  return (
    <div className="card-ui my-3 overflow-hidden rounded-card bg-surface shadow-card" data-assistant-card="html-fallback">
      <div className="flex items-center gap-2 border-b border-line px-4 py-2 text-[11.5px] text-ink-3">
        <Icon name="read" size={12} className="shrink-0" />
        <span className="min-w-0 flex-1 truncate">{title ? t("ui-assistantcard.html_frame_titled", { title }) : t("ui-assistantcard.html_frame")}</span>
        <span className="shrink-0 truncate">{t("ui-assistantcard.html_not_run")}</span>
      </div>
      <p className="px-4 py-3 text-[13px] leading-relaxed whitespace-pre-wrap break-words text-ink select-text">{text}</p>
    </div>
  );
}

/** One html component of a card that runs them: nothing while the card's code loads (the first card only), its words for
 *  a component without a key (no bridge). */
function HtmlItem(p: { item: UIItem; conversation: string; cardKey: string; version: number; page: string; fallback: string }) {
  return p.item.key ? <Suspense fallback={null}><HtmlCard {...p} /></Suspense> : <HtmlFallback text={p.fallback} title={p.item.title} />;
}

// ── the card ──

/** What one of the card's actions does, read from the card itself (ui actions keep only id, confirm, submit). */
interface CardAction {
  reply?: string;
  open?: string;
  page?: string;
  pop?: boolean;
  confirm?: string;
  submit?: boolean;
}

const isObject = (v: unknown): v is Record<string, unknown> => typeof v === "object" && v !== null && !Array.isArray(v);

/** Every action of the card, on any page, by id (checkCard made sure each id is there once). */
function cardActions(raw: Record<string, unknown>): Map<string, CardAction> {
  const out = new Map<string, CardAction>();
  const pages = [raw, ...Object.values(isObject(raw.pages) ? raw.pages : {})];
  for (const page of pages) {
    for (const s of isObject(page) && Array.isArray(page.sections) ? page.sections : []) {
      for (const it of isObject(s) && Array.isArray(s.items) ? s.items : []) {
        if (!isObject(it)) continue;
        for (const a of [it.action, isObject(it.button) ? it.button.action : undefined]) {
          if (!isObject(a) || typeof a.id !== "string") continue;
          const push = isObject(a.push) ? a.push : undefined;
          out.set(a.id, {
            ...(typeof a.reply === "string" && a.reply ? { reply: a.reply } : {}),
            ...(typeof a.open === "string" && a.open ? { open: a.open } : {}),
            ...(push && typeof push.page === "string" ? { page: push.page } : {}),
            ...(a.pop === true ? { pop: true } : {}),
            ...(typeof a.confirm === "string" && a.confirm.trim() ? { confirm: a.confirm } : {}),
            ...(a.submit === true ? { submit: true } : {}),
          });
        }
      }
    }
  }
  return out;
}

/** A page of the card as the renderer draws it ("" the root); one that does not read draws nothing. `html`: its html
 *  components are kept, for a card that runs them. */
function pageView(raw: Record<string, unknown>, name: string, html: boolean): UIView | null {
  const page = name ? (isObject(raw.pages) ? raw.pages[name] : undefined) : raw;
  if (!isObject(page)) return null;
  try {
    return readView(typeof page.type === "string" ? { sections: [{ items: [page] }] } : page, { html });
  } catch {
    return null;
  }
}

/** The values a reply action sends: a submit, every field of the page (and its search / filter); a segmented, its own. */
function replyValues(view: UIView, values: Record<string, string>, a: UIAction, sem: CardAction, chosen?: { item: UIItem; value: string }): Record<string, string> {
  if (chosen?.item.name) return { [chosen.item.name]: chosen.value };
  if (!sem.submit) return {};
  const start = fieldValues(view);
  const out: Record<string, string> = {};
  for (const s of view.sections) for (const it of s.items) if (it.type === "field" && it.name) out[it.name] = values[it.name] ?? start[it.name] ?? "";
  if (view.search && (sem.submit || view.search.action.id === a.id)) out.q = values.q ?? view.search.value ?? "";
  if (view.filter) out[view.filter.name] = values[view.filter.name] ?? view.filter.value ?? view.filter.choices[0]?.value ?? "";
  return out;
}

interface Asking {
  action: UIAction;
  sem: CardAction;
  page: string;
  values: Record<string, string>;
  /** The version the user is confirming: what is sent, whatever comes in meanwhile. */
  version: number;
}

const NO_DRAFT: CardDraft = { values: {}, stack: [] };

function CardBody({ raw, conversation, cardKey, version, source, htmlRuns }: { raw: Record<string, unknown>; conversation: string; cardKey: string; version: number; source: "text" | "live"; htmlRuns?: boolean }) {
  const manager = useLinkManager();
  const takesAnswers = useStore((s) => hubHas(s.features, VIEWS_FEATURE));
  const keyed = !!conversation && !!cardKey;
  const registered = useCards((s) => keyed && (source === "live" || !!conversationCards(conversation, s).blocks[cardKey]));
  const answer: ViewAction | undefined = useCards((s) => (keyed ? conversationCards(conversation, s).actions[cardKey] : undefined));
  const stored = useCards((s) => (keyed ? s.drafts[draftKey(conversation, cardKey, s.hub)] : undefined));
  // a card without a key (an agent's chat, a block not told apart) keeps its draft here
  const [own, setOwn] = useState<CardDraft>(NO_DRAFT);
  const draft = keyed ? (stored ?? NO_DRAFT) : own;
  const patch = (f: (d: CardDraft) => CardDraft) => (keyed ? updateDraft(conversation, cardKey, f) : setOwn(f));
  const [asking, setAsking] = useState<Asking | null>(null);

  const actions = useMemo(() => cardActions(raw), [raw]);
  // the pages still there in this version (an update may take one away): the stack keeps what still reads
  const pages = isObject(raw.pages) ? raw.pages : {};
  const stack: string[] = [];
  for (const p of draft.stack) if (p in pages) stack.push(p);
  else break;
  const page = stack.at(-1) ?? "";
  const view = useMemo(() => pageView(raw, page, !!htmlRuns), [raw, page, htmlRuns]);
  const rootTitle = typeof raw.title === "string" ? raw.title : undefined;
  if (!view) return <FallbackCard text={String(raw.fallback ?? "")} />;

  const answered = !!answer;
  // what was sent shows once answered (the hub's record wins); else the card's own, under what was typed
  const values = answered && (answer.page ?? "") === page ? { ...fieldValues(view), ...(answer.values ?? {}) } : { ...fieldValues(view), ...draft.values };
  const sending = draft.sending;
  const canReply = keyed && registered && takesAnswers && !answered && !sending?.inFlight;
  const canPress = (a: UIAction) => {
    const sem = actions.get(a.id);
    if (!sem) return false;
    if (sem.reply) return canReply;
    return true; // open, push, pop
  };

  const go = (a: UIAction, chosen?: { item: UIItem; value: string }) => {
    const sem = actions.get(a.id);
    if (!sem) return;
    if (sem.pop) return patch((d) => ({ ...d, stack: stack.slice(0, -1) }));
    if (sem.page) return stack.length < STACK_MOST && patch((d) => ({ ...d, stack: [...stack, sem.page!] }));
    if (sem.open) return openTargetOf(sem.open, manager);
    if (!sem.reply || !canReply) return;
    const sent = replyValues(view, values, a, sem, chosen);
    const bad = Object.entries(sent).find(([name]) => view.sections.some((s) => s.items.some((it) => it.type === "field" && it.name === name && it.kind === "number")) && numberProblem(sent[name]));
    if (bad) return patch((d) => ({ ...d, error: t("ui-assistantcard.check_number") }));
    // a confirm, or a card that changed under the last submit: asked natively first, at the version shown now
    if (sem.confirm || draft.changed) return setAsking({ action: a, sem, page, values: sent, version });
    void send({ action: a, sem, page, values: sent, version }, false);
  };

  const send = async (x: Asking, confirmed: boolean) => {
    const out = await submitAction(conversation, cardKey, { action: x.action.id, page: x.page, values: x.values, expectedVersion: x.version, confirmed });
    if (out === "changed") toast(t("ui-assistantcard.changed_toast"));
    if (out === "answered_elsewhere") toast(t("ui-assistantcard.elsewhere"));
  };

  const retry = () => {
    if (!sending || sending.inFlight) return;
    const a = { id: sending.action };
    void send({ action: a, sem: actions.get(a.id) ?? {}, page: sending.page, values: sending.values, version: sending.expectedVersion }, sending.confirmed);
  };

  const title = view.title ?? rootTitle;
  const replies = [...actions.values()].some((a) => a.reply);
  const hint = !keyed || !replies || answered ? undefined : !registered ? "waiting" : !takesAnswers ? "unsupported" : undefined;
  return (
    <div className="card-ui my-3 flex flex-col gap-3 text-[13px] leading-normal" data-assistant-card="persona-view" data-card-key={keyed ? cardKey : undefined} data-card-version={version}>
      {(title || stack.length > 0) && (
        <div className="flex min-w-0 items-center gap-1 px-1">
          {stack.length > 0 && (
            <Button tone="quiet" icon="chevronLeft" onClick={() => patch((d) => ({ ...d, stack: stack.slice(0, -1) }))} className="-ml-2 shrink-0">
              <span className="truncate">{t("ui-assistantcard.back")}</span>
            </Button>
          )}
          {title && <p className="min-w-0 flex-1 truncate text-[13px] font-semibold text-ink">{title}</p>}
        </div>
      )}
      <ViewSections
        view={view}
        values={values}
        setValue={(name, v) => !answered && patch((d) => ({ ...d, values: { ...d.values, [name]: v }, error: undefined }))}
        busy={sending?.inFlight ? sending.action : ""}
        onPress={(a) => go(a)}
        onChoose={(it, v) => {
          if (answered) return;
          patch((d) => ({ ...d, values: { ...d.values, [it.name ?? ""]: v } }));
          if (it.action) go(it.action, { item: it, value: v });
        }}
        canPress={canPress}
        // an html reply's action is the component's key, not one of the card's actions: nothing to mark
        chosen={answer && answer.kind !== "html" ? answer.action : undefined}
        locked={answered || !!sending?.inFlight}
        renderHtml={htmlRuns ? (it) => <HtmlItem key={`${cardKey}/${version}/${page}/${it.key ?? ""}`} item={it} conversation={conversation} cardKey={cardKey} version={version} page={page} fallback={String(raw.fallback ?? "")} /> : undefined}
      />
      <CardStatus answer={answer} draft={draft} hint={hint} onRetry={retry} />
      {asking && (
        <Dialog
          open
          onClose={() => setAsking(null)}
          width={420}
          title={t("ui-assistantcard.confirm_title")}
          footer={
            <>
              <Button tone="quiet" onClick={() => setAsking(null)}>
                <span className="truncate">{t("common.cancel")}</span>
              </Button>
              <Button
                tone="primary"
                onClick={() => {
                  const x = asking;
                  setAsking(null);
                  void send(x, true);
                }}
              >
                <span className="truncate">{t("ui-assistantcard.send")}</span>
              </Button>
            </>
          }
        >
          <div className="flex flex-col gap-2 text-[13px] leading-relaxed break-words text-ink-2">
            {draft.changed && <p className="text-orange">{t("ui-assistantcard.changed_confirm")}</p>}
            {asking.sem.confirm && <p>{asking.sem.confirm}</p>}
            <p className="text-[12px] text-ink-3">{t("ui-assistantcard.will_send")}</p>
            <p className="rounded-control bg-inset px-3 py-2 whitespace-pre-wrap text-ink shadow-hairline select-text">
              {replyPreview(asking.sem.reply ?? "", view, asking.values)}
            </p>
          </div>
        </Dialog>
      )}
    </div>
  );
}

/** The message as the hub will write it (viewReplyMessage): the reply, then a line per value, by name. */
function replyPreview(reply: string, view: UIView, values: Record<string, string>): string {
  const names = Object.keys(values).sort();
  return [reply, ...names.map((k) => `${labelOf(view, k)}：${values[k]}`)].join("\n"); // i18n-ignore: the hub's own format
}

const labelOf = (view: UIView, name: string) => {
  for (const s of view.sections) for (const it of s.items) if (it.name === name) return it.label || name;
  return name;
};

/** Under the card: what became of the answer, or why it cannot be given yet. */
function CardStatus({ answer, draft, hint, onRetry }: { answer?: ViewAction; draft: CardDraft; hint?: "waiting" | "unsupported"; onRetry: () => void }) {
  let line: React.ReactNode = null;
  let tone = "text-ink-3";
  if (answer) {
    if (answer.status === "failed") {
      tone = "text-red";
      line = t("ui-assistantcard.status_failed");
    } else line = answer.status === "pending" ? t("ui-assistantcard.status_pending") : t("ui-assistantcard.status_sent");
  } else if (draft.sending?.inFlight) line = t("ui-assistantcard.sending");
  else if (draft.error) {
    tone = "text-red";
    line = draft.error;
  } else if (draft.changed) {
    tone = "text-orange";
    line = t("ui-assistantcard.changed");
  } else if (hint) line = t(hint === "waiting" ? "ui-assistantcard.waiting" : "ui-assistantcard.unsupported");
  if (!line) return null;
  const retry = !answer && draft.sending && !draft.sending.inFlight;
  return (
    <div className={cx("flex min-w-0 items-center gap-2 px-1 text-[11.5px]", tone)} role="status">
      {answer && answer.status !== "failed" && <Icon name="check" size={12} className="shrink-0" />}
      <span className="min-w-0 flex-1 truncate" title={typeof line === "string" ? line : undefined}>{line}</span>
      {draft.elsewhere && answer && <span className="shrink-0 truncate">{t("ui-assistantcard.elsewhere_short")}</span>}
      {retry && (
        <Button tone="quiet" icon="retry" onClick={onRetry} className="shrink-0">
          <span className="truncate">{t("ui-assistantcard.retry")}</span>
        </Button>
      )}
    </div>
  );
}

/** Opens what an open action names, inside the app; a target the web does not open says so, never the system.
 *  `manager`: the chat's the card is in (openLink). */
export function openTargetOf(target: string, manager?: string) {
  const to: OpenTarget | undefined = openTarget(target);
  switch (to?.kind) {
    case "web":
      openLink(to.url, undefined, manager);
      return;
    case "session":
      navigate({ name: "session", mid: to.mid, sid: to.sid });
      return;
    case "task":
      openTask(to.id);
      return;
    case "kb":
      navigate({ name: "kb", kind: to.path[0], id: to.path.slice(1).join("/") || undefined });
      return;
    case "file":
      openPanel({ kind: "file", mid: to.mid, path: to.path });
      return;
    default:
      toast(t("ui-assistantcard.cannot_open"), "error");
  }
}
