// Forward to (#3899; iOS ForwardSheet.swift): the message being forwarded, then Persona (the main chat, unless that is
// where it comes from) and New agent…, the recent targets, and each computer's live Agents, with search, which also finds
// ended Agents and Persona's other conversations (lib/forward-targets.ts). A dialog on a wide screen, a sheet from the
// bottom on a phone (ui/atoms Dialog). Picking a target reads the whole message when the chat holds it cut and the
// pictures of your own message, then opens that chat with the forward over its box and a line saying where it comes
// from and goes; nothing is sent until the user sends there. New agent opens New agent with the forward over its first
// message. The lists are the sessions this page holds, read once as it opens: nothing is fetched for them.
import { useMemo, useState } from "react";
import { t } from "../../i18n";
import { errorText, fullItem, imageURL } from "../../lib/api";
import { imagePayload, MAX_IMAGE } from "../../lib/attachments";
import { MAX_IMAGES } from "../../lib/composer-commands";
import { sendDraft, type DraftImage } from "../../lib/drafts";
import type { ForwardDraft } from "../../lib/forward";
import { forwardRef, forwardRoute } from "../../lib/forward-block";
import { forwardSessions, PERSONA_TARGET, rememberForward, sessionTarget } from "../../lib/forward-memory";
import { forwardSections, keyOf, targetDetail, targetTitle, type TargetSection } from "../../lib/forward-targets";
import { stateOf } from "../../lib/format";
import { addContext, whenComposer, type ContextRef } from "../../lib/links";
import { agentLabel } from "../../lib/mentions";
import { machineWithHub } from "../../lib/mesh";
import { openNewAgent } from "../../lib/new-agent";
import { navigate } from "../../lib/router";
import { SESSIONS_LIST_FEATURE } from "../../lib/session-list";
import { STATE_LABEL } from "../../lib/state-labels";
import { getState } from "../../lib/store";
import { hasFinePointer } from "../../lib/viewport";
import type { Item, Session } from "../../lib/types";
import { SessionMark } from "../../ui/AgentMark";
import { cx, Dialog, Spinner, toast } from "../../ui/atoms";
import { Icon, type IconName } from "../../ui/icons";
import { SearchField } from "../../ui/SearchField";
import { StatusMark } from "../../ui/StatusMark";
import { conversationTitle } from "../assistant/conversations";
import { ForwardBar } from "./ForwardBar";
import { readPhoto } from "./QueueStrip";

type Target = { kind: "persona" } | { kind: "new" } | { kind: "session"; s: Session };

const targetId = (x: Target) => (x.kind === "session" ? sessionTarget(x.s.machine_id, x.s.session_id) : x.kind);

/** Every chat this page holds: the Agents list's rows and the chats opened here, and Persona's conversations. */
function heldSessions(): Session[] {
  const st = getState();
  const rows = st.features.includes(SESSIONS_LIST_FEATURE) ? [...st.sessionSummaries, ...Object.values(st.sessions).flat()] : Object.values(st.sessions).flat();
  return [...rows.filter((s) => !s.assistant), ...(st.assistant?.conversations ?? [])];
}

/** A picture of the transcript as the composer takes it: inline, or read from the hub. */
async function readImage(mid: string, sid: string, item: Item): Promise<DraftImage | null> {
  const name = `${item.id.replace(/[^\w-]+/g, "_")}.${(item.image?.mime ?? "image/png").split("/")[1] ?? "png"}`;
  if (item.image?.data) return { name, mime: item.image.mime, data: item.image.data };
  try {
    const r = await fetch(imageURL(mid, sid, item.id));
    const blob = r.ok ? await r.blob() : null;
    if (!blob || !blob.type.startsWith("image/") || blob.size > MAX_IMAGE) return null;
    return { name, ...(await imagePayload(new File([blob], name, { type: blob.type }))) };
  } catch {
    return null;
  }
}

/** The forward with the whole text and its pictures, as it goes; `lost`: pictures that could not be read. */
async function complete(d: ForwardDraft): Promise<{ ref: ContextRef; images: DraftImage[]; lost: number }> {
  const { mid, sid } = d.source;
  const raw = d.cut ? ((await fullItem(mid, sid, d.cut)).text ?? d.raw) : d.raw;
  const wanted = [...(d.images ?? []).map((i) => () => readImage(mid, sid, i)), ...(d.photos ?? []).map((p) => () => readPhoto(mid, p, d.cwd))].slice(0, MAX_IMAGES);
  const read = await Promise.all(wanted.map((f) => f()));
  const images = read.filter((x): x is DraftImage => Boolean(x));
  return { ref: forwardRef(raw, d.source), images, lost: read.length - images.length };
}

