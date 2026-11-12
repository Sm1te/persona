// What a Persona conversation adds to the chat page (the app's AssistantTopBar): the persona's dot in the header,
// moving with the conversation's state; the header menu's "Details" / "Knowledge" / "Back to main chat" / "Copy info"; the
// main conversation's "Compact context" / "New main chat" and the strip above the composer while it compacts (#1727). Its
// model is the composer's model pill (views/chat/ModelPill.tsx AssistantModelPill, #5033).
import { useEffect, useRef, useState } from "react";
import { api, ApiError, errorText } from "../../lib/api";
import { assistantInfoText, conversationKey, effectiveLauncher, shortModel, usePickedLauncher } from "../../lib/assistant-chat";
import {
  archivedMainTitle, COMPACT_FEATURE, compactBody, compactionView, compactPath, NEW_MAIN_FEATURE, NEW_MAIN_PATH, newMainBody, type CompactionView,
} from "../../lib/compaction";
import { runtimeFallbackText } from "../../lib/model-config";
import { personaState } from "../../lib/persona";
import { conversationPage } from "../../lib/conversation-page";
import { CLEAR_MAIN_FEATURE, clearMainContext } from "../../lib/main-clear";
import { shownManager } from "../../lib/manager-choice";
import { navigate } from "../../lib/router";
import { hubHas, refreshAssistant, useStore } from "../../lib/store";
import type { Item, Session } from "../../lib/types";
import { Button, copyText, Dialog, IconButton, Spinner, toast, type MenuItem } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { ModelFallbackCard } from "../chat/ModelFallbackCard";
import { Switch } from "./parts";
import { PersonaDot, usePersona } from "./Persona";
import { t } from "../../i18n";

// ── the header ──────────────────────────────────────────

/** The persona's dot beside a Persona conversation's title, in the conversation's state; nothing for the page's mark. */
export function AssistantFace({ session, last }: { session?: Session; last?: Pick<Item, "role" | "kind"> }) {
  const persona = usePersona();
  const conn = useStore((s) => s.conn);
  const machineOnline = useStore((s) => s.machines.find((m) => m.id === (session?.machine_id ?? s.assistant?.machine_id))?.online);
  const waiting = useStore((s) => (session ? s.interactions.filter((i) => i.machine_id === session.machine_id && i.session_id === session.session_id).length : 0));
  if (!persona.info && !persona.mochi) return null;
  const state = personaState({ online: conn === "online", machineOnline, state: session?.state, interactions: waiting, last });
  return <PersonaDot size={30} state={state} fallback={null} className="-my-1" />;
}

/** The header menu's Persona part: "Details" (the profile), "Knowledge", "Back to main chat", the main conversation's "Compact context",
 *  "Clear context" and "New main chat" (when the hub can), "Copy info"; `dialogs` goes with them. Empty for an agent. */
export function useAssistantMenu(session: Session | undefined): { items: MenuItem[]; dialogs: React.ReactNode } {
  const assistant = useStore((s) => s.assistant);
  const machine = useStore((s) => (session ? s.machines.find((m) => m.id === session.machine_id)?.name ?? "" : ""));
  const canCompact = useStore((s) => hubHas(s.features, COMPACT_FEATURE));
  const canNewMain = useStore((s) => hubHas(s.features, NEW_MAIN_FEATURE));
  const canClear = useStore((s) => hubHas(s.features, CLEAR_MAIN_FEATURE));
  const picked = usePickedLauncher(session ? conversationKey(session.machine_id, session.session_id) : "");
  const [newMain, setNewMain] = useState(false);
  if (!session) return { items: [], dialogs: null };
  const isMain = assistant?.main === session.session_id;
  const launcher = effectiveLauncher({ isMain, picked: session.model_pick?.launcher || picked, session: session.launcher, config: assistant?.launcher });
  const cwd = session.cwd || assistant?.cwd || "~/.persona/assistant";
  const title = !isMain && session.title && session.title !== "Claude Code" ? session.title : undefined;
  const compacting = session.compaction?.state === "running";
  const items: MenuItem[] = [
    { key: "profile", label: t("assistant-assistantchat.details"), icon: "info", onSelect: () => navigate({ name: "profile" }) },
    { key: "kb", label: t("assistant-assistantchat.knowledge"), icon: "book", onSelect: () => navigate({ name: "kb" }) },
    ...(assistant?.main && !isMain ? [{ key: "main", label: t("assistant-assistantchat.back_to_main"), icon: "home" as const, onSelect: () => navigate(conversationPage(assistant.main!, assistant.machine_id, shownManager())) }] : []),
    ...(isMain && canCompact
      ? [{ key: "compact", label: compacting ? t("assistant-assistantchat.compacting") : t("assistant-assistantchat.compact"), icon: "layers" as const, disabled: compacting, onSelect: () => void startCompaction(session) }]
      : []),
    // as /clear (#5095): no confirmation, the old conversation is archived, not deleted
    ...(isMain && canClear ? [{ key: "clear", label: t("assistant-assistantchat.clear"), icon: "retry" as const, onSelect: () => void clearMainContext(session.session_id) }] : []),
    ...(isMain && canNewMain ? [{ key: "newmain", label: t("assistant-assistantchat.new_main_ellipsis"), icon: "compose" as const, onSelect: () => setNewMain(true) }] : []),
    {
      key: "copyinfo",
      label: t("assistant-assistantchat.copy_info"),
      icon: "copy",
      onSelect: () => void copyText(assistantInfoText({ title, isMain, sessionId: session.session_id, machine, machineId: session.machine_id, cwd, launcher, model: shortModel(session.model) || undefined })),
    },
  ];
  const dialogs = isMain && canNewMain ? <NewMainDialog open={newMain} onClose={() => setNewMain(false)} session={session} /> : null;
  return { items, dialogs };
}

