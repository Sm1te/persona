// What a Persona conversation adds to the chat page (the app's AssistantTopBar): the persona's dot in the header,
// moving with the conversation's state; the header menu's "Details" / "Knowledge" / "Back to main chat" / "Copy info"; and the
// launcher chip under the composer that switches the model for the next messages (AssistantTopBar's "Launcher" menu); the
// main conversation's "Compact context" / "New main chat" and the strip above the composer while it compacts (#1727).
import { useEffect, useRef, useState } from "react";
import { api, ApiError, errorText } from "../../lib/api";
import {
  assistantInfoText, conversationKey, effectiveLauncher, NEW_CONVERSATION, pickLauncher, pickModel, launcherOffered, runsAssistant, shortModel, usePickedLauncher,
  usePickedModel, type AssistantLauncher,
} from "../../lib/assistant-chat";
import {
  archivedMainTitle, COMPACT_FEATURE, compactBody, compactionView, compactPath, NEW_MAIN_FEATURE, NEW_MAIN_PATH, newMainBody, type CompactionView,
} from "../../lib/compaction";
import {
  defaultLabel, forgetCustomModel, launcherModels, machineLaunchers, modelName, oneOff, takesTypedModel, type AgentModel, type LauncherDefault,
} from "../../lib/launcher-models";
import { modelIdRule, TYPED_MODELS_FEATURE, typedModelProblem } from "../../lib/model-id";
import { runtimeFallbackText } from "../../lib/model-config";
import { personaState } from "../../lib/persona";
import { conversationPage } from "../../lib/conversation-page";
import { CLEAR_MAIN_FEATURE, clearMainContext } from "../../lib/main-clear";
import { shownManager } from "../../lib/manager-choice";
import { navigate } from "../../lib/router";
import { hubHas, refreshAssistant, useStore } from "../../lib/store";
import type { Item, Session } from "../../lib/types";
import { Button, copyText, Dialog, IconButton, Menu, Spinner, toast, useMenu, type MenuItem } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { ModelFallbackCard } from "../chat/ModelFallbackCard";
import { Chip } from "../chat/SessionDialogs";
import { Switch } from "./parts";
import { PersonaDot, usePersona } from "./Persona";
import { t } from "../../i18n";

// ── launchers ───────────────────────────────────────────

const cache = new Map<string, { at: number; list: AssistantLauncher[]; def?: LauncherDefault | null }>();

/** The launchers the assistant's computer can run it with (Claude Code or Codex), fetched when a menu opens, and
 *  the main chat's "Default: …" from the model configuration (`?purpose=assistant`, #2455; absent from an older hub). */
function useAssistantLaunchers(mid: string | undefined, wanted: boolean): { list: AssistantLauncher[] | null; def?: LauncherDefault | null; error: string } {
  const hit = mid ? cache.get(mid) : undefined;
  const [list, setList] = useState<AssistantLauncher[] | null>(hit?.list ?? null);
  const [def, setDef] = useState<LauncherDefault | null | undefined>(hit?.def);
  const [error, setError] = useState("");
  useEffect(() => {
    if (!mid || !wanted) return;
    const c = cache.get(mid);
    if (c && Date.now() - c.at < 60_000) {
      setList(c.list);
      setDef(c.def);
      return;
    }
    let live = true;
    machineLaunchers(mid, { purpose: "assistant" })
      .then((r) => {
        const l = (r.launchers ?? []).filter(runsAssistant);
        cache.set(mid, { at: Date.now(), list: l, def: r.default });
        if (live) {
          setList(l);
          setDef(r.default);
          setError("");
        }
      })
      .catch((e) => live && setError(errorText(e)));
    return () => {
      live = false;
    };
  }, [mid, wanted]);
  return { list, def, error };
}

/** The launchers after the menu's "Default: …"; `picked`, the one chosen over the default, is checked and marked "This time only". */
function launcherItems(list: AssistantLauncher[] | null, error: string, picked: string | undefined, onPick: (name: string) => void): MenuItem[] {
  if (!list) return [{ key: "loading", label: error ? t("assistant-assistantchat.launchers_failed", { error }) : t("assistant-assistantchat.launchers_loading"), disabled: true, onSelect: () => {} }];
  if (!list.length) return [{ key: "none", label: t("assistant-assistantchat.launchers_none"), disabled: true, onSelect: () => {} }];
  return list.map((l) => ({
    key: l.name, label: l.name, hint: [l.name === picked && oneOff(), shortModel(l.model)].filter(Boolean).join(" · ") || undefined,
    checked: l.name === picked, onSelect: () => onPick(l.name),
  }));
}

/** The composer's "Launcher" chip in a Persona conversation: the next message (and the ones after it) uses the pick.
 *  Picking a launcher opens its model submenu in the same menu. */
