// The assistant's html component, run on the web (#4215, docs/assistant/assistant-views.md "Free-form HTML
// rendering"): loaded with the card only where the runtime may run (AssistantCard decides; this module and the
// runtime are lazy chunks). The page runs while it is on screen and is torn down after leaving it; at most a few run
// at once (host.ts claimSlot). Its frame says the assistant drew it and has native Stop / Run controls. persona.reply
// and persona.open reach the hub only after a native confirmation here, outside the card, showing what they carry.
import { useCallback, useEffect, useRef, useState } from "react";
import { currentLanguage, t } from "../i18n";
import { htmlRequest, judge, mounted, newBridge, prompted, unmounted, type BridgeAsk, type HtmlInstance } from "../lib/html-bridge";
import { conversationCards, useCards } from "../lib/assistant-views";
import { postHtmlAction } from "../lib/html-actions";
import type { UIItem } from "../lib/extension-ui";
import { uuid } from "../lib/format";
import { claimSlot, releaseSlot, runCard, type RunningCard } from "../lib/html-runtime/host";
import { cardHeight, FRAME_MIN, frameHeight } from "../lib/html-runtime/height";
import { HIDDEN_GRACE_MS } from "../lib/html-runtime/limits";
import { FRAME_SANDBOX } from "../lib/html-runtime/sanitize";
import type { StopReason } from "../lib/html-runtime/protocol";
import { useLinkManager } from "../lib/manager-choice";
import { openTargetOf } from "./AssistantCard";
import { Button, cx, Dialog, toast } from "./atoms";
import { Icon } from "./icons";

type Phase = { kind: "idle" } | { kind: "queued" } | { kind: "loading" } | { kind: "running" } | { kind: "stopped"; reason: StopReason | "user" };

interface Asking {
  ask: BridgeAsk;
  resolve: (ok: boolean) => void;
}

const REASON: Record<StopReason | "user", string> = {
  user: "ui-htmlcard.stopped_user",
  time: "ui-htmlcard.limit_time",
  memory: "ui-htmlcard.limit_memory",
  size: "ui-htmlcard.limit_size",
  crash: "ui-htmlcard.crash",
};