// ── Compact context ─────────────────────────────────────

const COMPACT_REQUESTED = "persona:compact-requested";

/** Asks the hub to compact the conversation; its progress comes on the session (`compaction`). */
async function startCompaction(session: Session) {
  try {
    await api(compactPath(session.session_id), { method: "POST", json: compactBody(session.machine_id) });
    window.dispatchEvent(new CustomEvent(COMPACT_REQUESTED, { detail: { mid: session.machine_id, sid: session.session_id } }));
  } catch (e) {
    // 409: busy (answering), compacting, gone, unsupported — the hub's words say which
    toast(t("assistant-assistantchat.compact_start_failed", { error: errorText(e) }), "error");
  }
}

const dismissedKey = (mid: string, sid: string) => `compact-dismissed:${mid}/${sid}`;
/** How long a done compaction's notice stays. */
const DONE_SHOWN_MS = 4000;
/** How long the strip says "Compacting" after asking, before the hub's word on it arrives. */
const REQUEST_GRACE_MS = 15_000;

/** Above the composer of an assistant conversation: its runtime fallback (#2455), then the compaction strip. */
export function CompactionStrip({ session }: { session: Session }) {
  return (
    <>
      {session.runtime_fallback && <RuntimeFallbackNote reason={session.runtime_fallback} />}
      {session.model_fallback && <AssistantModelFallback session={session} />}
      <Compaction session={session} />
    </>
  );
}

/** The refusal fallback's card in a Persona conversation (#2528): "Back to the configured model", and in the main
 *  conversation the existing "New main chat" too (the same dialog as the menu's). */
function AssistantModelFallback({ session }: { session: Session }) {
  const isMain = useStore((s) => s.assistant?.main === session.session_id);
  const canNewMain = useStore((s) => hubHas(s.features, NEW_MAIN_FEATURE));
  const [open, setOpen] = useState(false);
  const offer = isMain && canNewMain;
  return (
    <>
      <ModelFallbackCard session={session} headless onNewMain={offer ? () => setOpen(true) : undefined} />
      {offer && <NewMainDialog open={open} onClose={() => setOpen(false)} session={session} />}
    </>
  );
}

/** The conversation asked for the Agent SDK and runs on the CLI: why (no Node.js 22+, no bridge, no claude). */
export function RuntimeFallbackNote({ reason }: { reason: string }) {
  const text = runtimeFallbackText(reason);
  return (
    <div role="status" title={text} className="flex min-h-8 items-center gap-2 rounded-[12px] bg-orange-tint px-3 py-1 text-[12px] text-orange">
      <Icon name="warning" size={12} stroke={2.2} className="shrink-0" />
      <span className="min-w-0 flex-1 truncate">{text}</span>
    </div>
  );
}

/** Above the composer while the conversation compacts ("Compacting context…"), briefly once it is done ("Context compacted"), and a
 *  failure until it is closed (lib/compaction.ts compactionView: only what this page saw change, or just now). */