/** Puts the forward over `key`'s box and its pictures in its tray once that box is on screen, and focuses it. */
function place(key: string, ref: ContextRef, images: DraftImage[], lost: number) {
  addContext(key, ref);
  const missed = (n: number) => n > 0 && toast(t("chat-forwardto.photos_lost", { count: n }), "error");
  whenComposer(
    key,
    (c) =>
      // after the box's own listeners are on (they mount with it, after it registers)
      setTimeout(() => {
        missed(lost + images.filter((image) => !sendDraft(key, { type: "image", image })).length);
        c.focus();
      }, 0),
    () => missed(lost + images.length),
  );
}

/** Goes to the target with the forward: its chat with the forward over its box, or New agent with it. */
function deliver(target: Target, ref: ContextRef, images: DraftImage[], lost: number) {
  if (target.kind !== "new") rememberForward(target.kind === "persona" ? PERSONA_TARGET : targetId(target));
  const from = ref.attrs?.machine;
  if (target.kind === "new") {
    if (lost) toast(t("chat-forwardto.photos_lost", { count: lost }), "error");
    openNewAgent({ forward: ref, images });
    return;
  }
  if (target.kind === "persona") {
    const a = getState().assistant;
    // no main conversation yet: the new one's box (views/PersonaAssistant.tsx Starter)
    place(a?.main ? `${a.machine_id}/${a.main}` : "persona-new", { ...ref, route: forwardRoute(from) }, images, lost);
    navigate({ name: "persona" });
    return;
  }
  const s = target.s;
  const machine = getState().machines.find((m) => m.id === s.machine_id)?.name;
  place(keyOf(s), { ...ref, route: s.assistant ? forwardRoute(from) : forwardRoute(from, { agent: agentLabel(s), machine }) }, images, lost);
  if (s.assistant) navigate({ name: "persona", sid: s.session_id });
  else navigate({ name: "session", mid: s.machine_id, sid: s.session_id, view: "chat" });
}

export function ForwardDialog({ draft, open, onClose }: { draft: ForwardDraft; open: boolean; onClose: () => void }) {
  const [query, setQuery] = useState("");
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  // what the page holds as it opens (iOS reads the list once): rows don't move under the pointer while it is open
  const held = useMemo(() => ({ sessions: heldSessions(), machines: getState().machines, assistant: getState().assistant, recents: forwardSessions() }), [draft]);
  const from = `${draft.source.mid}/${draft.source.sid}`;
  const a = held.assistant;
  const main = a?.main ? `${a.machine_id}/${a.main}` : undefined;
  const preview = useMemo(() => forwardRef(draft.raw, draft.source), [draft]);
  const name = (mid: string) => machineWithHub(mid, held.machines);
  const sections = useMemo(
    () =>
      forwardSections({
        all: held.sessions,
        from,
        recents: held.recents,
        main,
        machines: held.machines.map((m) => ({ id: m.id, name: machineWithHub(m.id, held.machines) })),
        query,
        online: (mid) => held.machines.find((m) => m.id === mid)?.online === true,
        conversationTitle: (s) => conversationTitle(s),
      }),
    [held, from, main, query],
  );

  const pick = async (target: Target) => {
    if (busy) return;
    setBusy(targetId(target));
    setError("");
    let done: Awaited<ReturnType<typeof complete>>;
    try {
      done = await complete(draft);
    } catch (e) {
      setBusy("");
      setError(t("chat-forwardto.failed", { error: errorText(e) }));
      return;
    }
    onClose();
    deliver(target, done.ref, done.images, done.lost);
  };

  // Enter in the search field: the first row
  const top = sections[0]?.sessions[0];
  const first: Target | undefined = !query.trim() ? (from !== main ? { kind: "persona" } : { kind: "new" }) : top ? { kind: "session", s: top } : undefined;

  return (
    <Dialog open={open} onClose={onClose} title={t("chat-forwardto.title")} width={480}>
      <div className="flex flex-col gap-2.5" data-forward-dialog aria-busy={Boolean(busy) || undefined}>
        <SearchField
          autoFocus={hasFinePointer()}
          value={query}
          onChange={setQuery}
          placeholder={t("chat-forwardto.search")}
          tone="inset"
          onKeyDown={(e) => {
            if (e.key !== "Enter" || e.nativeEvent.isComposing || !first) return;
            e.preventDefault();
            void pick(first);
          }}
        />
        <div aria-label={t("chat-forwardto.preview")} role="group">
          <ForwardBar forward={preview} />
        </div>
        <div className="-mx-1.5 flex flex-col">
          {!query.trim() && (
            <>
              {from !== main && <FixedRow icon="spark" title="Persona" detail={t("chat-forwardto.main_chat")} busy={busy === "persona"} disabled={Boolean(busy)} onClick={() => void pick({ kind: "persona" })} id="persona" />}
              <FixedRow icon="plus" title={t("chat-forwardto.new_agent")} detail={t("chat-forwardto.new_agent_detail")} busy={busy === "new"} disabled={Boolean(busy)} onClick={() => void pick({ kind: "new" })} id="new" />
            </>
          )}
          {sections.map((sec) => (
            <Section key={sec.id} section={sec}>
              {sec.sessions.map((s) => (
                <TargetRow
                  key={keyOf(s)}
                  s={s}
                  machine={sec.kind === "machine" || s.assistant ? undefined : name(s.machine_id)}
                  busy={busy === targetId({ kind: "session", s })}
                  disabled={Boolean(busy)}
                  onClick={() => void pick({ kind: "session", s })}
                />
              ))}
            </Section>
          ))}
          {query.trim() && !sections.length && (
            <p className="px-2.5 py-6 text-center text-[12.5px] text-ink-3" data-forward-none>
              {t("chat-forwardto.no_match")}
            </p>
          )}
        </div>
        {error && (
          <p role="alert" className="text-[12px] text-red">
            {error}
          </p>
        )}
      </div>
    </Dialog>
  );
}