export default function HtmlCard({ item, conversation, cardKey, version, page, fallback }: { item: UIItem; conversation: string; cardKey: string; version: number; page: string; fallback: string }) {
  const raw = item as UIItem & { html?: unknown; height?: unknown };
  const html = typeof raw.html === "string" ? raw.html : "";
  // the frame follows what the card draws, up to this; taller content scrolls inside it
  const most = cardHeight(raw.height);
  const componentKey = item.key ?? "";
  const manager = useLinkManager();
  const answered = useCards((s) => !!conversationCards(conversation, s).actions[cardKey]);
  const answeredRef = useRef(answered);
  answeredRef.current = answered;

  const box = useRef<HTMLDivElement>(null);
  const frame = useRef<HTMLIFrameElement>(null);
  const [phase, setPhase] = useState<Phase>({ kind: "idle" });
  const [visible, setVisible] = useState(false);
  const [failed, setFailed] = useState(false);
  // how tall what the running card draws is (frame.js), null until it says
  const [content, setContent] = useState<number | null>(null);
  const [asking, setAsking] = useState<Asking | null>(null);
  // the question on screen, set as it is asked (onBridge) and cleared as it is answered
  const askingRef = useRef<Asking | null>(null);
  const slot = useRef<ReturnType<typeof claimSlot>>(null);
  const run = useRef<{ card: RunningCard; inst: HtmlInstance } | null>(null);
  const bridge = useRef(newBridge());
  const alive = useRef(true);

  const halt = useCallback((next: Phase) => {
    const r = run.current;
    run.current = null;
    if (r) {
      bridge.current = unmounted(bridge.current, r.inst.nonce);
      r.card.stop();
    }
    releaseSlot(slot.current);
    slot.current = null;
    askingRef.current?.resolve(false);
    if (!alive.current) return;
    setAsking(null);
    setPhase(next);
  }, []);

  const begin = useCallback(
    (force: boolean) => {
      if (slot.current) return;
      const s = claimSlot(() => halt({ kind: "queued" }), force);
      if (!s) return setPhase({ kind: "queued" });
      slot.current = s;
      setFailed(false);
      setContent(null);
      setPhase({ kind: "loading" });
    },
    [halt],
  );

  useEffect(() => {
    alive.current = true;
    const el = box.current;
    let hide: ReturnType<typeof setTimeout> | undefined;
    const io = new IntersectionObserver(([e]) => {
      clearTimeout(hide);
      if (e.isIntersecting) setVisible(true);
      else hide = setTimeout(() => setVisible(false), HIDDEN_GRACE_MS);
    });
    if (el) io.observe(el);
    return () => {
      io.disconnect();
      clearTimeout(hide);
      alive.current = false;
      halt({ kind: "idle" });
    };
  }, [halt]);

  // on screen: start (or wait for a place); off screen: torn down, to start again from the beginning
  useEffect(() => {
    if (visible && phase.kind === "idle") begin(false);
    if (!visible && (phase.kind === "loading" || phase.kind === "running" || phase.kind === "queued")) halt({ kind: "idle" });
  }, [visible, phase.kind, begin, halt]);

  const onBridge = useCallback(
    async (inst: HtmlInstance, kind: "reply" | "open", value: string): Promise<boolean> => {
      const msg = kind === "reply" ? { nonce: inst.nonce, kind, text: value } : { nonce: inst.nonce, kind, url: value };
      const verdict = judge(bridge.current, msg, inst.version, Date.now());
      // one question at a time: the place is taken now, not when React draws it (a card can call 200 times in one turn)
      if (!verdict.ok || askingRef.current) return false;
      if (verdict.ask.kind === "reply" && answeredRef.current) return false;
      const ok = await new Promise<boolean>((resolve) => {
        const a: Asking = {
          ask: verdict.ask,
          resolve: (yes) => {
            if (askingRef.current === a) askingRef.current = null;
            resolve(yes);
          },
        };
        askingRef.current = a;
        setAsking(a);
      });
      bridge.current = prompted(bridge.current, !ok, Date.now());
      if (!ok || run.current?.inst.nonce !== inst.nonce) return false;
      const out = await postHtmlAction(inst.conversation, inst.key, htmlRequest(inst, verdict.ask, uuid()));
      if (!out.ok) {
        if (out.outcome === "answered_elsewhere") toast(t("ui-htmlcard.elsewhere"));
        else if (out.outcome === "changed") toast(t("ui-htmlcard.changed"));
        else toast(t("ui-htmlcard.failed", { error: out.message ?? "" }), "error");
        return false;
      }
      if (verdict.ask.kind === "open") openTargetOf(out.open ?? verdict.ask.url, manager);
      return true;
    },
    [manager],
  );

  // the frame is in the page once loading: start the runtime in it
  useEffect(() => {
    if (phase.kind !== "loading" || run.current || !frame.current) return;
    const f = frame.current;
    const inst: HtmlInstance = { nonce: uuid(), conversation, key: cardKey, componentKey, page, version };
    bridge.current = mounted(bridge.current, inst);
    const card = runCard({
      frame: f,
      html,
      info: { width: Math.round(f.clientWidth) || 320, height: most, dpr: window.devicePixelRatio || 1, lang: currentLanguage(), dark: document.documentElement.classList.contains("dark") },
      onBridge: (kind, value) => onBridge(inst, kind, value),
      onState: (s) => {
        if (!alive.current || run.current?.inst.nonce !== inst.nonce) return;
        if (s.kind === "running") setPhase({ kind: "running" });
        if (s.kind === "stopped" && s.reason !== "user") halt({ kind: "stopped", reason: s.reason });
      },
      onHeight: (h) => alive.current && run.current?.inst.nonce === inst.nonce && setContent(h),
      onError: () => alive.current && setFailed(true),
    });
    run.current = { card, inst };
  }, [phase.kind, html, most, conversation, cardKey, componentKey, page, version, onBridge, halt]);

  // a frame that never says how tall its content is (no ResizeObserver) is shown at its most after a second
  useEffect(() => {
    if (phase.kind !== "running" || content !== null) return;
    const t = setTimeout(() => setContent((c) => c ?? most), 1000);
    return () => clearTimeout(t);
  }, [phase.kind, content, most]);

  const live = phase.kind === "loading" || phase.kind === "running";
  const dark = typeof document !== "undefined" && document.documentElement.classList.contains("dark");
  const label = item.title ? t("ui-htmlcard.frame_titled", { title: item.title }) : t("ui-htmlcard.frame");
  const note = phase.kind === "stopped" ? t(REASON[phase.reason]) : phase.kind === "queued" ? t("ui-htmlcard.queued") : phase.kind === "loading" ? t("ui-htmlcard.loading") : "";
  return (
    <div ref={box} className="card-ui overflow-hidden rounded-card bg-surface shadow-card" data-assistant-card="html" data-html-state={phase.kind} data-html-reason={phase.kind === "stopped" ? phase.reason : undefined}>
      <div className="flex min-w-0 items-center gap-2 border-b border-line py-1 pr-1.5 pl-4 text-[11.5px] text-ink-3">
        <Icon name="read" size={12} className="shrink-0" />
        <span className="min-w-0 flex-1 truncate">{label}</span>
        {note && <span className="min-w-0 shrink truncate" role="status">{note}</span>}
        {live ? (
          <Button tone="quiet" icon="stop" onClick={() => halt({ kind: "stopped", reason: "user" })} className="shrink-0">
            <span className="truncate">{t("ui-htmlcard.stop")}</span>
          </Button>
        ) : phase.kind === "queued" || phase.kind === "stopped" ? (
          <Button tone="quiet" icon={phase.kind === "queued" ? "play" : "retry"} onClick={() => begin(true)} className="shrink-0">
            <span className="truncate">{t(phase.kind === "queued" ? "ui-htmlcard.run" : "ui-htmlcard.retry")}</span>
          </Button>
        ) : null}
      </div>
      {live ? (
        // As tall as what the card draws, between FRAME_MIN and `most` (taller content scrolls inside the frame); unseen
        // until the frame first says how tall that is, so it never jumps. The frame's color-scheme as its own document
        // says it: a frame whose scheme differs from the page's gets an opaque backdrop (black in dark mode) instead of
        // the card's surface.
        <div className="relative" style={{ height: content === null ? FRAME_MIN : frameHeight(content, most) }} data-html-frame-height={content === null ? undefined : frameHeight(content, most)}>
          <iframe ref={frame} sandbox={FRAME_SANDBOX} referrerPolicy="no-referrer" title={label} style={{ colorScheme: dark ? "dark" : "light" }} className={cx("block size-full border-0 bg-transparent", (phase.kind === "loading" || content === null) && "opacity-0")} />
        </div>
      ) : (
        // stopped, waiting or not run: the card's words, as tall as they are, up to the same most
        <p className="overflow-auto px-4 py-3 text-[13px] leading-relaxed whitespace-pre-wrap break-words text-ink-2 select-text" style={{ maxHeight: most }}>
          {fallback}
        </p>
      )}
      {failed && live && (
        <p className="border-t border-line px-4 py-1.5 text-[11.5px] text-ink-3" role="status">
          {t("ui-htmlcard.error_line")}
        </p>
      )}
      {asking && (
        <Dialog
          open
          onClose={() => {
            asking.resolve(false);
            setAsking(null);
          }}
          width={420}
          title={t(asking.ask.kind === "reply" ? "ui-htmlcard.confirm_reply_title" : "ui-htmlcard.confirm_open_title")}
          footer={
            <>
              <Button
                tone="quiet"
                onClick={() => {
                  asking.resolve(false);
                  setAsking(null);
                }}
              >
                <span className="truncate">{t("common.cancel")}</span>
              </Button>
              <Button
                tone="primary"
                onClick={() => {
                  asking.resolve(true);
                  setAsking(null);
                }}
              >
                <span className="truncate">{t(asking.ask.kind === "reply" ? "ui-htmlcard.send" : "ui-htmlcard.open")}</span>
              </Button>
            </>
          }
        >
          <div className="flex flex-col gap-2 text-[13px] leading-relaxed break-words text-ink-2" data-html-confirm={asking.ask.kind}>
            <p>{t("ui-htmlcard.confirm_from")}</p>
            <p className="text-[12px] text-ink-3">{t(asking.ask.kind === "reply" ? "ui-htmlcard.will_send" : "ui-htmlcard.will_open")}</p>
            <p className={cx("max-h-64 overflow-auto rounded-control bg-inset px-3 py-2 text-ink shadow-hairline select-text", asking.ask.kind === "reply" ? "whitespace-pre-wrap" : "break-all")}>
              {asking.ask.kind === "reply" ? asking.ask.text : asking.ask.url}
            </p>
          </div>
        </Dialog>
      )}
    </div>
  );
}