export function AssistantLauncherChip({ session }: { session: Session }) {
  const menu = useMenu();
  const assistant = useStore((s) => s.assistant);
  const key = conversationKey(session.machine_id, session.session_id);
  const picked = usePickedLauncher(key);
  const pickedM = usePickedModel(key);
  // `locked`: why a pick doesn't reach this launcher's agent (it passes its own --model)
  const [modelsFor, setModelsFor] = useState<{ name: string; options: AgentModel[]; loading: boolean; locked?: string } | null>(null);
  const [knownModels, setKnownModels] = useState<{ machine: string; name: string; options: AgentModel[] } | null>(null);
  const isMain = assistant?.main === session.session_id;
  const { list, def, error } = useAssistantLaunchers(session.machine_id, isMain || Boolean(menu.anchor));
  // A launcher the list no longer offers (gone, or one that can't run the assistant) is not what the next message runs
  // on: the hub moves a conversation off it to the main chat's (assistant/launcher_mainchat.go), and refuses a pick of it.
  const own = launcherOffered(list, session.launcher) ? session.launcher : undefined;
  useStalePickDropped(key, picked, list);
  const current = effectiveLauncher({ isMain, picked, session: own, config: (isMain && def?.provider) || assistant?.launcher });
  // With no pick the main chat runs the model configuration's default; another conversation keeps the launcher it started with.
  const fallback = isMain ? def ?? { provider: assistant?.launcher } : { provider: own || assistant?.launcher, model: own ? session.model : undefined };
  const defaultModel = isMain
    ? def?.model || (own === current ? session.model : list?.find((l) => l.name === current)?.model)
    : (own === current || (!own && !session.launcher) ? session.model : list?.find((l) => l.name === current)?.model);
  const modelId = pickedM ?? (picked ? list?.find((l) => l.name === picked)?.model : defaultModel);
  const named = knownModels?.machine === session.machine_id && knownModels.name === current ? knownModels.options.find((m) => m.id === modelId) : undefined;
  const model = named ? modelName(named) : shortModel(modelId);
  const label = [current, picked ? (pickedM ? `${model} · ${oneOff()}` : oneOff()) : model].filter(Boolean).join(" · ");

  // Picking the launcher already applies; the submenu only offers that launcher's models.
  const openModels = (name: string) => {
    pickLauncher(key, name);
    setModelsFor({ name, options: [], loading: true });
    void launcherModels(session.machine_id, name)
      .then((r) => {
        const options = r?.models ?? [];
        setModelsFor((cur) => (cur?.name === name ? { name, options, loading: false, locked: r?.locked } : cur));
        setKnownModels({ machine: session.machine_id, name, options });
      })
      .catch(() => setModelsFor((cur) => (cur?.name === name ? { name, options: [], loading: false } : cur)));
  };
  const pick = (name: string, id: string | undefined) => {
    pickModel(key, id);
    toast(id ? t("assistant-assistantchat.using_model", { launcher: name, model: id }) : t("assistant-assistantchat.using_default_model", { launcher: name }), "ok");
  };
  // Forget an ID typed before (#1644): off the submenu at once, back when the hub says no.
  const forget = (name: string, id: string) => {
    const drop = (options: AgentModel[]) => options.filter((m) => m.id !== id);
    const before = modelsFor?.name === name ? modelsFor.options : knownModels?.name === name ? knownModels.options : [];
    setModelsFor((cur) => (cur?.name === name ? { ...cur, options: drop(cur.options) } : cur));
    setKnownModels((cur) => (cur?.name === name ? { ...cur, options: drop(cur.options) } : cur));
    forgetCustomModel(session.machine_id, name, id).catch((e) => {
      setModelsFor((cur) => (cur?.name === name ? { ...cur, options: before } : cur));
      setKnownModels((cur) => (cur?.name === name && cur.machine === session.machine_id ? { ...cur, options: before } : cur));
      toast(t("assistant-assistantchat.forget_failed", { id, error: errorText(e) }), "error");
    });
  };
  // the launchers whose agent takes a typed model ID (#1644): Claude Code and Codex
  const typable = (name: string) => takesTypedModel(list?.find((l) => l.name === name)?.agent || "claude");
  // the hub keeps a typed pick for the next message; an older one dropped it without a word
  const keepsTyped = useStore((s) => hubHas(s.features, TYPED_MODELS_FEATURE));

  const items: MenuItem[] = modelsFor
    ? [
        { key: "back", label: t("assistant-assistantchat.back_to_launchers"), onSelect: () => setModelsFor(null), keepOpen: true },
        { key: "default", label: t("assistant-assistantchat.default_model"), radio: true, checked: !pickedM, onSelect: () => pick(modelsFor.name, undefined) },
        ...(modelsFor.loading
          ? [{ key: "loading", label: t("assistant-assistantchat.models_loading"), disabled: true, onSelect: () => {} }]
          : modelItems(modelsFor.name, modelsFor.options, modelsFor.locked)),
      ]
    : [
        { key: "default", label: defaultLabel(fallback), checked: !picked, onSelect: () => pickLauncher(key, undefined) },
        ...launcherItems(list, error, picked, openModels).map((it) => ({ ...it, submenu: true })),
      ];

  /** A launcher's models: its own, then the IDs typed before under their heading (#1644), "Other model…" (a field for an
   *  ID, checked with the typed pick when the list doesn't have it; where the hub keeps one, and why not when the
   *  launcher passes its own --model), and a submenu to forget the typed ones. */
  function modelItems(name: string, options: AgentModel[], locked?: string): MenuItem[] {
    const row = (m: AgentModel): MenuItem => ({
      key: m.id,
      label: modelName(m),
      detail: m.description,
      radio: true,
      // the launcher's own default entry picks the same as "Default model" above, which carries the check then
      checked: pickedM === m.id,
      onSelect: () => pick(name, m.default ? undefined : m.id),
    });
    const own = options.filter((m) => !m.custom);
    const typed = options.filter((m) => m.custom);
    if (!typable(name)) return own.map(row);
    const unlisted = pickedM && picked === name && !options.some((m) => m.id === pickedM) ? pickedM : "";
    const other: MenuItem[] = !keepsTyped
      ? []
      : locked
        ? [{ key: "locked", label: locked, note: true, divider: true, onSelect: () => {} }]
        : [{
            key: "other",
            label: t("assistant-assistantchat.other_model"),
            icon: "keyboard" as const,
            divider: true,
            hint: unlisted || undefined,
            radio: true,
            checked: Boolean(unlisted),
            onSelect: () => {},
            rename: { value: "", placeholder: t("assistant-assistantchat.model_id_placeholder"), validate: typedModelProblem, hint: modelIdRule(), commit: (id: string) => pick(name, id) },
          }];
    return [
      ...own.map(row),
      ...(typed.length ? [{ key: "typed", label: t("assistant-assistantchat.typed_before"), heading: true, divider: true, onSelect: () => {} }] : []),
      // the heading says what they are: no line under each, and the ID as it was typed
      ...typed.map((m) => ({ ...row(m), detail: undefined, mono: true })),
      ...other,
      ...(typed.length
        ? [{
            key: "forget",
            label: t("assistant-assistantchat.forget_typed"),
            icon: "trash" as const,
            onSelect: () => {},
            children: typed.map((m) => ({ key: `forget:${m.id}`, label: m.id, mono: true, onSelect: () => forget(name, m.id) })),
          }]
        : []),
    ];
  }

  return (
    <>
      <Chip label={label} phoneIcon="agent" onClick={menu.open} active={Boolean(menu.anchor)} title={t("assistant-assistantchat.launcher_chip_title")} />
      {menu.anchor && (
        <Menu
          anchor={menu.anchor}
          onClose={() => {
            menu.close();
            setModelsFor(null);
          }}
          side="top"
          width={280}
          header={modelsFor ? t("assistant-assistantchat.models_header", { launcher: modelsFor.name }) : t("assistant-assistantchat.from_next_message")}
          items={items}
        />
      )}
    </>
  );
}

