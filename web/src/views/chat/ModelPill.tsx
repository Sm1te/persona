// The composer's model pill (#5032, after Cindy's model selector, which the user asked this part of the chat to match;
// docs/web/web.md "Model pill and model panel", docs/design/DESIGN_SYSTEM.md 3.1): at the right of the composer's bottom
// row, before voice / Stop / Send, `[provider][model][engine][effort][Fast][⌄]`. It opens the unified model panel above
// it (ui/model-panel/ModelPanel.tsx), and so does `/model` Enter in the composer. The composer card is a container: under
// 600 px the pill is 148 px wide, under 520 px its text and marks are a step smaller, under 420 px it keeps only the
// provider's tile and the chevron (64 px). An agent that can switch neither its model nor its effort gets the same
// words as a label, not a button. A Persona chat (and the new-chat page) has `AssistantModelPill` (#5033): the same pill
// over what the assistant's computer can run Persona chats on, its panel saying whether the chat follows the default.
import { memo, useCallback, useEffect, useRef, useState, type ReactNode, type Ref } from "react";
import { t } from "../../i18n";
import { agentName } from "../../lib/agent-kind";
import { onModelPicker, sessionLauncher } from "../../lib/model-switch";
import { effortTitle } from "../../lib/session-actions";
import { dropPick, launcherOffered, usePickedEffort, usePickedLauncher, usePickedModel } from "../../lib/assistant-chat";
import { ASSISTANT_MODEL_PICK_FEATURE, assistantPickKey, assistantPillView, assistantSetup, type AssistantChat } from "../../lib/assistant-models";
import { assistantLaunchers, modelList, pendingView, pillView, readAssistantLaunchers, readForPanel, readModels, sessionSwitches, useModelLists, type PillSetup } from "../../lib/session-models";
import { hubHas, useStore } from "../../lib/store";
import { CODEX_ULTRA, ultracodeOn } from "../../lib/ultracode";
import type { Session } from "../../lib/types";
import { AgentMark } from "../../ui/AgentMark";
import { cx, Spinner, toast } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { ProviderMark } from "../../ui/model-panel/ProviderMark";
import { lazyOverlay } from "../LazyOverlay";
import { UltraMark } from "./Ultracode";

// the panel (and what it is built from) loads on the pill's first tap, not with every chat's composer
const SessionModelPanel = lazyOverlay(() => import("./SessionModelPanel").then((m) => m.SessionModelPanel));

/** The effort as the pill says it: English shortens "Very high" to "Extra" here only (the tooltip and the panel keep the
 *  full name); other languages say it in full. */
export function pillEffort(id: string): string {
  return id === "xhigh" ? t("chat-modelpill.effort_xhigh_short") : effortTitle(id);
}

// the composer card's widths (Composer.tsx `@container/composer`): narrow < 600, dense < 520, ultra < 420
const ULTRA_HIDDEN = "@max-[420px]/composer:hidden";