function Compaction({ session }: { session: Session }) {
  const mid = session.machine_id;
  const sid = session.session_id;
  const c = session.compaction;
  const sig = c ? `${c.started_at}|${c.state}|${c.finished_at ?? ""}|${c.error ?? ""}` : "";
  const prev = useRef<NonNullable<typeof c>["state"] | undefined>(undefined);
  const [view, setView] = useState<CompactionView>(null);
  // asked here and the hub has not said anything new yet: already compacting
  const [requested, setRequested] = useState<{ sig: string; at: number } | null>(null);

  useEffect(() => {
    const v = compactionView(c, prev.current, Date.now(), localStorage.getItem(dismissedKey(mid, sid)) ?? undefined);
    prev.current = c?.state;
    setView(v);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [mid, sid, sig]);

  useEffect(() => {
    const on = (e: Event) => {
      const d = (e as CustomEvent<{ mid: string; sid: string }>).detail;
      if (d.mid === mid && d.sid === sid) setRequested({ sig, at: Date.now() });
    };
    window.addEventListener(COMPACT_REQUESTED, on);
    return () => window.removeEventListener(COMPACT_REQUESTED, on);
  }, [mid, sid, sig]);
  useEffect(() => {
    if (!requested) return;
    if (requested.sig !== sig) {
      setRequested(null);
      return;
    }
    const timer = setTimeout(() => setRequested(null), REQUEST_GRACE_MS);
    return () => clearTimeout(timer);
  }, [requested, sig]);
  useEffect(() => {
    if (view?.kind !== "done") return;
    const timer = setTimeout(() => setView(null), DONE_SHOWN_MS);
    return () => clearTimeout(timer);
  }, [view]);

  const shown: CompactionView = requested ? { kind: "running" } : view;
  if (!shown) return null;
  const dismiss = () => {
    if (shown.kind === "failed") localStorage.setItem(dismissedKey(mid, sid), shown.key);
    setView(null);
  };
  return (
    <div role="status" className="flex min-h-9 items-center gap-2 rounded-[12px] bg-surface px-3 py-1.5 text-[12.5px] text-ink-2 shadow-card" style={{ animation: "fade-up 200ms ease-out both" }}>
      {shown.kind === "running" ? (
        <>
          <Spinner size={12} />
          <span className="min-w-0 flex-1 truncate">{t("assistant-assistantchat.compacting")}</span>
        </>
      ) : shown.kind === "done" ? (
        <>
          <Icon name="check" size={13} stroke={2.2} className="shrink-0 text-green" />
          <span className="min-w-0 flex-1 truncate">{t("assistant-assistantchat.compacted")}</span>
        </>
      ) : (
        <>
          <Icon name="info" size={13} className="shrink-0 text-red" />
          <span className="min-w-0 flex-1 break-words">{t("assistant-assistantchat.compact_failed", { error: shown.error })}</span>
          <IconButton icon="x" label={t("assistant-assistantchat.close")} onClick={dismiss} size={24} iconSize={13} />
        </>
      )}
    </div>
  );
}

// ── New main chat ───────────────────────────────────────

/** Archives the main conversation (renamed "Old main chat · until <date>") and leaves none: the next message starts the new one. */
function NewMainDialog({ open, onClose, session }: { open: boolean; onClose: () => void; session: Session }) {
  const [carry, setCarry] = useState(true);
  const [busy, setBusy] = useState(false);
  const toEmptyMain = async () => {
    await refreshAssistant().catch(() => {});
    onClose();
    navigate({ name: "persona" });
  };
  const go = async () => {
    setBusy(true);
    try {
      const r = await api<{ ok: boolean; archived?: string; carried?: boolean }>(NEW_MAIN_PATH, { method: "POST", json: newMainBody(carry, session.session_id) });
      await toEmptyMain();
      toast(r.carried ? t("assistant-assistantchat.new_main_done_carried") : t("assistant-assistantchat.new_main_done"), "ok");
    } catch (e) {
      if (e instanceof ApiError && e.code === "main_changed") {
        // another device replaced it: show what the hub has now
        await toEmptyMain();
        toast(t("assistant-assistantchat.new_main_elsewhere"), "info");
      } else {
        toast(t("assistant-assistantchat.new_main_failed", { error: errorText(e) }), "error");
      }
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog
      open={open}
      onClose={onClose}
      title={t("assistant-assistantchat.new_main_title")}
      footer={
        <>
          <Button tone="quiet" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button tone="primary" busy={busy} onClick={() => void go()}>
            {t("assistant-assistantchat.new_main")}
          </Button>
        </>
      }
    >
      <p className="text-[13px] leading-relaxed text-ink-2">
        {t("assistant-assistantchat.new_main_body", { title: archivedMainTitle() })}
      </p>
      <div className="mt-3 flex items-start gap-3 rounded-control bg-inset px-3 py-2.5 shadow-hairline">
        <div className="min-w-0 flex-1">
          <div className="text-[13px] font-medium text-ink">{t("assistant-assistantchat.carry_summary")}</div>
          <div className="mt-0.5 text-[12px] leading-relaxed text-ink-3">{t("assistant-assistantchat.carry_summary_hint")}</div>
        </div>
        <Switch checked={carry} onChange={setCarry} label={t("assistant-assistantchat.carry_summary")} disabled={busy} />
      </div>
    </Dialog>
  );
}