/** Drops a launcher picked under key that the list no longer offers (gone, or one that can't run the assistant), saying so. */
function useStalePickDropped(key: string, picked: string | undefined, list: AssistantLauncher[] | null) {
  useEffect(() => {
    if (!picked || launcherOffered(list, picked)) return;
    pickLauncher(key, undefined);
    toast(t("assistant-assistantchat.pick_dropped", { launcher: picked }), "info");
  }, [key, picked, list]);
}

/** The new-conversation page's chip: what the conversation starts with (the default unless picked). */
export function StarterLauncherChip() {
  const menu = useMenu();
  const assistant = useStore((s) => s.assistant);
  const picked = usePickedLauncher(NEW_CONVERSATION);
  const current = effectiveLauncher({ isNew: true, picked, config: assistant?.launcher });
  const { list, def, error } = useAssistantLaunchers(assistant?.machine_id, true);
  useStalePickDropped(NEW_CONVERSATION, picked, list);
  if (!assistant) return null;
  const label = defaultLabel(def ?? { provider: assistant.launcher });
  return (
    <>
      <Chip label={t("main-runtime.new_launcher", { launcher: picked ? `${current} · ${oneOff()}` : label })} onClick={menu.open} active={Boolean(menu.anchor)} title={t("assistant-assistantchat.new_chat_launcher_title")} />
      {menu.anchor && (
        <Menu
          anchor={menu.anchor}
          onClose={menu.close}
          side="top"
          width={280}
          header={t("assistant-assistantchat.new_chat_uses")}
          items={[
            { key: "default", label, checked: !picked, onSelect: () => pickLauncher(NEW_CONVERSATION, undefined) },
            ...launcherItems(list, error, picked, (name) => pickLauncher(NEW_CONVERSATION, name)),
          ]}
        />
      )}
    </>
  );
}

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
  const launcher = effectiveLauncher({ isMain, picked, session: session.launcher, config: assistant?.launcher });
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