export function ModelPill({
  current,
  pending,
  loading,
  failed,
  disabled,
  open,
  onOpen,
  workflow,
  buttonRef,
}: {
  current: PillSetup;
  /** A switch accepted for the next message (#5035): a clock first, the launcher it moves to named (the engine, when
   *  that changes) and drawn, the tooltip both setups. */
  pending?: PillSetup | null;
  /** The model list is read for the first time. */
  loading?: boolean;
  /** Reading the model list failed. */
  failed?: boolean;
  /** A switch is on its way: dimmed, takes no clicks. */
  disabled?: boolean;
  open?: boolean;
  /** Opens (or closes) the panel at the pill; absent: the pill is a label. */
  onOpen?: (anchor: HTMLElement) => void;
  /** The workflow mark after the effort (ultracode on, a Codex thread on its "ultra" effort; #3428). */
  workflow?: { label?: string; title?: string };
  buttonRef?: Ref<HTMLButtonElement>;
}) {
  const shown = pending ?? current;
  const unknown = !shown.model;
  const name = shown.model ?? (loading ? t("chat-modelpill.loading") : failed ? t("chat-modelpill.failed") : t("chat-modelpill.default_model"));
  const describe = (x: PillSetup) =>
    [agentName(x.engine), x.model ?? name, x.provider, x.effort && effortTitle(x.effort), x.fast && t("chat-sessiondialogs.speed_fast")].filter(Boolean).join(" · ");
  const title = pending
    ? t("chat-modelpill.current_and_next", { current: describe(current), next: describe(pending) })
    : [describe(current), failed && !unknown ? t("chat-modelpill.failed") : ""].filter(Boolean).join(" · ");
  const effort = shown.effort ? effortTitle(shown.effort) : "";
  // what the next message runs on, before its model: the engine when it changes (Cindy's case), else the launcher
  // (Persona's in-session switch, #5035)
  const target = pending ? (pending.engine !== current.engine ? agentName(pending.engine) : pending.provider) : "";
  const label = pending
    ? t(effort ? "chat-modelpill.aria_pending_effort" : "chat-modelpill.aria_pending", { agent: target, model: name, effort })
    : t(effort ? "chat-modelpill.aria_effort" : "chat-modelpill.aria", { model: name, effort });

  const body: ReactNode = (
    <>
      {pending && <Icon name="clock" stroke={2} className="size-[12px] shrink-0 text-ink-3 @max-[520px]/composer:size-[11px]" />}
      {unknown && loading && <Spinner size={13} />}
      {unknown && failed && !loading && <Icon name="warning" stroke={2} className="size-[13px] shrink-0 text-red @max-[520px]/composer:size-[12px]" />}
      <ProviderMark name={shown.provider} className={cx("mr-1.5", failed && !unknown ? "text-red" : "text-ink-2")} />
      {pending && (
        <>
          <span data-pill-target className="max-w-[96px] shrink-0 truncate text-ink-3 @max-[600px]/composer:hidden">{target}</span>
          <span aria-hidden className="shrink-0 text-ink-3 @max-[600px]/composer:hidden">·</span>
        </>
      )}
      {/* the name gives way first (a far larger shrink than the effort's): narrow, the effort truncates only once the
          name has no room left */}
      <span title={name} className={cx("min-w-0 shrink-[1000] truncate text-ink @max-[600px]/composer:max-w-[108px]", ULTRA_HIDDEN)}>{name}</span>
      {/* the engine's mark is the separator before the effort: no "·" */}
      <span aria-hidden data-pill-engine={shown.engine} className={cx("ml-1 flex shrink-0 items-center @max-[520px]/composer:scale-[0.917]", ULTRA_HIDDEN)}>
        <AgentMark agent={shown.engine} size={12} decorative className="text-ink-2" />
      </span>
      {shown.effort && (
        <span title={effort} data-pill-effort className={cx("shrink-0 whitespace-nowrap text-ink @max-[600px]/composer:min-w-0 @max-[600px]/composer:shrink @max-[600px]/composer:truncate", ULTRA_HIDDEN)}>
          {pillEffort(shown.effort)}
        </span>
      )}
      {shown.fast && <Icon name="bolt" stroke={2} className={cx("ml-0.5 size-[13px] shrink-0 fill-current text-ink-2 @max-[520px]/composer:size-[12px]", ULTRA_HIDDEN)} />}
      {workflow && !pending && <UltraMark size={12} label={workflow.label} title={workflow.title} className={cx("ml-0.5", ULTRA_HIDDEN)} />}
      {!unknown && loading && <span className="ml-0.5 flex shrink-0"><Spinner size={12} /></span>}
      {!unknown && failed && !loading && <Icon name="warning" stroke={2} className="ml-0.5 size-[12px] shrink-0 text-red @max-[520px]/composer:size-[11px]" />}
      {onOpen && <Icon name="chevron" stroke={2} className="size-[14px] shrink-0 text-ink-2 @max-[520px]/composer:size-[13px]" />}
    </>
  );
  const shell = cx(
    // Cindy's bare toolbar pill: no frame at rest, the capsule shows on hover and while its panel is open
    "flex h-[30px] max-w-[min(320px,100%)] min-w-[72px] shrink items-center rounded-full border border-transparent px-2.5 text-[13px] leading-none whitespace-nowrap transition-colors duration-150",
    "@max-[600px]/composer:w-[148px] @max-[520px]/composer:text-[12px] @max-[520px]/composer:[--provider-mark:14.2px] @max-[420px]/composer:min-w-[64px]",
    // under 420 px the tile and the chevron: 64 px, as the pill always was there; only a waiting switch's clock before
    // them makes it grow to hold it
    pending ? "@max-[420px]/composer:w-auto" : "@max-[420px]/composer:w-[64px]",
  );
  // the clip is inside, so the touch target (touch-hit) can reach past the pill's 30 px
  const inner = "flex min-w-0 flex-1 items-center gap-1 overflow-hidden";
  if (!onOpen) {
    return (
      <span data-model-pill="label" title={title} className={shell}>
        <span className="sr-only">{label}</span>
        <span aria-hidden className={inner}>{body}</span>
      </span>
    );
  }
  return (
    <button
      ref={buttonRef}
      type="button"
      data-model-pill={pending ? "pending" : failed ? "failed" : unknown && loading ? "loading" : "ready"}
      aria-haspopup="listbox"
      aria-expanded={Boolean(open) && !disabled}
      aria-label={label}
      title={title}
      disabled={disabled}
      // the caret stays in the message box (Tab still focuses the pill)
      onMouseDown={(e) => e.preventDefault()}
      onClick={(e) => onOpen(e.currentTarget)}
      className={cx(
        shell,
        "touch-hit hover:border-line-strong hover:bg-hover focus-visible:rounded-full! aria-expanded:border-line-strong aria-expanded:bg-hover",
        disabled && "pointer-events-none opacity-50",
      )}
    >
      <span className={inner}>{body}</span>
    </button>
  );
}

