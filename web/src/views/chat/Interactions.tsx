// What the agent is waiting on (questions, plans, permissions, terminal dialogs, MCP forms and links), as Beautiful UI's Approval Card.
import { createContext, Fragment, useContext, useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import { answerCard, usePendingLine } from "../../lib/card-answer";
import { cardKeyAction, focusComposer } from "../../lib/card-keys";
import { delegateLines, isDelegating, recommendedLabels, type DelegatedAnswer } from "../../lib/delegate";
import { Choice, DigitHint } from "./Choice";
import { DelegatedCard } from "./Delegated";
import { allowBody, denyBody } from "../../lib/quick-answers";
import { BUSY_MOTION, spinStyle } from "../../lib/busy-motion";
import { appArchiveDeps, archiveAgent, archiveSession } from "../../lib/agent-archives";
import type { Interaction } from "../../lib/types";
import { defaults, elicitationDraft, elicitationResolution, elicitationSource, fields, numberInputMode, saveElicitationDraft, urlHost, urlTarget, validate, type ElicitationAction, type Field, type Values } from "../../lib/elicitation";
import { answerLines } from "../../lib/answered";
import { interactionCanAnswer, sessionTitle } from "../../lib/format";
import { interactionAsk } from "../../lib/inbox";
import { plainMarkdown } from "../../lib/plain-markdown";
import { cardToolName, mcpServerLabel } from "../../lib/turns";
import { isSecretHandoff, isSecretRun, optionDisplay, secretHandoffInput, secretRunInput } from "../../lib/tool-detail";
import { RecommendedTag } from "../../ui/RecommendedTag";
import { dropAnswered, getSession, useStore, type AnsweredCard as Answered } from "../../lib/store";
import { useChildRow } from "../../lib/use-child-sessions";
import { Button, Collapse, cx, EASE, Kbd, Menu, SECRET_INPUT, toast, useMenu } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { Markdown } from "../../ui/Markdown";
import { permissionDetail, permissionReason, summaryExtra } from "../../lib/tool-detail";
import { DIFF_TOOLS, DiffBlocks, editDiffs } from "./ActionRow";
import { cardFold, cardLine, nextWaiting, PLAN_MAX, showsAnswered } from "./dock";
import { cardState } from "../../lib/card-focus";
import { href, onLinkClick, type Route } from "../../lib/router";
import { t } from "../../i18n";
import { alwaysScope, offersAlways } from "../../lib/always-scope";
import { StatusMark } from "../../ui/StatusMark";
import { useVoiceAsking } from "../../lib/voice/calls";
import { composing } from "../../lib/send-history";

/** Answers a card: the chat's cards and the overview's quick answers (Home, #4090) send through this. */
export async function resolve(ix: Interaction, body: Record<string, unknown>) {
  const r = await answerCard(ix, body);
  if (!r.ok) toast(r.error, "error");
  else if (r.already) toast(t("chat-interactions.already_answered"), "info");
  return r.ok;
}

/**
 * A card's own keys while it holds keyboard focus (lib/card-keys.ts decides whose a key is): digits pick options,
 * Enter runs `enter` (the card's main action; an Enter on a focused control stays that control's), Esc hands focus back
 * to the composer and never answers. `focused` is when — and only when — the card's key hints (option digits, the Enter
 * badge) are drawn: a card at rest looks exactly as before. Spread `root` on the card's root element (Card does).
 */
function useCardKeys({ enter, digit }: { enter?: () => boolean | void; digit?: (n: number) => boolean } = {}) {
  const ref = useRef<HTMLDivElement | null>(null);
  const [focused, setFocused] = useState(false);
  const at = useRef({ enter, digit });
  at.current = { enter, digit };
  // a card this page cannot answer (a disabled fieldset, which stops its buttons but not a plain div's focus) shows no
  // hints: cardKeyAction already leaves its keys alone
  const answerable = () => !ref.current?.closest("fieldset[disabled]");
  const onKeyDown = (e: React.KeyboardEvent) => {
    const act = cardKeyAction(e.nativeEvent, ref.current);
    if (!act) return;
    if (act.kind === "esc") {
      // always this card's: the window's own Esc (ChatView's interrupt of a working agent) must not take it. The
      // composer is where focus goes when there is one to type into; without one (read-only) focus stays put
      e.preventDefault();
      e.stopPropagation();
      focusComposer();
    } else if (act.kind === "digit") {
      if (at.current.digit?.(act.digit)) e.preventDefault();
    } else if (at.current.enter?.() !== false) e.preventDefault();
  };
  return {
    focused,
    root: {
      ref,
      tabIndex: -1,
      "data-card-keys": true,
      onFocus: (e: React.FocusEvent<HTMLDivElement>) => setFocused(e.target === ref.current && answerable()),
      onBlur: (e: React.FocusEvent<HTMLDivElement>) => {
        if (!ref.current?.contains(e.relatedTarget)) setFocused(false);
      },
      onKeyDown,
    },
  };
}

type CardKeys = ReturnType<typeof useCardKeys>;

/**
 * A card waiting on you. No `onDismiss`: it has no "Hand back" (a card no computer's terminal can take back).
 * `looking`: a question the assistant is still deciding on (#544), marked as at work rather than waiting on the user.
 * `busy`: an answer to this card is on its way; hand back is off with the rest of its answers (#1120).
 * `cardKeys`: the card's own keyboard (useCardKeys); without it the card is only reachable by pointer, as before.
 * `shrink-0`: the box above the input scrolls (ChatView's dock), and a card with `overflow-hidden` would otherwise let
 * flex squeeze it below its content — its footer (the very buttons that answer it) out of sight below the fold.
 * `overflow-clip` rather than `overflow-hidden`: it clips to the rounded corners the same, but is not a scroll container,
 * so the footer's `sticky` follows the dock's scrolling and the buttons stay at its bottom edge while the card is taller
 * than the dock. `-bottom-1` cancels the dock's `p-1` (`bottom-0` would leave 4px of the body under the buttons).
 */
/** The card the voice is asking about (docs/web/web.md "Voice"): InteractionCard says so, Card marks its kicker row. */
const AskedByVoice = createContext(false);
/** The answer on its way for the card, as a line for its footer ("Sending: Allowed"); empty when none, or for its first
 *  moment online. InteractionCard says so (lib/card-answer.ts usePendingLine, past the card's own state) and disables
 *  every answer meanwhile. */
const SendingAnswer = createContext("");
/** Which child session asks (#5120): "From subagent · <title>" at the top of a card on its parent's page. InteractionCard
 *  says so for every kind but a tool approval, whose body already has that line (PermissionBody). */
const AskedFrom = createContext("");

function Card({ title, kicker, onDismiss, busy, children, footer, looking = false, cardKeys }: { title: string; kicker: string; onDismiss?: () => void; busy?: boolean; children: React.ReactNode; footer: React.ReactNode; looking?: boolean; cardKeys?: CardKeys }) {
  const asked = useContext(AskedByVoice);
  const sending = useContext(SendingAnswer);
  const from = useContext(AskedFrom);
  return (
    <div {...(cardKeys?.root ?? {})} aria-label={title} className="shrink-0 overflow-clip rounded-card bg-surface shadow-card outline-none focus-visible:ring-2 focus-visible:ring-accent" style={{ animation: `fade-up 350ms ${EASE} both` }}>
      <div className="px-3.5 pt-3 pb-2.5">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <div className={cx("mb-0.5 flex items-center gap-1.5 text-[11px] font-medium", looking ? "text-ink-3" : "text-attention")}>
              <StatusMark state={looking ? "working" : "needs_you"} size="sm" label={false} />
              {kicker}
              {/* the card stays a quiet surface: a neutral chip, the voice's color only on its mark */}
              {asked && (
                <span data-testid="voice-asking" className="ml-0.5 inline-flex min-w-0 items-center gap-1 rounded-full bg-inset px-1.5 py-px text-[10.5px] font-medium whitespace-nowrap text-ink-2 shadow-hairline">
                  <Icon name="voice" size={10} stroke={2.4} className="shrink-0 text-ai" />
                  <span className="truncate">{t("voice.asking_mark")}</span>
                </span>
              )}
            </div>
            <div className="text-[13.5px] font-medium text-ink">{title}</div>
          </div>
        </div>
        <div className="mt-2">
          {from && <p data-permission-from title={from} className="mb-2 truncate text-[12px] leading-snug text-ink-3">{from}</p>}
          {children}
        </div>
        {/* Hand back (dismiss) leaves the request to the computer's terminal, and the hub takes no other answer for this card
            after it: so it says so in words on a row of its own (Android's action row), never a bare × that reads as "close" */}
        {onDismiss && (
          <div className="mt-2 flex">
            <button
              type="button"
              aria-label={t("chat-interactions.hand_back")}
              onClick={onDismiss}
              disabled={busy}
              data-card-answer
              className="touch-hit -ml-1.5 rounded-[6px] px-1.5 py-0.5 text-[12px] font-medium whitespace-nowrap text-ink-3 transition-colors enabled:hover:bg-hover enabled:hover:text-ink disabled:opacity-40"
            >
              {t("chat-interactions.hand_back")}
            </button>
          </div>
        )}
      </div>
      {/* data-card-actions: what answers the card (with hand back above), for the sync scenarios (spec/fixtures/sync) */}
      <div data-card-actions className={cx("sticky -bottom-1 flex items-center justify-between gap-2 border-t border-line bg-inset px-3 py-2", sending && "flex-wrap")}>
        {sending && <p data-card-pending role="status" className="basis-full truncate text-[12px] text-ink-2">{sending}</p>}
        {footer}
      </div>
    </div>
  );
}

/** A terminal dialog's option: its words without the "(Recommended)" suffix, the "Recommended" tag instead. */
function OptionLabel({ label }: { label: string }) {
  const shown = optionDisplay(label);
  return (
    <span className="block text-[13px] text-ink">
      {shown.text}
      {shown.recommended && <RecommendedTag />}
    </span>
  );
}

function SendArrow({ ready, onClick, label, busy, kbd }: { ready: boolean; onClick: () => void; label: string; busy?: boolean; /** While the card has keyboard focus: the Enter that runs this. */ kbd?: boolean }) {
  return (
    <span className="flex items-center gap-1.5">
      {kbd && <Kbd>Enter</Kbd>}
      <button
        type="button"
        aria-label={label}
        title={label}
        disabled={!ready || busy}
        onClick={onClick}
        className="touch-hit flex size-7 items-center justify-center rounded-[8px] transition-[background-color,color,transform] duration-200 enabled:active:scale-[0.96]"
        style={{ background: ready ? "var(--ink)" : "var(--field)", color: ready ? "var(--surface)" : "var(--ink-3)", boxShadow: ready ? "inset 0 1px 0 rgba(255,255,255,0.14)" : "var(--shadow-btn)" }}
      >
        <Icon name="up" size={14} stroke={2.5} />
      </button>
    </span>
  );
}

function ToolLoopCard({ ix }: { ix: Interaction }) {
  const [busy, setBusy] = useState(false);
  const sending = useRef(false);
  const keys = useCardKeys();
  const q = ix.questions?.[0];
  const options = q?.options;
  if (!q || !options || options.length !== 2) return null;
  const send = async (label: string) => {
    if (sending.current) return;
    sending.current = true;
    setBusy(true);
    try {
      await resolve(ix, { answers: { [q.question]: label } });
    } finally {
      sending.current = false;
      setBusy(false);
    }
  };
  return (
    <Card
      kicker={t("chat-interactions.tool_loop_kicker")}
      title={q.question}
      cardKeys={keys}
      footer={options.map((option, i) => (
        <Button key={i} tone={i === 0 ? "quiet" : "primary"} className="min-w-0 shrink" title={option.label} disabled={busy} onClick={() => void send(option.label)}>
          <span className="truncate">{option.label}</span>
        </Button>
      ))}
    >
      {null}
    </Card>
  );
}

function QuestionCard({ ix }: { ix: Interaction }) {
  const qs = ix.questions ?? [];
  const [qi, setQi] = useState(0);
  const [picked, setPicked] = useState<Record<number, string[]>>({});
  const [custom, setCustom] = useState<Record<number, string>>({});
  const [busy, setBusy] = useState(false);
  const q = qs[qi];
  // picking on a single-select slides to the next question by itself, a beat later; going on before that (Enter, the
  // arrow) cancels the slide, or the two would advance a question each and skip one
  const slide = useRef<number | undefined>(undefined);
  const advance = () => {
    clearTimeout(slide.current);
    setQi((i) => Math.min(qs.length - 1, i + 1));
  };
  // the card's own keys (a hook, so before the early return): digits pick this question's options, Enter is what the
  // send arrow does, Esc is the card's wherever in it focus sits
  const keys = useCardKeys({
    digit: (n) => {
      const o = q?.options?.[n - 1];
      if (!o || busy) return false;
      toggle(o.label);
      return true;
    },
    enter: () => {
      if (busy || (last ? !all : !answered(qi))) return false;
      if (last) void send();
      else advance();
    },
  });
  if (!q) return null;
  const looking = isDelegating(ix);
  const picks = recommendedLabels(qs, ix.delegate?.recommend);
  // a recommendation that is the options' own words shows on them, not again in the context
  const onOptions = picks.every((p) => p.length > 0);
  const context = delegateLines(ix.delegate).filter((l) => !(l.key === "recommend" && onOptions));
  const last = qi === qs.length - 1;
  const answered = (i: number) => (picked[i]?.length ?? 0) > 0 || Boolean(custom[i]?.trim());
  const all = qs.every((_, i) => answered(i));

  const send = async () => {
    setBusy(true);
    const answers: Record<string, string> = {};
    qs.forEach((x, i) => {
      answers[x.question] = custom[i]?.trim() || (picked[i] ?? []).join(", ");
    });
    await resolve(ix, { answers });
    setBusy(false);
  };

  const toggle = (label: string) => {
    setCustom((c) => ({ ...c, [qi]: "" }));
    setPicked((cur) => {
      const have = cur[qi] ?? [];
      const next = q.multi_select ? (have.includes(label) ? have.filter((x) => x !== label) : [...have, label]) : [label];
      return { ...cur, [qi]: next };
    });
    if (!q.multi_select && !last) slide.current = window.setTimeout(() => setQi((i) => Math.min(qs.length - 1, i + 1)), 380);
  };

  return (
    <Card
      kicker={q.header || t("chat-interactions.question")}
      title={q.question}
      looking={looking}
      cardKeys={keys}
      onDismiss={() => resolve(ix, { dismiss: true })}
      busy={busy}
      footer={
        <>
          <span className="flex items-center gap-2">
            {qs.length > 1 && (
              <>
                <button type="button" aria-label={t("chat-interactions.previous_question")} disabled={qi === 0} onClick={() => setQi(qi - 1)} className="touch-hit flex size-6 items-center justify-center rounded-[5px] text-ink-3 enabled:hover:bg-hover disabled:opacity-35">
                  <Icon name="chevronLeft" size={14} stroke={2.2} />
                </button>
                <span className="flex items-center gap-1">
                  {qs.map((_, i) => (
                    <button
                      key={i}
                      type="button"
                      aria-label={t("chat-interactions.question_n", { n: i + 1 })}
                      onClick={() => setQi(i)}
                      className="rounded-full transition-all duration-300"
                      style={i === qi ? { width: 9, height: 9, border: "2.5px solid var(--ink)" } : answered(i) ? { width: 7, height: 7, background: "var(--ink-3)" } : { width: 7, height: 7, border: "1.5px solid var(--ink-3)" }}
                    />
                  ))}
                </span>
                <button type="button" aria-label={t("chat-interactions.next_question")} disabled={last} onClick={() => setQi(qi + 1)} className="touch-hit flex size-6 items-center justify-center rounded-[5px] text-ink-3 enabled:hover:bg-hover disabled:opacity-35">
                  <Icon name="chevronRight" size={14} stroke={2.2} />
                </button>
              </>
            )}
            {q.multi_select && <span className="text-[11.5px] text-ink-3">{t("chat-interactions.multi_select")}</span>}
          </span>
          <SendArrow ready={last ? all : answered(qi)} busy={busy} kbd={keys.focused} label={last ? t("chat-interactions.send_answer") : t("chat-interactions.next_question")} onClick={() => (last ? void send() : advance())} />
        </>
      }
    >
      {looking && (
        <div data-delegating className="mb-2 flex items-start gap-2 rounded-control bg-inset px-2.5 py-2 text-[12px] leading-snug shadow-hairline">
          <Icon name="eye" size={13} stroke={2.2} className="mt-px shrink-0 text-ink-3" />
          <span className="min-w-0">
            <span className="block font-medium text-ink">{t("chat-delegate.looking")}</span>
            <span className="block text-ink-3">{t("chat-delegate.looking_hint")}</span>
          </span>
        </div>
      )}
      {context.length > 0 && (
        <div data-delegate-context className="mb-2 rounded-control bg-inset px-2.5 py-2 shadow-hairline">
          <div className="mb-1 flex items-center gap-1.5 text-[11px] font-medium text-ink-3">
            <Icon name="sparkles" size={12} stroke={2.2} className="shrink-0" />
            {t("chat-delegate.context_title")}
          </div>
          <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 text-[12px] leading-snug">
            {context.map((l) => (
              <Fragment key={l.key}>
                <dt className="whitespace-nowrap text-ink-3">{l.label}</dt>
                <dd className="break-words whitespace-pre-wrap text-ink-2">{l.text}</dd>
              </Fragment>
            ))}
          </dl>
        </div>
      )}
      <div key={qi} className="flex flex-col gap-0.5" style={{ animation: `fade-up 300ms ${EASE} both` }}>
        {(q.options ?? []).map((o, i) => (
          <Choice key={o.label} on={(picked[qi] ?? []).includes(o.label)} multi={q.multi_select} label={o.label} desc={o.description} pick={picks[qi]?.includes(o.label)} hint={keys.focused && i < 9 ? i + 1 : undefined} onClick={() => toggle(o.label)} />
        ))}
        <label className="-mx-1.5 flex items-center gap-2 rounded-control px-1.5 py-1.5 transition-colors focus-within:bg-hover hover:bg-hover">
          <span aria-hidden className="size-4 shrink-0" />
          <input
            value={custom[qi] ?? ""}
            onChange={(e) => {
              setCustom((c) => ({ ...c, [qi]: e.target.value }));
              if (e.target.value) setPicked((p) => ({ ...p, [qi]: [] }));
            }}
            onKeyDown={(e) => e.key === "Enter" && !composing(e.nativeEvent) && (last ? all && void send() : answered(qi) && setQi(qi + 1))}
            placeholder={t("chat-interactions.custom_placeholder")}
            aria-label={t("chat-interactions.custom_answer")}
            // focused in a box that scrolls, it lands clear of the card's sticky answer row below and of a sticky title
            // above (the assistant's Pending tray), rather than under either
            className="min-w-0 flex-1 scroll-my-14 bg-transparent text-[13px] text-ink outline-none placeholder:text-ink-3"
          />
        </label>
      </div>
    </Card>
  );
}

const MODES = [
  { key: "acceptEdits", label: t("chat-interactions.mode_accept_edits") },
  { key: "bypassPermissions", label: t("chat-interactions.mode_bypass") },
  { key: "default", label: t("chat-interactions.mode_default") },
];

function PlanCard({ ix }: { ix: Interaction }) {
  const [feedback, setFeedback] = useState("");
  const [rejecting, setRejecting] = useState(false);
  const [busy, setBusy] = useState(false);
  const menu = useMenu();
  const go = async (body: Record<string, unknown>) => {
    setBusy(true);
    await resolve(ix, body);
    setBusy(false);
  };
  // Enter approves with edits (the Approve button); rejecting has its own field, whose Enter is the field's
  const keys = useCardKeys({
    enter: () => {
      if (busy || rejecting) return false;
      void go({ approve: true, mode: "acceptEdits" });
    },
  });
  return (
    <Card
      kicker={t("chat-interactions.plan_kicker")}
      title={t("chat-interactions.plan_title")}
      cardKeys={keys}
      onDismiss={() => go({ dismiss: true })}
      busy={busy}
      footer={
        rejecting ? (
          <>
            <input
              autoFocus
              value={feedback}
              onChange={(e) => setFeedback(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && !composing(e.nativeEvent) && feedback.trim() && go({ approve: false, feedback: feedback.trim() })}
              placeholder={t("chat-interactions.feedback_placeholder")}
              className="h-8 min-w-0 flex-1 rounded-control bg-surface px-2.5 text-[13px] text-ink shadow-btn outline-none placeholder:text-ink-3"
            />
            <Button tone="quiet" disabled={busy} onClick={() => setRejecting(false)}>
              {t("common.cancel")}
            </Button>
            <Button tone="primary" busy={busy} disabled={!feedback.trim()} onClick={() => go({ approve: false, feedback: feedback.trim() })}>
              {t("chat-interactions.reject")}
            </Button>
          </>
        ) : (
          <>
            <Button tone="quiet" disabled={busy} onClick={() => setRejecting(true)}>
              {t("chat-interactions.request_changes")}
            </Button>
            <span className="flex items-center">
              <Button tone="primary" busy={busy} className="rounded-r-none" onClick={() => go({ approve: true, mode: "acceptEdits" })}>
                {t("chat-interactions.approve")}
                {keys.focused && <Kbd>Enter</Kbd>}
              </Button>
              <button type="button" aria-label={t("chat-interactions.approve_mode")} disabled={busy} onClick={menu.open} className="touch-hit flex h-8 w-7 items-center justify-center rounded-r-control border-l border-white/15 bg-ink text-surface enabled:hover:opacity-90 disabled:opacity-40">
                <Icon name="chevron" size={12} stroke={2.4} />
              </button>
              {menu.anchor && <Menu anchor={menu.anchor} onClose={menu.close} align="right" side="top" width={196} items={MODES.map((m) => ({ key: m.key, label: m.label, onSelect: () => go({ approve: true, mode: m.key }) }))} />}
            </span>
          </>
        )
      }
    >
      <div className="overflow-y-auto rounded-[8px] bg-inset px-3 py-2.5 shadow-hairline" style={{ maxHeight: PLAN_MAX }} data-scroll-box>
        <Markdown text={ix.plan ?? ""} small />
      </div>
    </Card>
  );
}

function PermissionCard({ ix }: { ix: Interaction }) {
  const [busy, setBusy] = useState(false);
  const go = async (body: Record<string, unknown>) => {
    setBusy(true);
    await resolve(ix, body);
    setBusy(false);
  };
  // Enter allows (the Allow button). Deny is never a key away: it stays an explicit press of its button — or Enter
  // while that button is focused, which is the button's own activation, not the card's main action
  const keys = useCardKeys({
    enter: () => {
      if (busy) return false;
      void go(allowBody());
    },
  });
  return (
    <Card
      kicker={t("chat-interactions.permission_kicker")}
      title={t("chat-interactions.permission_title", { tool: cardToolName(ix) })}
      cardKeys={keys}
      onDismiss={() => go({ dismiss: true })}
      busy={busy}
      footer={
        <>
          <Button tone="quiet" disabled={busy} onClick={() => go(denyBody())}>
            {t("chat-interactions.deny")}
          </Button>
          <span className="flex gap-1.5">
            {/* Only where an Always is kept (offersAlways): never on the manager's own cards. */}
            {offersAlways(ix) && (
              <Button tone="secondary" disabled={busy} onClick={() => go({ allow: true, always: true })}>
                {t("chat-interactions.always_allow")}
              </Button>
            )}
            <Button tone="primary" busy={busy} onClick={() => go(allowBody())}>
              {t("chat-interactions.allow")}
              {keys.focused && <Kbd>Enter</Kbd>}
            </Button>
          </span>
        </>
      }
    >
      <PermissionBody ix={ix} />
      {alwaysScope(ix) && <p className="mt-2 text-[12px] leading-relaxed text-ink-3">{alwaysScope(ix)}</p>}
    </Card>
  );
}

const CODE_BOX = "max-h-48 overflow-auto rounded-[8px] bg-inset px-2.5 py-2 font-mono text-[11.5px] leading-[1.6] whitespace-pre-wrap text-ink-2 shadow-hairline";

/** What the tool will do, field by field (Android PermissionRequestSummary): the agent's sentence, then the command,
 *  file or address under a small label, an edit's changes; a tool it does not know shows the hub's summary. The raw
 *  input stays one click away, open from the start only when nothing else says what the tool does. */
const FROM_MAX = 48;

/** "From subagent · <type>" for a subagent's card, its type on one line and cut to FROM_MAX; for a child session's card
 *  on its parent's page (#5120, `folded_under`), the child's title the same way, and "From worker · <title>" for a lead's
 *  worker's (#5180, `folded_as`); empty otherwise. */
export function permissionFrom(ix: Pick<Interaction, "agent_id" | "agent_type" | "folded_under" | "folded_as" | "session_title">): string {
  const oneLine = (s: string | undefined) => (s ?? "").replace(/\s+/g, " ").trim();
  const name = ix.folded_under ? oneLine(ix.session_title) || t("chat-subagentcard.untitled_task") : ix.agent_id?.trim() ? oneLine(ix.agent_type) : "";
  if (!name || (!ix.folded_under && name.toLowerCase() === "main")) return "";
  const agent = name.length > FROM_MAX ? `${name.slice(0, FROM_MAX - 1)}…` : name;
  return ix.folded_under && ix.folded_as === "worker" ? t("lib-elicitation.from_worker", { agent }) : t("lib-elicitation.from_subagent", { agent });
}

function PermissionBody({ ix }: { ix: Interaction }) {
  const input = ix.tool_input ?? {};
  const hasInput = Object.keys(input).length > 0;
  const detail = permissionDetail(ix.tool_name, input);
  const diffs = detail && DIFF_TOOLS.has(ix.tool_name ?? "") ? editDiffs({ id: ix.id, seq: 0, role: "assistant", kind: "tool_use", tool_name: ix.tool_name, tool_input: input }) : null;
  const reason = permissionReason(ix.agent, ix.reason);
  const extra = detail ? summaryExtra(ix.summary, detail.fields) : "";
  const extraShown = extra && extra !== detail?.note && extra !== reason && !detail?.fields.some((f) => f.value === extra);
  const lead = detail ? undefined : ix.summary?.trim();
  const [showInput, setShowInput] = useState(!detail && !lead);
  // a subagent's call (Claude Code sends its parent's session with agent_id and agent_type): say which one asks
  // (#4376); the main session's own cards (agent_type alone, as `claude --agent x` sets it, or "main") say nothing
  const from = permissionFrom(ix);
  return (
    <div className="flex flex-col gap-2">
      {from && <p data-permission-from title={from} className="truncate text-[12px] leading-snug text-ink-3">{from}</p>}
      {reason && <p className="text-[13px] leading-relaxed whitespace-pre-wrap text-ink-2">{reason}</p>}
      {detail?.note && <p className="text-[13px] leading-relaxed whitespace-pre-wrap text-ink">{detail.note}</p>}
      {detail?.fields.map((f) => (
        <div key={f.label}>
          <div className="mb-1 text-[11px] font-medium text-ink-3">{t(`chat-interactions.field_${f.label}`)}</div>
          <pre className={CODE_BOX}>{f.value}</pre>
        </div>
      ))}
      {diffs && diffs.length > 0 && (
        <div>
          <div className="mb-1 text-[11px] font-medium text-ink-3">{t(ix.tool_name === "Write" ? "chat-interactions.field_content" : "chat-interactions.field_changes")}</div>
          <div className="flex max-h-48 flex-col gap-2 overflow-auto">
            <DiffBlocks diffs={diffs} />
          </div>
        </div>
      )}
      {extraShown && <p className="text-[12.5px] leading-relaxed whitespace-pre-wrap text-ink-2">{extra}</p>}
      {lead && <pre className={CODE_BOX}>{lead}</pre>}
      {hasInput && (detail || lead) && (
        <button type="button" aria-expanded={showInput} onClick={() => setShowInput((v) => !v)} className="touch-hit flex items-center gap-1 self-start text-[11.5px] whitespace-nowrap text-ink-3 transition-colors hover:text-ink">
          <Icon name="chevron" size={11} stroke={2.4} style={{ transform: showInput ? "none" : "rotate(-90deg)", transition: "transform 150ms" }} />
          {t(showInput ? "chat-interactions.hide_input" : "chat-interactions.show_input")}
        </button>
      )}
      {showInput && <pre className={CODE_BOX}>{JSON.stringify(input, null, 2)}</pre>}
    </div>
  );
}

/**
 * The assistant's password card (#1137, #1581): what runs where, every command verbatim and read-only, the password
 * field, Decline and Run. The password lives only in this card's state and its input: emptied as Run sends it, on
 * Decline, and when the card goes (a card with another id is a new SecretRunCard, see InteractionCard). No form and
 * nothing Enter submits: the commands run on a click on Run, never on a key pressed while typing. The card's own keys
 * are only Esc back to the composer — no digits (they are the password), no Enter (never a key runs these commands).
 */
function SecretRunCard({ ix }: { ix: Interaction }) {
  const [busy, setBusy] = useState<"run" | "decline" | null>(null);
  const [password, setPassword] = useState("");
  const input = useRef<HTMLInputElement>(null);
  const keys = useCardKeys();
  // a field taken off the page keeps its value until collected, and Firefox offers to save a formless password field
  // that is removed while it holds one (its form-removal capture): it is emptied before React removes it
  useLayoutEffect(() => {
    const el = input.current;
    return () => {
      if (el) el.value = "";
    };
  }, []);
  const { machine, purpose, commands } = secretRunInput(ix.tool_input);
  // without commands in tool_input the hub's summary (the computer, the purpose and every command) stands in for them;
  // with neither, nothing says what would run and Run stays off
  const summary = commands.length ? "" : (ix.summary?.trim() ?? "");
  // the hub refuses a password of spaces alone
  const ready = password.trim() !== "" && (commands.length > 0 || summary !== "");
  const label = ix.fields?.find((f) => f.key === "password")?.label?.trim() || t("chat-interactions.password");
  const send = async (body: Record<string, unknown>, key: "run" | "decline") => {
    setPassword("");
    setBusy(key);
    await resolve(ix, body);
    setBusy(null);
  };
  return (
    <Card
      kicker={t("chat-interactions.secret_run_kicker")}
      title={t("chat-interactions.secret_run_title", { machine })}
      cardKeys={keys}
      footer={
        <>
          <Button tone="quiet" busy={busy === "decline"} disabled={busy !== null} onClick={() => void send({ allow: false }, "decline")}>
            {t("chat-interactions.secret_run_decline")}
          </Button>
          <Button tone="primary" busy={busy === "run"} disabled={busy !== null || !ready} onClick={() => void send({ allow: true, values: { password } }, "run")}>
            {t("chat-interactions.secret_run_run")}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-2">
        {purpose && <p className="text-[13px] leading-relaxed whitespace-pre-wrap text-ink">{purpose}</p>}
        {commands.length > 0 ? (
          <div>
            <div className="mb-1 text-[11px] font-medium text-ink-3">{t("chat-interactions.secret_run_commands")}</div>
            {/* one block per command: a long one that wraps never reads as two */}
            <div className="flex flex-col gap-1.5">
              {commands.map((c, i) => <pre key={i} data-secret-run-command className={CODE_BOX}>{c}</pre>)}
            </div>
          </div>
        ) : (
          summary && <pre data-secret-run-summary className={CODE_BOX}>{summary}</pre>
        )}
        <div className="flex flex-col gap-1">
          {/* The user's computer password, not this site's: "one-time-code" keeps Chrome from offering to save it (its
              password parser skips such fields) and from suggesting a generated one ("new-password" would); no name, no
              form. */}
          <input
            ref={input}
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            // Enter only puts the keyboard away, as Return does on the iPhone card
            onKeyDown={(e) => {
              if (e.key !== "Enter") return;
              e.preventDefault();
              e.currentTarget.blur();
            }}
            placeholder={label}
            aria-label={label}
            disabled={busy !== null}
            {...SECRET_INPUT}
            autoComplete="one-time-code"
            autoCapitalize="off"
            autoCorrect="off"
            enterKeyHint="done"
            className="h-8 w-full min-w-0 rounded-control bg-surface px-2.5 text-[13px] text-ink shadow-btn outline-none placeholder:text-ink-3"
          />
          <span className="text-[12px] leading-snug text-ink-3">{t("chat-interactions.secret_run_note")}</span>
        </div>
      </div>
    </Card>
  );
}

/**
 * The hand-off card (#3962): secret values a provider holds, handed to one Agent run once the user approves here. Only
 * this card approves it (lib/card-answer.ts sends its approval with the card's feature; Overview's rows and the
 * notifications open the chat instead), and no key does: Allow is a press of its button. It says what goes where:
 * each variable with the item it comes from, the Agent and its computer, where it runs and what it is asked.
 */
function SecretHandoffCard({ ix }: { ix: Interaction }) {
  const [busy, setBusy] = useState<"allow" | "always" | "deny" | null>(null);
  const keys = useCardKeys();
  const { variables, agent, machine, cwd, prompt } = secretHandoffInput(ix.tool_input);
  // nothing says what would be handed over: only Deny
  const ready = variables.length > 0;
  const go = async (body: Record<string, unknown>, key: "allow" | "always" | "deny") => {
    setBusy(key);
    const r = await answerCard(ix, body, { handoffCard: true });
    if (!r.ok) toast(r.error, "error");
    else if (r.already) toast(t("chat-interactions.already_answered"), "info");
    setBusy(null);
  };
  const lead = agent
    ? t("chat-interactions.secret_handoff_lead", { agent, machine })
    : t("chat-interactions.secret_handoff_lead_unnamed", { machine });
  return (
    <Card
      kicker={t("chat-interactions.permission_kicker")}
      title={t("chat-interactions.secret_handoff_title")}
      cardKeys={keys}
      footer={
        <>
          <Button tone="quiet" busy={busy === "deny"} disabled={busy !== null} onClick={() => void go({ allow: false }, "deny")}>
            {t("chat-interactions.deny")}
          </Button>
          <span className="flex gap-1.5">
            {ix.always_label && (
              <Button tone="secondary" busy={busy === "always"} disabled={busy !== null || !ready} onClick={() => void go({ allow: true, always: true }, "always")}>
                {t("chat-interactions.always_allow")}
              </Button>
            )}
            <Button tone="primary" busy={busy === "allow"} disabled={busy !== null || !ready} onClick={() => void go({ allow: true }, "allow")}>
              {t("chat-interactions.allow")}
            </Button>
          </span>
        </>
      }
    >
      <div className="flex flex-col gap-2">
        <p className="text-[13px] leading-relaxed text-ink">{machine ? lead : t("chat-interactions.secret_handoff_lead_bare")}</p>
        <ul className="flex flex-col gap-1">
          {variables.map((v) => (
            <li key={v.name} data-handoff-variable className="flex min-w-0 items-baseline gap-2">
              <code className="shrink-0 font-mono text-[12px] text-ink">{v.name}</code>
              <span className="min-w-0 break-words text-[13px] text-ink-2">{v.title ? `${v.provider} › ${v.title}` : v.provider}</span>
            </li>
          ))}
        </ul>
        {(cwd || prompt) && (
          <div className="flex flex-col gap-0.5 text-[12px] leading-snug text-ink-3">
            {cwd && <span data-handoff-cwd className="truncate font-mono">{cwd}</span>}
            {prompt && <span data-handoff-prompt className="line-clamp-2">{prompt}</span>}
          </div>
        )}
        <span className="text-[12px] leading-snug text-ink-3">
          {t("chat-interactions.secret_handoff_note")}
          {ix.always_label ? ` ${t("chat-interactions.secret_handoff_always_scope", { scope: ix.always_label })}` : ""}
        </span>
      </div>
    </Card>
  );
}

function CloseBlockedAgent({ ix, disabled }: { ix: Interaction; disabled: boolean }) {
  const session = useStore(() => getSession(ix.machine_id, ix.session_id));
  const [confirming, setConfirming] = useState(false);
  const [closing, setClosing] = useState(false);
  if (!session || session.assistant || session.status !== "blocked") return null;
  const close = async () => {
    if (closing) return;
    setClosing(true);
    await archiveAgent(session, true, { ...appArchiveDeps, archive: s => archiveSession(s, true, true) });
    setClosing(false);
    setConfirming(false);
  };
  if (!confirming) return <Button className="min-h-11" disabled={disabled} onClick={() => setConfirming(true)}>{t("chat-interactions.close_agent")}</Button>;
  return <div className="flex min-w-0 flex-col gap-2">
    <p className="text-ink-2">{t("chat-interactions.close_agent_hint")}</p>
    <div className="flex flex-wrap gap-2">
      <Button className="min-h-11" disabled={disabled || closing} onClick={() => setConfirming(false)}>{t("chat-interactions.close_agent_cancel")}</Button>
      <Button className="min-h-11" tone="danger" busy={closing} disabled={disabled || closing} onClick={() => void close()}>{t("chat-interactions.close_agent_confirm")}</Button>
    </div>
  </div>;
}

function ScreenCard({ ix }: { ix: Interaction }) {
  const input = useRef<HTMLInputElement>(null);
  useLayoutEffect(() => {
    const el = input.current;
    return () => {
      if (el) el.value = "";
    };
  }, []);
  const d = ix.dialog;
  const [text, setText] = useState("");
  const [typing, setTyping] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const go = async (body: Record<string, unknown>, key: string) => {
    setBusy(key);
    await resolve(ix, body);
    setBusy(null);
  };
  // digits pick the options by place (their key badges say what the computer itself reads); with no options Enter sends
  // the text. A "type something" option's digit only opens its field, as a click does
  const keys = useCardKeys({
    digit: (n) => {
      const o = d?.options?.[n - 1];
      if (!o || busy !== null) return false;
      if (o.kind === "type_something") setTyping(o.key);
      else void go({ option: o.key }, o.key);
      return true;
    },
    enter: () => {
      if (busy !== null || (d?.options?.length ?? 0) > 0) return false;
      void go({ text, keys: ["enter"] }, "text");
    },
  });
  if (d?.multi) return <MultiScreenCard ix={ix} />;
  return (
    <Card
      kicker={d?.header || t("chat-interactions.screen_kicker")}
      title={d?.question || t("chat-interactions.screen_title")}
      cardKeys={keys}
      onDismiss={() => go({ dismiss: true }, "dismiss")}
      busy={busy !== null}
      footer={<div className="flex w-full min-w-0 flex-col gap-2">
        <div className="flex min-w-0 items-center gap-2">
        {d?.options?.length ? (
          <span className="text-[11.5px] text-ink-3">{t("chat-interactions.screen_hint")}</span>
        ) : (
          <>
            <input
              ref={ix.secret ? input : undefined}
              type={ix.secret ? "password" : "text"}
              value={text}
              onChange={(e) => setText(e.target.value)}
              placeholder={ix.secret ? t("chat-interactions.password") : t("chat-interactions.type_and_enter")}
              autoComplete={ix.secret ? "one-time-code" : "off"}
              autoCapitalize={ix.secret ? "off" : undefined}
              autoCorrect={ix.secret ? "off" : undefined}
              {...(ix.secret ? SECRET_INPUT : {})}
              className="h-8 min-w-0 flex-1 rounded-control bg-surface px-2.5 text-[13px] text-ink shadow-btn outline-none placeholder:text-ink-3"
            />
            <Button tone="primary" busy={busy === "text"} disabled={busy !== null} onClick={() => go({ text, keys: ["enter"] }, "text")}>
              {t("chat-interactions.send")}
              {keys.focused && <Kbd>Enter</Kbd>}
            </Button>
          </>
        )}
        </div>
        <CloseBlockedAgent ix={ix} disabled={busy !== null} />
      </div>}
    >
      {d?.detail && <p className="mb-2 text-[12.5px] leading-relaxed whitespace-pre-wrap text-ink-2">{d.detail}</p>}
      {d?.options?.length ? (
        <div className="flex flex-col gap-0.5">
          {d.options.map((o, i) =>
            o.kind === "type_something" && typing === o.key ? (
              <div key={o.key} className="flex items-center gap-1.5 py-1">
                <input
                  autoFocus
                  value={text}
                  onChange={(e) => setText(e.target.value)}
                  onKeyDown={(e) => e.key === "Enter" && !composing(e.nativeEvent) && text.trim() && go({ option: o.key, text }, o.key)}
                  placeholder={o.label}
                  className="h-8 min-w-0 flex-1 rounded-control bg-surface px-2.5 text-[13px] text-ink shadow-btn outline-none placeholder:text-ink-3"
                />
                <Button tone="primary" busy={busy === o.key} disabled={!text.trim() || busy !== null} onClick={() => go({ option: o.key, text }, o.key)}>
                  {t("chat-interactions.send")}
                </Button>
              </div>
            ) : (
              <button
                key={o.key}
                type="button"
                disabled={busy !== null}
                onClick={() => (o.kind === "type_something" ? setTyping(o.key) : go({ option: o.key }, o.key))}
                className="touch-hit -mx-1.5 flex items-start gap-2 rounded-control px-1.5 py-1.5 text-left transition-colors hover:bg-hover disabled:opacity-60"
              >
                <span className="mt-[1px] flex h-4.5 min-w-4.5 shrink-0 items-center justify-center rounded-[5px] bg-field px-1 font-mono text-[10.5px] text-ink-2 shadow-hairline">{o.key}</span>
                {keys.focused && i < 9 && <DigitHint n={i + 1} />}
                <span className="min-w-0">
                  <OptionLabel label={o.label} />
                  {o.detail && <span className="block text-[12px] text-ink-3">{o.detail}</span>}
                </span>
                {busy === o.key && <span className={`ml-auto mt-1 size-3 shrink-0 rounded-full border-[1.5px] border-line-strong border-t-ink-2 ${BUSY_MOTION}`} style={spinStyle(700)} />}
              </button>
            ),
          )}
        </div>
      ) : (
        <pre className="max-h-56 overflow-auto rounded-[8px] bg-[#1b1c1f] px-3 py-2 font-mono text-[11.5px] leading-[1.5] whitespace-pre text-[#e6e7e9]">{ix.screen}</pre>
      )}
    </Card>
  );
}

/** A multi-select terminal question: ticks (starting from what the computer shows) and the free text, sent together;
 *  the computer's boxes end up as here, one key at a time (node/dialogmulti.go). */
function MultiScreenCard({ ix }: { ix: Interaction }) {
  const d = ix.dialog!;
  const options = d.options ?? [];
  const field = options.find((o) => o.kind === "type_something");
  const [picked, setPicked] = useState(() => new Set(options.filter((o) => !o.kind && o.checked).map((o) => o.key)));
  const [text, setText] = useState(field?.text ?? "");
  const [busy, setBusy] = useState<string | null>(null);
  const go = async (body: Record<string, unknown>, key: string) => {
    setBusy(key);
    await resolve(ix, body);
    setBusy(null);
  };
  const toggle = (key: string) => setPicked((p) => {
    const next = new Set(p);
    if (next.has(key)) next.delete(key); else next.add(key);
    return next;
  });
  const chat = options.find((o) => o.kind === "chat");
  const answer = text.trim();
  // digits tick the boxes by place (their key badges are the computer's own digits); Enter is the submit button's send
  const plain = options.filter((o) => !o.kind);
  const keys = useCardKeys({
    digit: (n) => {
      const o = plain[n - 1];
      if (!o || busy !== null) return false;
      toggle(o.key);
      return true;
    },
    enter: () => {
      if (busy !== null || (picked.size === 0 && !answer)) return false;
      void go({ options: options.map((o) => o.key).filter((k) => picked.has(k)), text: answer || undefined }, "multi");
    },
  });
  return (
    <Card
      kicker={[d.header, t("chat-interactions.multi_select")].filter(Boolean).join(" · ")}
      title={d.question || t("chat-interactions.multi_title")}
      cardKeys={keys}
      onDismiss={() => go({ dismiss: true }, "dismiss")}
      busy={busy !== null}
      footer={
        <>
          {chat && (
            <Button busy={busy === chat.key} disabled={busy !== null} onClick={() => go({ option: chat.key }, chat.key)}>
              {t("chat-interactions.say_in_chat")}
            </Button>
          )}
          <span className="flex-1" />
          <Button
            tone="primary"
            busy={busy === "multi"}
            disabled={busy !== null || (picked.size === 0 && !answer)}
            onClick={() => go({ options: options.map((o) => o.key).filter((k) => picked.has(k)), text: answer || undefined }, "multi")}
          >
            {d.submit === "Next" ? t("chat-interactions.next_question") : t("chat-interactions.submit")}
            {keys.focused && <Kbd>Enter</Kbd>}
          </Button>
        </>
      }
    >
      {d.detail && <p className="mb-2 text-[12.5px] leading-relaxed whitespace-pre-wrap text-ink-2">{d.detail}</p>}
      <div className="flex flex-col gap-0.5">
        {options.filter((o) => !o.kind).map((o, i) => (
          <button
            key={o.key}
            type="button"
            role="checkbox"
            aria-checked={picked.has(o.key)}
            disabled={busy !== null}
            onClick={() => toggle(o.key)}
            className="touch-hit -mx-1.5 flex items-start gap-2 rounded-control px-1.5 py-1.5 text-left transition-colors hover:bg-hover disabled:opacity-60"
          >
            <span className={`mt-[1px] flex h-4.5 min-w-4.5 shrink-0 items-center justify-center rounded-[5px] px-1 font-mono text-[10.5px] shadow-hairline ${picked.has(o.key) ? "bg-accent text-white" : "bg-field text-ink-2"}`}>
              {picked.has(o.key) ? "✓" : o.key}
            </span>
            {keys.focused && i < 9 && <DigitHint n={i + 1} />}
            <span className="min-w-0">
              <OptionLabel label={o.label} />
              {o.detail && <span className="block text-[12px] text-ink-3">{o.detail}</span>}
            </span>
          </button>
        ))}
        {field && (
          <input
            value={text}
            onChange={(e) => setText(e.target.value)}
            placeholder={t("chat-interactions.other_answer")}
            disabled={busy !== null}
            className="mt-1 h-8 min-w-0 rounded-control bg-surface px-2.5 text-[13px] text-ink shadow-btn outline-none placeholder:text-ink-3"
          />
        )}
      </div>
    </Card>
  );
}

/**
 * An MCP server's form or link (docs/protocol/protocol.md "MCP elicitation"), as Android's ElicitationFields: a form in the
 * schema subset lib/elicitation.ts checks (anything else goes to the terminal), or a link that has to be opened from
 * here before "Opened, continue". "Submit" accepts, "Deny" declines, "Cancel" cancels; × hands it back to the computer as on every card.
 */
function ElicitationCard({ ix }: { ix: Interaction }) {
  const form = ix.elicitation;
  const url = form?.mode === "url";
  // a card that moves on the page (the one above it answered) is drawn anew: it picks up what was typed and opened
  const [values, setValues] = useState<Values>(() => elicitationDraft(ix.id)?.values ?? defaults(form?.requested_schema));
  const [opened, setOpened] = useState<string | undefined>(() => elicitationDraft(ix.id)?.opened);
  const canApprove = useStore(() => interactionCanAnswer(ix, getSession(ix.machine_id, ix.session_id)));
  useEffect(() => saveElicitationDraft(ix.id, form?.requested_schema, { values, opened }), [ix.id, form?.requested_schema, values, opened]);
  const [busy, setBusy] = useState<ElicitationAction | "dismiss" | null>(null);
  const go = async (body: Record<string, unknown>, key: ElicitationAction | "dismiss") => {
    setBusy(key);
    await resolve(ix, body);
    setBusy(null);
  };
  const act = (action: ElicitationAction) => {
    const body = elicitationResolution(ix, action, values, opened);
    if (body) void go(body, action);
  };
  const set = (key: string, value: unknown) => {
    setValues((cur) => {
      const next = { ...cur };
      if (value === undefined) delete next[key];
      else next[key] = value;
      return next;
    });
  };
  const schema = form?.requested_schema;
  const check = validate(schema, values);
  const canFill = form?.mode === "form" && check.supported;
  const ready = Boolean(elicitationResolution(ix, "accept", values, opened));
  const target = urlTarget(form?.url);
  const source = elicitationSource(ix);
  const hint = "text-[12px] leading-snug text-ink-3";
  // a form answers field by field; of the card's own keys it takes only Esc back to the composer
  const keys = useCardKeys();
  return (
    <Card
      kicker={url ? t("chat-interactions.url_kicker") : t("chat-interactions.form_kicker")}
      title={url ? t("chat-interactions.url_title") : t("chat-interactions.form_title")}
      cardKeys={keys}
      onDismiss={() => go({ dismiss: true }, "dismiss")}
      busy={busy !== null}
      footer={
        <>
          <Button tone="quiet" busy={busy === "cancel"} disabled={busy !== null} onClick={() => act("cancel")}>
            {t("chat-interactions.elicitation_cancel")}
          </Button>
          <span className="flex gap-1.5">
            <Button tone="quiet" busy={busy === "decline"} disabled={busy !== null} onClick={() => act("decline")}>
              {t("chat-interactions.elicitation_decline")}
            </Button>
            {(url || canFill) && (
              <Button tone="primary" busy={busy === "accept"} disabled={busy !== null || !ready} onClick={() => act("accept")}>
                {url ? t("chat-interactions.opened_continue") : t("chat-interactions.submit")}
              </Button>
            )}
          </span>
        </>
      }
    >
      {source.length > 0 && <div className={cx("mb-1.5", hint)}>{source.map((l) => <span key={l} className="block">{l}</span>)}</div>}
      {form?.message?.trim() && (
        <div className="mb-2 text-ink-2">
          <Markdown text={form.message} small />
        </div>
      )}
      {url ? (
        <div className="flex flex-col gap-1.5">
          <div className={hint}>
            <span className="block">{t("chat-interactions.url_server", { server: ix.mcp_server_name?.trim() ? mcpServerLabel(ix.mcp_server_name.trim()) : t("chat-interactions.url_server_unknown") })}</span>
            <span className="block">{t("chat-interactions.url_host", { host: urlHost(form?.url) ?? t("chat-interactions.url_invalid") })}</span>
          </div>
          <code className="rounded-[8px] bg-inset px-2.5 py-2 font-mono text-[11.5px] leading-[1.5] break-all text-ink-2 shadow-hairline">{form?.url}</code>
          <p className={hint}>{t("chat-interactions.url_hint")}</p>
          <div className="flex items-center gap-2">
            {/* a link, not window.open: a click on it is never blocked, and the MCP server's page gets neither a handle on
                this one (noopener) nor the hub's address (noreferrer) */}
            <Button icon="external" href={target} disabled={!target || !canApprove || busy !== null} onClick={() => setOpened(target)}>
              {t("chat-interactions.open_link")}
            </Button>
            {opened && <Icon name="check" size={13} stroke={2.6} className="text-green" />}
          </div>
        </div>
      ) : !canFill ? (
        <p className="text-[12.5px] leading-relaxed text-ink-2">{t("chat-interactions.unsupported_form")}</p>
      ) : (
        <div className="flex flex-col gap-3">
          {fields(schema).map((f) => (
            <ElicitationField key={f.key} field={f} value={values[f.key]} error={check.errors[f.key]} onChange={(v) => set(f.key, v)} />
          ))}
        </div>
      )}
    </Card>
  );
}

const inputType = (f: Field) =>
  f.secret ? "password" : f.type !== "string" ? "text" : ({ email: "email", uri: "url", date: "date" } as Record<string, string>)[String(f.schema.format)] ?? "text";

/** One field of an MCP form: its title ("Required" / "Optional"), what it is for, the control, and what is wrong with it. */
function ElicitationField({ field: f, value, error, onChange }: { field: Field; value: unknown; error?: string; onChange: (v: unknown) => void }) {
  const input = useRef<HTMLInputElement>(null);
  useLayoutEffect(() => {
    const el = input.current;
    return () => {
      if (el) el.value = "";
    };
  }, []);
  const id = useId();
  const text = f.type !== "boolean" && f.choices.length === 0;
  const list = Array.isArray(value) ? value : [];
  const label = f.required ? t("chat-interactions.field_required", { title: f.title }) : t("chat-interactions.field_optional", { title: f.title });
  return (
    <div role={text ? undefined : "group"} aria-labelledby={text ? undefined : `${id}-label`} className="flex flex-col gap-1">
      <label id={`${id}-label`} htmlFor={text ? id : undefined} className="text-[12.5px] font-medium text-ink">{label}</label>
      {f.description && <span className="text-[12px] leading-snug text-ink-3">{f.description}</span>}
      {f.type === "boolean" ? (
        <div className="flex gap-3">
          {([true, false] as const).map((b) => (
            <Choice key={String(b)} on={value === b} label={b ? t("chat-interactions.yes") : t("chat-interactions.no")} onClick={() => onChange(b)} />
          ))}
        </div>
      ) : f.choices.length > 0 ? (
        <div className="flex flex-col gap-0.5">
          {f.choices.map((c, i) => {
            const on = f.type === "array" ? list.includes(c) : value === c;
            const pick = () => onChange(f.type === "array" ? (on ? list.filter((x) => x !== c) : [...list, c]) : c);
            return <Choice key={String(c)} on={on} multi={f.type === "array"} label={f.choiceLabels[i] ?? String(c)} onClick={pick} />;
          })}
        </div>
      ) : (
        <input
          id={id}
          ref={f.secret ? input : undefined}
          type={inputType(f)}
          inputMode={numberInputMode(f)}
          value={value === undefined ? "" : String(value)}
          // an emptied field is not filled in: a required one says so, an optional one is left out
          onChange={(e) => onChange(e.target.value === "" ? undefined : e.target.value)}
          placeholder={f.schema.format === "date-time" ? "2026-01-01T09:00:00Z" : undefined}
          autoComplete={f.secret ? "one-time-code" : "off"}
          autoCapitalize={f.secret ? "off" : undefined}
          autoCorrect={f.secret ? "off" : undefined}
          {...(f.secret ? SECRET_INPUT : {})}
          aria-invalid={error ? true : undefined}
          className={cx(
            "h-8 w-full min-w-0 rounded-control bg-surface px-2.5 text-[13px] text-ink shadow-btn outline-none placeholder:text-ink-3",
            error && "ring-1 ring-red",
          )}
        />
      )}
      {error && <span role="alert" className="text-[12px] text-red">{error}</span>}
      {f.secret && <span className="text-[12px] leading-snug text-ink-3">{t("chat-interactions.secret_note")}</span>}
      {!f.required && value !== undefined && (
        <button type="button" onClick={() => onChange(undefined)} className="touch-hit self-start rounded-[5px] text-[12px] whitespace-nowrap text-ink-3 hover:text-ink">
          {t("chat-interactions.clear_field")}
        </button>
      )}
    </div>
  );
}

/** What an answered card was about, in one line of plain text: what it asked (interactionAsk), a plan's first line;
 *  empty when it says nothing of its own (never its kind's title, "Plan awaits your approval", which would read as still waiting). */
export function receiptAbout(ix: Interaction): string {
  const raw = ix.kind === "plan" ? ((ix.plan ?? "").split("\n").find((l) => l.trim()) ?? "").replace(/^\s*#+\s*/, "") || ix.summary || "" : interactionAsk(ix);
  return plainMarkdown(raw).replace(/\s+/g, " ").trim();
}

/** A card this page answered: what was asked and the picks, read-only, for a moment (lib/store.ts endInteraction). */
export function AnsweredCard({ card }: { card: Answered }) {
  // handed back: its head says so, and the line under it is which request went back to the computer, not those words again
  const handedBack = card.resolution.dismiss === true;
  const lines = handedBack ? [] : answerLines(card.ix, card.resolution);
  // a card with one unlabeled answer ("Allow", "Approve", a terminal option) says what it was about (#4353): which command was
  // allowed, not only that something was
  const about = handedBack || (lines.length === 1 && !lines[0].label) ? receiptAbout(card.ix) : "";
  return (
    <div role="status" data-answered={card.ix.id} className="shrink-0 overflow-hidden rounded-card bg-surface px-3.5 py-2.5 shadow-card" style={{ animation: `fade-up 300ms ${EASE} both` }}>
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0 flex-1">
          <div className="mb-1 flex items-center gap-1.5 text-[11px] font-medium text-green">
            <Icon name="check" size={11} stroke={3} />
            {handedBack ? t("chat-interactions.handed_back") : t("chat-interactions.answered")}
          </div>
          <div className="flex flex-col gap-1">
            {about && <span data-answered-about className="block truncate text-[12px] text-ink-3" title={about}>{about}</span>}
            {lines.map((l, i) => (
              <div key={i} className="min-w-0 text-[13px] leading-snug">
                {l.label && <span className="block truncate text-[12px] text-ink-3">{l.label}</span>}
                <span className="block whitespace-pre-wrap text-ink">{l.answer}</span>
              </div>
            ))}
          </div>
        </div>
        <button
          type="button"
          aria-label={t("chat-interactions.collapse")}
          title={t("chat-interactions.collapse")}
          onClick={() => dropAnswered(card.ix.id)}
          className="touch-hit flex size-6 shrink-0 items-center justify-center rounded-[6px] text-ink-3 transition-colors hover:bg-hover hover:text-ink"
        >
          <Icon name="x" size={14} stroke={2.2} />
        </button>
      </div>
    </div>
  );
}

export function InteractionCard({ ix }: { ix: Interaction }) {
  // a child session's card on its parent's page (#5120): its row is the one the parent's chat holds (a paging hub's
  // summary, which the store's complete rows never have)
  const child = useChildRow(ix.machine_id, ix.session_id);
  const canApprove = useStore(() => interactionCanAnswer(ix, child ?? getSession(ix.machine_id, ix.session_id)));
  // an answer still on its way (the hub away, apiOp retrying) holds the card, also after leaving the chat and coming back
  const { pending, line } = usePendingLine(ix);
  const card = ix.kind === "question" && ix.tool_name === "persona_tool_loop" ? <ToolLoopCard key={ix.id} ix={ix} />
    : ix.kind === "question" ? <QuestionCard ix={ix} />
    : ix.kind === "plan" ? <PlanCard ix={ix} />
    : isSecretRun(ix) ? <SecretRunCard key={ix.id} ix={ix} />
    : isSecretHandoff(ix) ? <SecretHandoffCard key={ix.id} ix={ix} />
    : ix.kind === "permission" ? <PermissionCard ix={ix} />
    : ix.kind === "elicitation" ? <ElicitationCard ix={ix} /> : <ScreenCard ix={ix} />;
  const asked = useVoiceAsking(ix);
  // a permission card says it in its body (PermissionBody); a secret run's or hand-off's card has none, so it says it here
  const from = ix.folded_under && (ix.kind !== "permission" || isSecretRun(ix) || isSecretHandoff(ix)) ? permissionFrom(ix) : "";
  // data-card: which card this is, for the sync scenarios (spec/fixtures/sync); `contents` keeps the layout as it was.
  // The fieldset is always there (switching it in would remount the card): disabled, it stops every answer and the
  // card's keys (lib/card-keys.ts) while the session can't be approved here or an answer is on its way
  return (
    <div data-card={ix.id} className="contents">
      <AskedByVoice.Provider value={asked}>
        <SendingAnswer.Provider value={line}>
          <AskedFrom.Provider value={from}>
            <fieldset disabled={!canApprove || pending} className={canApprove ? "contents" : undefined}>{card}</fieldset>
          </AskedFrom.Provider>
        </SendingAnswer.Provider>
      </AskedByVoice.Provider>
    </div>
  );
}

/**
 * The cards waiting on you, and the ones just answered, above the composer. Wide screens show them all, as always.
 * Narrow ones (#1996) keep them in a box that scrolls on its own: the first in full and "N more" for the others until
 * that is tapped; while the keyboard is up, one line for all of them (tapping it puts the keyboard away).
 */
export function PendingCards({ interactions, answered, delegated = [], offerNext, narrow, short = false, keyboard, onDismissKeyboard }: {
  interactions: Interaction[];
  answered: Answered[];
  /** The questions the assistant answered for the user (#544): shown with the answered ones, until put away. */
  delegated?: DelegatedAnswer[];
  /** This chat: offer what other chats wait on you with (#4091), one line under "Answered" once it has no card left. */
  offerNext?: { mid: string; sid: string };
  narrow: boolean;
  /** A phone on its side: all of them in one line until tapped. */
  short?: boolean;
  keyboard: boolean;
  onDismissKeyboard: () => void;
}) {
  const [open, setOpen] = useState(false);
  const count = interactions.length;
  useEffect(() => {
    if (!count) setOpen(false);
  }, [count]);
  const { full, summary } = cardFold(count, { narrow, keyboard, open, short });
  if (!count && !answered.length && !delegated.length) return null;
  const shown = showsAnswered({ narrow, keyboard, open, short });
  const done = shown ? [...delegated.map((d) => <DelegatedCard key={d.ix.id} d={d} />), ...answered.map((a) => <AnsweredCard key={a.ix.id} card={a} />)] : [];
  const cards = interactions.map((ix) => <InteractionCard key={ix.id} ix={ix} />);
  // only right after answering here (the "Answered" receipt shows), never while this chat still has a card: no jump, no nag
  const onward = !count && shown && answered.length > 0 && offerNext ? <NextWaitingRow {...offerNext} /> : null;
  if (!narrow) return <>{done}{cards}{onward}</>;
  const more = summary?.kind === "more";
  return (
    <div className="flex min-h-0 shrink flex-col gap-2">
      {/* the box clips what is in it: its children keep a little room for their shadows (p-1; -m-1 gives it back) */}
      <div data-chat-cards data-scroll-box className={cx("-m-1 flex min-h-0 shrink flex-col overflow-y-auto overscroll-contain p-1", !done.length && !full && "hidden")}>
        {done.length > 0 && <div className="flex flex-col gap-2">{done}</div>}
        {/* unclipped once open (#3112): a clipping fold is the box a card's sticky footer sticks in, so a card taller than
            data-chat-cards (a long command) left its buttons below the fold, out of sight, with no way to bring them up */}
        {count > 0 && (
          <Collapse open={full >= 1} unclipped>
            <div className={cx(done.length > 0 && "pt-2")}>{cards[0]}</div>
          </Collapse>
        )}
        {count > 1 && (
          <Collapse open={full > 1} unclipped>
            <div className="flex flex-col gap-2 pt-2">{cards.slice(1)}</div>
          </Collapse>
        )}
      </div>
      {/* the line for what is folded stays in view under the box (in it, a tall first card would scroll it away) */}
      {summary && (
        <FoldRow
          label={more ? t("chat-interactions.more_pending", { count: summary.count }) : t("chat-interactions.pending", { count: summary.count })}
          detail={(more ? interactions.slice(1) : interactions).map(cardLine).join(" · ")}
          onClick={summary.kind === "typing" ? onDismissKeyboard : () => setOpen(true)}
        />
      )}
      {onward}
      {open && (count > 1 || short) && !keyboard && (
        <Button tone="quiet" onClick={() => setOpen(false)} className="touch-hit -mt-1 self-center">
          {short ? t("chat-interactions.collapse") : t("chat-interactions.first_only")}
        </Button>
      )}
    </div>
  );
}

/** One line in place of folded cards: how many, what they are, ›. */
function FoldRow({ label, detail, onClick }: { label: string; detail: string; onClick: () => void }) {
  return (
    <button
      type="button"
      data-chat-cards-fold
      // the composer keeps its focus until the click (Android): the row must not move out from under the finger first
      onMouseDown={(e) => e.preventDefault()}
      onClick={onClick}
      className="flex h-11 shrink-0 items-center gap-2 rounded-card bg-surface px-3.5 text-left shadow-card transition-colors hover:bg-hover"
      style={{ animation: `fade-up 250ms ${EASE} both` }}
    >
      <span className="size-1.5 shrink-0 rounded-full bg-attention" />
      <span className="shrink-0 text-[13px] font-medium whitespace-nowrap text-ink">{label}</span>
      <span className="min-w-0 flex-1 truncate text-[12.5px] text-ink-3">{detail}</span>
      <Icon name="chevronRight" size={14} stroke={2.2} className="shrink-0 text-ink-3" />
    </button>
  );
}

/** "N more need you · Next: <agent> · <what it asks> ›", opening that chat with that card first (lib/card-focus.ts).
 *  Mounted only right after an answer here: the cards of other chats coming and going redraw this, not the chat. */
function NextWaitingRow({ mid, sid }: { mid: string; sid: string }) {
  const next = useStore((s) => nextWaiting(s.interactions, mid, sid));
  const session = useStore((s) => (next ? (s.sessions[next.ix.machine_id] ?? []).find((x) => x.session_id === next.ix.session_id) : undefined));
  if (!next) return null;
  const { ix, count } = next;
  // named as "Overview"'s "Needs you" row names it, worded as the folded cards' line (cardLine)
  const title = ix.session_title?.trim() || (session ? sessionTitle(session) : ix.cwd || t("home.session"));
  const ask = plainMarkdown(cardLine(ix)).replace(/\s+/g, " ").trim();
  const to: Route = { name: "session", mid: ix.machine_id, sid: ix.session_id };
  return (
    <a
      href={href(to)}
      data-chat-next-waiting
      onClick={(e) => onLinkClick(e, to, cardState(ix.id))}
      className="flex h-11 shrink-0 items-center gap-2 rounded-card bg-surface px-3.5 shadow-card transition-colors hover:bg-hover"
      style={{ animation: `fade-up 250ms ${EASE} both` }}
    >
      <span className="size-1.5 shrink-0 rounded-full bg-attention" />
      <span className="shrink-0 text-[13px] font-medium whitespace-nowrap text-ink">{t("chat-interactions.next_waiting", { count })}</span>
      <span className="min-w-0 flex-1 truncate text-[12.5px] text-ink-3">{t("chat-interactions.next_waiting_detail", { title, ask })}</span>
      <Icon name="chevronRight" size={14} stroke={2.2} className="shrink-0 text-ink-3" />
    </a>
  );
}