function Section({ section, children }: { section: TargetSection; children: React.ReactNode }) {
  const heading = section.kind === "recent" ? t("chat-forwardto.recent") : section.kind === "machine" ? section.title : "";
  return (
    <div role="group" aria-label={heading || undefined} data-forward-section={section.kind}>
      {heading && <div className="truncate px-2.5 pt-2 pb-1 text-[10.5px] font-medium tracking-[0.06em] text-ink-3 uppercase">{heading}</div>}
      {children}
    </div>
  );
}

const ROW = "flex w-full min-w-0 items-center gap-3 rounded-control px-2.5 py-2 text-left transition-colors duration-100 enabled:hover:bg-hover disabled:opacity-60";

/** Persona and New agent: a mark in the accent, the name and what it is. */
function FixedRow({ icon, title, detail, busy, disabled, onClick, id }: { icon: IconName; title: string; detail: string; busy: boolean; disabled: boolean; onClick: () => void; id: string }) {
  return (
    <button type="button" className={ROW} disabled={disabled && !busy} onClick={onClick} data-forward-target={id}>
      <span aria-hidden className="flex size-7 shrink-0 items-center justify-center rounded-control bg-accent-tint text-accent-ink">
        <Icon name={icon} size={14} />
      </span>
      <span className="min-w-0 flex-1">
        <span className="block truncate text-[13px] font-medium text-ink">{title}</span>
        <span className="block truncate text-[11.5px] text-ink-3">{detail}</span>
      </span>
      {busy && <Spinner size={13} />}
    </button>
  );
}

/** A chat to forward to: the Agent's mark (Persona's for its conversations), its title, who answers there and its
 *  project (and computer where rows mix them), and its state, as the palette's rows show them. */
function TargetRow({ s, machine, busy, disabled, onClick }: { s: Session; machine?: string; busy: boolean; disabled: boolean; onClick: () => void }) {
  const st = stateOf(s);
  return (
    <button type="button" className={ROW} disabled={disabled && !busy} onClick={onClick} data-forward-target={keyOf(s)}>
      <span aria-hidden className={cx("flex size-7 shrink-0 items-center justify-center rounded-control", s.assistant ? "bg-accent-tint text-accent-ink" : "bg-field shadow-hairline")}>
        {s.assistant ? <Icon name="spark" size={14} /> : <SessionMark s={s} size={14} decorative />}
      </span>
      <span className="min-w-0 flex-1">
        <span className="block truncate text-[13px] font-medium text-ink">{targetTitle(s, (x) => conversationTitle(x))}</span>
        <span className="block truncate text-[11.5px] text-ink-3">{targetDetail(s, machine)}</span>
      </span>
      {busy ? (
        <Spinner size={13} />
      ) : (
        !s.assistant && (
          <span className="flex shrink-0 items-center gap-1.5 text-[11px] whitespace-nowrap text-ink-3">
            <StatusMark state={st} size="sm" label={false} />
            {STATE_LABEL[st]}
          </span>
        )
      )}
    </button>
  );
}