/** An Agent's pill: what the session runs, and its panel. Loads the session's own model list once, for the name its list
 *  gives the model and to say when that list can't be read. */
export const AgentModelPill = memo(function AgentModelPill({ session }: { session: Session }) {
  useModelLists();
  const mid = session.machine_id;
  const own = sessionLauncher(session);
  const switches = sessionSwitches(session);
  const opens = switches.model || switches.effort;
  useEffect(() => {
    if (opens) void readModels(mid, own, { first: true });
  }, [opens, mid, own]);
  const [anchor, setAnchor] = useState<HTMLElement | null>(null);
  const button = useRef<HTMLButtonElement>(null);
  const key = `${mid}/${session.session_id}`;
  // `/model` Enter in the composer opens it where the agent switches models (the composer sends the text otherwise)
  useEffect(() => (switches.model ? onModelPicker(key, () => setAnchor(button.current)) : undefined), [switches.model, key]);
  useEffect(() => {
    if (!opens) setAnchor(null);
  }, [opens]);
  const close = useCallback(() => setAnchor(null), []);
  const shown = Boolean(anchor) && opens;
  // every list again behind the one shown, once per opening (while the panel's code loads, the first time)
  useEffect(() => {
    if (shown) readForPanel(mid, own);
  }, [shown, mid, own]);

  // a switch to another launcher that the next message carries out (#5035): its list names the model picked
  const target = session.pending_switch?.launcher;
  useEffect(() => {
    if (target && target !== own) void readModels(mid, target);
  }, [target, own, mid]);

  const view = pillView(session, modelList(mid, own));
  const pending = pendingView(session, (name) => modelList(mid, name));
  if (!opens && !view.model) return null;
  const workflow = ultracodeOn(session) ? {} : view.engine === "codex" && session.effort === CODEX_ULTRA ? { label: t("ultracode.codex_ultra_title"), title: t("ultracode.codex_ultra_title") } : undefined;
  return (
    <>
      <ModelPill
        current={view}
        pending={pending}
        loading={view.loading}
        failed={view.failed}
        open={Boolean(anchor)}
        onOpen={opens ? (el) => setAnchor((a) => (a ? null : el)) : undefined}
        workflow={workflow}
        buttonRef={button}
      />
      {shown && anchor && <SessionModelPanel session={session} anchor={anchor} onClose={close} />}
    </>
  );
});

const AssistantModelPanel = lazyOverlay(() => import("./AssistantModelPanel").then((m) => m.AssistantModelPanel));

/** A Persona chat's pill (#5033): the launcher, model and effort its next message runs on (its own pick, the default, or
 *  what it started with: lib/assistant-models.ts), opening the unified panel (scope "assistant"). `session` absent: the
 *  new-conversation page, whose pick goes with the start. Persona chats switch whatever their state: the hub applies a
 *  pick from the next message. */
export const AssistantModelPill = memo(function AssistantModelPill({ session }: { session?: Session }) {
  useModelLists();
  const assistant = useStore((s) => s.assistant);
  const kept = useStore((s) => hubHas(s.features, ASSISTANT_MODEL_PICK_FEATURE));
  const mid = session?.machine_id ?? assistant?.machine_id ?? "";
  const chat: AssistantChat = { session, isMain: Boolean(session && assistant?.main === session.session_id), kept, config: assistant?.launcher };
  const key = assistantPickKey(chat);
  // this page's picks (an older hub, the new-conversation page) draw it again
  const picked = usePickedLauncher(key);
  usePickedModel(key);
  usePickedEffort(key);
  const { info } = assistantLaunchers(mid);
  // the computer's default and what can run a Persona chat: once for the pill, again behind it on each opening
  useEffect(() => {
    if (mid) void readAssistantLaunchers(mid);
  }, [mid]);
  // a pick of this page's that the list no longer offers (gone, or it can't run the assistant) goes back to the default,
  // its model and effort with it: on the new-conversation page they would otherwise go with the start
  useEffect(() => {
    if (!picked || (session && kept) || launcherOffered(info?.launchers ?? null, picked)) return;
    dropPick(key);
    toast(t("chat-modelpill.pick_dropped", { launcher: picked }), "info");
  }, [key, picked, info, session, kept]);
  const setup = assistantSetup(chat, info);
  useEffect(() => {
    if (mid) void readModels(mid, setup.provider, { first: true });
  }, [mid, setup.provider]);
  const [anchor, setAnchor] = useState<HTMLElement | null>(null);
  const button = useRef<HTMLButtonElement>(null);
  const close = useCallback(() => setAnchor(null), []);
  const shown = Boolean(anchor) && Boolean(mid);
  // every list again behind the one shown, once per opening
  useEffect(() => {
    if (!shown) return;
    void readModels(mid, setup.provider, { again: true, first: true });
    void readAssistantLaunchers(mid, { again: true }).then(() => {
      for (const l of assistantLaunchers(mid).info?.launchers ?? []) if (l.name !== setup.provider) void readModels(mid, l.name, { again: true });
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps -- once per opening
  }, [shown, mid]);
  if (!mid) return null;
  const view = assistantPillView(chat, setup, info, modelList(mid, setup.provider));
  const workflow = view.engine === "codex" && view.effort === CODEX_ULTRA ? { label: t("ultracode.codex_ultra_title"), title: t("ultracode.codex_ultra_title") } : undefined;
  return (
    <>
      <ModelPill current={view} loading={view.loading} failed={view.failed} open={Boolean(anchor)} onOpen={(el) => setAnchor((a) => (a ? null : el))} workflow={workflow} buttonRef={button} />
      {shown && anchor && <AssistantModelPanel chat={chat} mid={mid} anchor={anchor} onClose={close} />}
    </>
  );
});
