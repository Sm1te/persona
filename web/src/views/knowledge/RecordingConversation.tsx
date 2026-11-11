// A recorded meeting's conversation on its Knowledge detail (#3862, docs/assistant/meeting-minutes.md "Meeting conversation
// (#3862)", "What the phone and the web do"): the private switch for every meeting, and for a room meeting the one thread
// (the room's questions and answers, then **After the meeting** and the follow-up conversation), the room's state and the
// follow-up's first message. Only on a hub that lists `meeting_conversation`; older hubs see the page as before.
// It reads `GET /api/v1/recordings/{id}/live` once, then only the parts a content-free `meeting_live` event says are
// newer (`?parts=`), and every 5 seconds while the meeting runs as a fallback. On a hub that also lists `meeting_photos`
// (phase 3) it reads a meeting's photos (`GET …/photos`) after that first read, again when their revision moves, and
// shows a photo the room was sent as one entry of the thread (RecordingPhotos.tsx).
import { useCallback, useEffect, useId, useMemo, useRef, useState } from "react";
import { t } from "../../i18n";
import { api, ApiError, enc, errorText, imageURL, sessionPath } from "../../lib/api";
import { conversationPage } from "../../lib/conversation-page";
import { uuid } from "../../lib/format";
import {
  countsAsMeeting,
  meetingConversation,
  meetingKind,
  meetingMeta,
  meetingRoom,
  recordingPhotos,
  type KBDetail,
  type MeetingConversation,
  type MeetingRoom,
  type RecordingAsk,
  type RecordingLive,
  type RecordingPhoto,
  type RecordingView,
} from "../../lib/kb";
import { messageManager } from "../../lib/manager-choice";
import { followupThread, MEETING_CONVERSATION_FEATURE, MEETING_PHOTOS_FEATURE, meetingRunning, newerParts, roomState, roomThread, type MeetingLine, type ThreadEntry } from "../../lib/meeting-thread";
import { href, navigate, onLinkClick, type Route } from "../../lib/router";
import { listenSession, onHubEvent, useStore } from "../../lib/store";
import type { ItemsPage, Item } from "../../lib/types";
import { documentPage, visiblePoll } from "../../lib/visible-poll";
import { Button, Collapse, cx, Spinner, Switch } from "../../ui/atoms";
import { Icon, type IconName } from "../../ui/icons";
import { Markdown } from "../../ui/Markdown";
import { Card, SectionTitle } from "./KBParts";
import { askFailure, AskRow } from "./RecordingAsks";
import { PhotoThumb, usePhotoImages, type MeetingPhotos } from "./RecordingPhotos";

type Auth = { token: string; tokenOwner: string; headers: Record<string, string> };
/** The parts this page reads again when they change (an event may name more from later phases). */
const PARTS = ["room", "asks", "private", "followup"] as const;
type Part = (typeof PARTS)[number];
/** The fallback read while the meeting runs (the event is the main path). */
const POLL_MS = 5000;
/** A conversation page this long is enough for one meeting's room or follow-up; more says where the rest is. */
const THREAD_PAGE = "turns=400&fold_work=1&lazy_images=1";

export interface MeetingState {
  /** The hub lists `meeting_conversation`. */
  on: boolean;
  /** A meeting as #2981 counts them: it takes the private mark. */
  meeting: boolean;
  /** A room meeting (its upload carried `room`). */
  roomMeeting: boolean;
  room?: MeetingRoom;
  followup?: MeetingConversation;
  private?: boolean;
  /** `asks` of `GET …/live`, once read. */
  asks?: RecordingAsk[];
  /** The meeting's photos (phase 3), which the minutes, the thread and **Photos** share. */
  photos: MeetingPhotos;
  /** Goes up whenever the room's or the follow-up's thread should be read again. */
  roomTick: number;
  followupTick: number;
  /** Sets the private mark (`PUT …/private`); throws what the hub answered. */
  setPrivate: (value: boolean) => Promise<void>;
  /** The follow-up was made (its first message's answer). */
  madeFollowup: (f: MeetingConversation) => void;
}

type Read = { room?: MeetingRoom; followup?: MeetingConversation; private?: boolean; asks?: RecordingAsk[] };

/** A recording's meeting conversation parts: detail.meta until `…/live` answers, then what it reads. `reload` reads the
 *  detail again (true once one read has: false when another read took its place), after a change of the photos: the hub
 *  may have taken a deleted photo's items out of the minutes, or be making them again. */
export function useRecordingMeeting(detail: KBDetail | null | undefined, rec: RecordingView | null, auth: Auth, current: () => boolean, manager: string, reload?: () => Promise<boolean>): MeetingState {
  const on = useStore((s) => s.features.includes(MEETING_CONVERSATION_FEATURE));
  const photosFlag = useStore((s) => s.features.includes(MEETING_PHOTOS_FEATURE));
  const meta = detail?.kind === "recordings" ? detail.meta : undefined;
  const fromMeta = useMemo(() => meetingMeta(meta), [meta]);
  const meeting = on && !!detail && detail.kind === "recordings" && countsAsMeeting(meta);
  const wanted = on && !!detail && detail.kind === "recordings" && (meeting || !!fromMeta.room || !!fromMeta.followup);
  const id = detail?.id ?? "";
  // What …/live said of this recording, part by part: a part it answered (even as absent) replaces detail.meta's. Kept
  // with the recording it is of, so another recording on the same page starts from its own detail.meta.
  const [state, setState] = useState<{ id: string; got: Partial<Record<Part, true>>; v: Read; gone?: boolean }>({ id, got: {}, v: {} });
  const read = state.id === id ? state : { id, got: {} as Partial<Record<Part, true>>, v: {} as Read, gone: false };
  const setRead = useCallback((of: string, f: (prev: { got: Partial<Record<Part, true>>; v: Read }) => { got: Partial<Record<Part, true>>; v: Read }) => {
    setState((prev) => ({ id: of, gone: prev.id === of ? prev.gone : false, ...f(prev.id === of ? prev : { got: {}, v: {} }) }));
  }, []);
  const [ticks, setTicks] = useState({ room: 0, followup: 0 });
  const revs = useRef<{ id: string; of: Record<string, number> }>({ id, of: {} });
  if (revs.current.id !== id) revs.current = { id, of: {} };
  const shown = useRef(id);
  shown.current = id;
  const request = useRef(auth);
  request.current = auth;
  // Photos come with the conversation, on a recording whose kind is meeting (the only kind that takes them).
  const photosOn = on && photosFlag && detail?.kind === "recordings" && meetingKind(meta);
  const photosWanted = useRef(photosOn);
  photosWanted.current = photosOn;
  const reloadRef = useRef(reload);
  reloadRef.current = reload;
  const [photos, setPhotos] = useState<{ id: string; list?: RecordingPhoto[] }>({ id });
  // The photos' revision read (`ack`, only once a list read succeeded; a reconnect forgets it), the last one a list read
  // acknowledged (`had`, kept across a reconnect) and the newest one heard of (`seen`). `changed`: a change of the photos
  // was heard that no successful list read has followed yet; `stale`: one has, and no detail read since has succeeded
  // (`reloads` counts the detail reads asked for, `reloading` while the last one runs).
  const photoRevs = useRef<{ id: string; ack?: number; had?: number; first?: number; seen: number; changed?: boolean; stale?: boolean; reloads: number; reloading?: boolean }>({ id, seen: 0, reloads: 0 });
  if (photoRevs.current.id !== id) photoRevs.current = { id, seen: 0, reloads: 0 };
  const photoList = photos.id === id ? photos.list : undefined;
  const images = usePhotoImages(id, photoList, auth, current);

  // Nothing of it on a hub without the flag: detail.meta may still name a room or a follow-up (a manager that has
  // meeting.json while the hub does not list the feature), and the page stays as it was then, its threads unread.
  const merged: Read = on ? {
    room: read.got.room ? read.v.room : fromMeta.room,
    followup: read.got.followup ? read.v.followup : fromMeta.followup,
    private: read.got.private ? read.v.private : fromMeta.private,
    asks: read.v.asks,
  } : {};

  // One read at a time; parts asked for meanwhile go in the next (all of them when any asked for everything).
  const queue = useRef<{ busy: boolean; next: Set<Part> | "all" | null }>({ busy: false, next: null });
  const apply = useCallback((of: string, parts: readonly Part[], r: RecordingLive) => {
    const moved = { room: false, followup: false };
    const known = revs.current.of;
    for (const [part, rev] of Object.entries(r.revs ?? {})) {
      // The photos' revision counts as read only once their list is (readPhotos).
      if (typeof rev !== "number" || part === "photos") continue;
      // Only a part that moved since a read: the threads read themselves when they first show.
      const before = known[part];
      if ((part === "room" || part === "asks") && before !== undefined && rev !== before) moved.room = true;
      if (part === "followup" && before !== undefined && rev !== before) moved.followup = true;
      known[part] = Math.max(rev, before ?? rev);
    }
    setRead(of, (prev) => {
      const got = { ...prev.got }, v = { ...prev.v };
      for (const part of parts) {
        got[part] = true;
        if (part === "room") v.room = meetingRoom(r.room);
        if (part === "followup") v.followup = meetingConversation(r.followup);
        if (part === "private") v.private = typeof r.private === "boolean" ? r.private : undefined;
        if (part === "asks") v.asks = Array.isArray(r.asks) ? r.asks : [];
      }
      return { got, v };
    });
    if (moved.room || moved.followup) setTicks((x) => ({ room: x.room + (moved.room ? 1 : 0), followup: x.followup + (moved.followup ? 1 : 0) }));
  }, [setRead]);

  // The detail read again after a change of the photos (`stale`): after the list read that follows the change, then on
  // the next poll, reconnect or event until one such read succeeds (one at a time). Never for the page's first list read.
  const readDetail = useCallback(() => {
    const revs = photoRevs.current;
    const reload = reloadRef.current;
    if (!revs.stale || revs.reloading || !reload) return;
    const n = ++revs.reloads;
    revs.reloading = true;
    const settle = (ok: boolean) => {
      if (revs.reloads !== n) return;
      revs.reloading = false;
      if (ok) revs.stale = false;
    };
    reload().then(settle, () => settle(false));
  }, []);
  // The photos' list, one read at a time (one asked for meanwhile follows). A successful read acknowledges the newest
  // revision heard of before it started; a failed one keeps the list shown, and the next event or read tries again. One
  // that follows a change of the photos reads the detail again (readDetail); with another read to follow, after that one.
  const photoQueue = useRef({ busy: false, again: false });
  const readPhotos = useCallback(() => {
    const q = photoQueue.current;
    if (q.busy) { q.again = true; return; }
    if (!photosWanted.current || !current() || shown.current !== id) return;
    q.busy = true;
    const revs = photoRevs.current;
    const asked = revs.seen;
    const changed = !!revs.changed;
    revs.changed = false;
    api<unknown>(`/recordings/${enc(id)}/photos`, request.current)
      .then((r) => {
        if (!current() || shown.current !== id || photoRevs.current !== revs) return;
        const list = recordingPhotos(r);
        revs.ack = Math.max(revs.ack ?? asked, asked);
        revs.had = Math.max(revs.had ?? asked, asked);
        setPhotos({ id, list });
        if (changed) {
          // A detail read running now may have started before the change: it settles nothing.
          revs.stale = true;
          revs.reloads++;
          revs.reloading = false;
        }
        if (!q.again) readDetail();
      })
      .catch(() => { if (changed) revs.changed = true; })
      .finally(() => {
        q.busy = false;
        if (q.again) { q.again = false; latestPhotos.current(); }
      });
  }, [id, current, readDetail]);
  const latestPhotos = useRef(readPhotos);
  latestPhotos.current = readPhotos;
  /** A photos revision heard of: the list is read until one read succeeds, then when the revision is newer than the one
   *  read. An event without a number is read anyway; a whole `…/live` read without one has no photos to tell of. A
   *  revision newer than the last list read's is a change (an event's also before any list read: the detail may be older
   *  than it); with no list to read, a detail read that failed is tried again. */
  const heardPhotos = useCallback((rev: unknown, fromLive = false) => {
    const revs = photoRevs.current;
    const n = typeof rev === "number" ? rev : undefined;
    if (n !== undefined) revs.seen = Math.max(revs.seen, n);
    // Before any list read, the page's first `…/live` read is the base (`first`): a later one newer than it, while that
    // first list read runs, is a change too.
    const base = revs.had ?? revs.first;
    if ((n === undefined || base === undefined) ? !fromLive : n > base) revs.changed = true;
    if (fromLive && n !== undefined && revs.first === undefined) revs.first = n;
    if (revs.ack === undefined || (n === undefined ? !fromLive : n > revs.ack)) readPhotos();
    else readDetail();
  }, [readPhotos, readDetail]);
  const readLive = useCallback((parts: readonly Part[] | "all") => {
    const q = queue.current;
    if (q.busy) {
      q.next = parts === "all" || q.next === "all" ? "all" : new Set([...(q.next ?? []), ...parts]);
      return;
    }
    if (!current() || shown.current !== id) return;
    q.busy = true;
    const asked = parts === "all" ? PARTS : parts;
    const path = `/recordings/${enc(id)}/live${parts === "all" ? "" : `?parts=${asked.join(",")}`}`;
    api<RecordingLive>(path, request.current)
      .then((r) => {
        if (!current() || shown.current !== id) return;
        apply(id, asked, r);
        // The photos after the first whole read (and every whole read after): its revision says whether they moved.
        if (photosWanted.current && parts === "all") heardPhotos(r.revs?.photos, true);
      })
      .catch((err) => {
        // No route (an older manager), no upload, or deleted: detail.meta is all there is.
        if (err instanceof ApiError && (err.status === 404 || err.status === 410) && shown.current === id) {
          setState((prev) => (prev.id === id ? { ...prev, gone: true } : { id, got: {}, v: {}, gone: true }));
        } else if (photosWanted.current && parts === "all") readPhotos();
      })
      .finally(() => {
        q.busy = false;
        const next = q.next;
        q.next = null;
        // What was asked meanwhile goes to the recording shown now (its own first read may be among it).
        if (next) latest.current(next === "all" ? "all" : [...next]);
      });
  }, [id, current, apply, heardPhotos, readPhotos]);
  const latest = useRef(readLive);
  latest.current = readLive;

  // The fallback read is a room meeting's: a #2981 meeting's asks are read by "Questions in the meeting" already, and
  // its private mark and follow-up come with the event.
  const running = !!rec && !!merged.room && (rec.partial || ["starting", "ready", "unavailable"].includes(roomState(merged.room) ?? ""));
  const live = wanted && !read.gone;
  useEffect(() => {
    if (!live) return;
    readLive("all");
    // A reconnect may have missed events: read everything again, the photos' list too once that read is in.
    const offHello = onHubEvent("hello", () => {
      photoRevs.current.ack = undefined;
      readLive("all");
    });
    const offEvent = onHubEvent("meeting_live", (msg) => {
      if (String(msg.recording_id ?? "").toLowerCase() !== id.toLowerCase() || messageManager(msg) !== manager) return;
      const ack = photoRevs.current.ack;
      const newer = newerParts(msg.changed, msg.revs, { ...revs.current.of, ...(ack !== undefined ? { photos: ack } : {}) });
      // `photos` has no field on `…/live`: its list is `GET …/photos`.
      if (photosWanted.current && newer.includes("photos")) heardPhotos((msg.revs as Record<string, unknown> | undefined)?.photos);
      else readDetail();
      const parts = newer.filter((p): p is Part => (PARTS as readonly string[]).includes(p));
      if (parts.length) readLive(parts);
    });
    return () => { offHello(); offEvent(); };
  }, [live, photosOn, id, manager, readLive, heardPhotos, readDetail]);
  useEffect(() => {
    if (!live || !running) return;
    // visiblePoll reads at once when the page is shown; the mount's read above already did. A page mounted hidden reads
    // when it is shown.
    let skip = documentPage.visible();
    return visiblePoll(() => { if (skip) { skip = false; return; } readLive("all"); }, POLL_MS, documentPage);
  }, [live, running, readLive]);

  const setPrivate = useCallback(async (value: boolean) => {
    const r = await api<{ private?: unknown }>(`/recordings/${enc(id)}/private`, { method: "PUT", json: { private: value }, ...request.current });
    if (!current() || shown.current !== id) return;
    setRead(id, (prev) => ({ got: { ...prev.got, private: true }, v: { ...prev.v, private: typeof r.private === "boolean" ? r.private : value } }));
  }, [id, current, setRead]);
  const madeFollowup = useCallback((f: MeetingConversation) => {
    setRead(id, (prev) => ({ got: { ...prev.got, followup: true }, v: { ...prev.v, followup: f } }));
    setTicks((x) => ({ ...x, followup: x.followup + 1 }));
  }, [id, setRead]);

  return {
    on, meeting, roomMeeting: on && !!merged.room, ...merged,
    photos: { on: photosOn, list: photosOn ? photoList : undefined, images },
    roomTick: ticks.room, followupTick: ticks.followup, setPrivate, madeFollowup,
  };
}

/** **Private meeting**: whether its minutes may be material when Persona answers in later meetings ("The private mark").
 *  A meeting with no mark counts as private, so the switch reads on until it is turned off. */
export function MeetingPrivate({ meeting }: { meeting: MeetingState }) {
  const label = useId(), hint = useId();
  const [busy, setBusy] = useState(false);
  const [shown, setShown] = useState<boolean | undefined>(undefined);
  const [error, setError] = useState("");
  if (!meeting.meeting) return null;
  const value = shown ?? meeting.private;
  const change = async (next: boolean) => {
    setBusy(true); setError(""); setShown(next);
    try {
      await meeting.setPrivate(next);
    } catch (err) {
      setError(errorText(err));
    } finally {
      setShown(undefined); setBusy(false);
    }
  };
  return (
    <section data-meeting-private>
      <Card>
        <div className="flex items-center gap-3 px-4 py-2.5">
          <Icon name="lock" size={14} className="shrink-0 text-ink-3" />
          <div className="min-w-0 flex-1">
            <p id={label} className="text-[13px] text-ink">{t("knowledge-kbdetail.private_label")}</p>
            <p id={hint} className="mt-0.5 text-[11.5px] leading-relaxed text-ink-3">
              {t(value === true ? "knowledge-kbdetail.private_on" : value === false ? "knowledge-kbdetail.private_off" : "knowledge-kbdetail.private_unset")}
            </p>
          </div>
          {busy && <Spinner />}
          <Switch on={value !== false} disabled={busy} label={t("knowledge-kbdetail.private_label")} describedBy={hint} onChange={(next) => void change(next)} />
        </div>
      </Card>
      {error && <p role="alert" className="mt-1.5 px-1 text-[11.5px] leading-relaxed break-words text-red">{t("knowledge-kbdetail.private_failed", { error })}</p>}
    </section>
  );
}

/** A conversation's items for the thread, read again whenever `tick` moves or its transcript grows. */
function useThread(conv: MeetingConversation | undefined, tick: number, auth: Auth, current: () => boolean) {
  const [page, setPage] = useState<{ key: string; items: Item[]; more: boolean } | null>(null);
  const [error, setError] = useState("");
  const [grew, setGrew] = useState(0);
  const mid = conv?.machine_id ?? "", sid = conv?.session_id ?? "";
  const key = mid && sid ? `${mid}/${sid}` : "";
  // The list's rev for it (the transcript's size), when a list has it.
  const rowRev = useStore((s) => {
    if (!key) return 0;
    const row = (s.sessions[mid] ?? []).find((x) => x.session_id === sid)
      ?? (s.assistant?.machine_id === mid ? s.assistant.conversations.find((x) => x.session_id === sid) : undefined);
    return row?.rev ?? 0;
  });
  useEffect(() => {
    if (!key) return;
    return listenSession(mid, sid, { rev: () => setGrew((n) => n + 1) });
  }, [key, mid, sid]);
  useEffect(() => {
    if (!key) return;
    let alive = true;
    // Session routes go to the conversation's own manager: no manager header of the page's.
    api<ItemsPage>(`${sessionPath(mid, sid)}/items?${THREAD_PAGE}`, { token: auth.token, tokenOwner: auth.tokenOwner })
      .then((p) => { if (alive && current()) { setPage({ key, items: p.items ?? [], more: !!p.has_more }); setError(""); } })
      .catch((err) => { if (alive && current()) setError(errorText(err)); });
    return () => { alive = false; };
  }, [key, mid, sid, tick, grew, rowRev, auth.token, auth.tokenOwner, current]);
  return { items: page?.key === key ? page.items : null, more: page?.key === key && page.more, error };
}

const ROOM_PROBLEMS = ["runtime_unsupported", "node_too_old", "machine_offline", "start_failed"];

/** The room's state in one line, and its icon. */
function roomLine(room: MeetingRoom | undefined, role: RecordingView["attendance"], trimmed: boolean): { icon: IconName; text: string } | null {
  const state = roomState(room);
  switch (state) {
    case "none": return { icon: "micOff", text: t("knowledge-kbdetail.room_none") };
    case "starting": return { icon: "clock", text: t("knowledge-kbdetail.room_starting") };
    case "ready": return { icon: "mic", text: t(role === "participant" ? "knowledge-kbdetail.room_ready_participant" : role === "listener" ? "knowledge-kbdetail.room_ready_listener" : "knowledge-kbdetail.room_ready_off") };
    case "unavailable": {
      const p = room?.problem ?? "";
      return { icon: "warning", text: t(ROOM_PROBLEMS.includes(p) ? `knowledge-kbdetail.room_problem_${p}` : "knowledge-kbdetail.room_problem_other") };
    }
    case "ended": return { icon: "check", text: t(trimmed ? "knowledge-kbdetail.room_ended_copy" : "knowledge-kbdetail.room_ended") };
    case "deleted": return { icon: "trash", text: t("knowledge-kbdetail.room_deleted") };
  }
  return null;
}

type Props = {
  detail: KBDetail;
  rec: RecordingView;
  meeting: MeetingState;
  auth: Auth;
  current: () => boolean;
  manager: string;
};

/** **Conversation**: the one thread of a room meeting (or of any recording with a follow-up). */
export function RecordingConversation({ detail, rec, meeting, auth, current, manager }: Props) {
  const heading = useId();
  const { room, followup } = meeting;
  const roomIds = room?.machine_id && room.session_id && roomState(room) !== "deleted" && roomState(room) !== "none"
    ? { machine_id: room.machine_id, session_id: room.session_id } : undefined;
  const roomPart = useThread(roomIds, meeting.roomTick, auth, current);
  const followPart = useThread(followup, meeting.followupTick, auth, current);
  if (!meeting.on || (!meeting.roomMeeting && !followup)) return null;
  const asks = meeting.asks ?? [];
  const roomEntries = roomIds && roomPart.items ? roomThread(roomPart.items, detail.id, asks) : [];
  const followEntries = followup && followPart.items ? followupThread(followPart.items) : [];
  // A room that never came up shows the questions asked of the meeting's material instead, as #3746 does; one that went
  // unavailable after it started shows those it answered from the material after its thread, and those its thread does
  // not hold that are not answered (one that failed before its message went out, or still waits to go).
  const inThread = new Set(roomEntries.flatMap((e) => ((e.kind === "question" || e.kind === "heard") && e.ask ? [e.ask] : [])));
  const shownAsks = !roomIds ? asks
    : asks.filter((a) => a.via === "material" || (!!roomPart.items && a.state !== "answered" && !inThread.has(a.id.toLowerCase())));
  const askById = new Map(asks.map((a) => [a.id.toLowerCase(), a]));
  const showAsks = shownAsks.length > 0;
  const before = roomEntries.length > 0 || showAsks;
  const running = meetingRunning(detail.meta, room);
  const status = meeting.roomMeeting ? roomLine(room, rec.attendance, !!rec.trimmedAt) : null;
  const roomRoute = roomIds ? conversationPage(roomIds.session_id, roomIds.machine_id, manager) : null;
  const followRoute = followup ? conversationPage(followup.session_id, followup.machine_id, manager) : null;
  const loadingRoom = !!roomIds && !roomPart.items && !roomPart.error;
  const loadingFollow = !!followup && !followPart.items && !followPart.error;

  return (
    <section aria-labelledby={heading} data-meeting-conversation>
      <SectionTitle title={<span id={heading}>{t("knowledge-kbdetail.conversation_title")}</span>} />
      <Card>
        {status && (
          <div className="flex items-start gap-2 px-4 py-2.5 text-[12.5px] leading-relaxed text-ink-2">
            <Icon name={status.icon} size={13} className="mt-0.5 shrink-0 text-ink-3" />
            <p role="status" className="min-w-0 flex-1 break-words">{status.text}</p>
            {roomRoute && <OpenLink route={roomRoute} title={t("knowledge-kbdetail.conversation_open_room")} />}
          </div>
        )}
        {(before || roomPart.error || loadingRoom || roomPart.more) && (
          <div className="py-1">
            {roomPart.more && <p className="px-4 pt-2 text-[11.5px] text-ink-3">{t("knowledge-kbdetail.conversation_earlier")}</p>}
            {roomPart.error && <p role="alert" className="px-4 py-2 text-[12.5px] break-words text-red">{t("knowledge-kbdetail.conversation_load_failed", { error: roomPart.error })}</p>}
            {loadingRoom && <div className="flex justify-center py-3"><Spinner /></div>}
            {roomEntries.length > 0 && <Thread entries={roomEntries} conv={roomIds!} asks={askById} room photos={meeting.photos} />}
            {showAsks && (
              <ol className="divide-y divide-line">
                {shownAsks.map((ask) => <AskRow key={ask.id} ask={ask} startedAt={rec.startedAt} />)}
              </ol>
            )}
          </div>
        )}
        {followup && (
          <div className="py-1">
            {before && <AfterTheMeeting route={followRoute!} />}
            {!before && followRoute && (
              <div className="flex items-center gap-2 px-4 pt-2.5 pb-1">
                <span className="min-w-0 flex-1 truncate text-[11.5px] font-medium text-ink-3">{t("knowledge-kbdetail.conversation_after")}</span>
                <OpenLink route={followRoute} title={t("knowledge-kbdetail.conversation_open_followup")} />
              </div>
            )}
            {followPart.more && <p className="px-4 pt-2 text-[11.5px] text-ink-3">{t("knowledge-kbdetail.conversation_earlier")}</p>}
            {followPart.error && <p role="alert" className="px-4 py-2 text-[12.5px] break-words text-red">{t("knowledge-kbdetail.conversation_load_failed", { error: followPart.error })}</p>}
            {loadingFollow && <div className="flex justify-center py-3"><Spinner /></div>}
            {followEntries.length > 0 && <Thread entries={followEntries} conv={followup} />}
          </div>
        )}
        {followup && followRoute ? (
          <div className="flex justify-end px-4 py-2.5">
            <a href={href(followRoute)} onClick={(ev) => onLinkClick(ev, followRoute)}
              className="inline-flex h-8 items-center gap-1.5 whitespace-nowrap rounded-control bg-surface px-3 text-[12.5px] font-medium text-ink shadow-btn transition-colors hover:bg-hover">
              <Icon name="chat" size={14} stroke={2.1} />
              {t("knowledge-kbdetail.conversation_continue")}
            </a>
          </div>
        ) : meeting.roomMeeting && running ? (
          <p className="px-4 py-2.5 text-[11.5px] leading-relaxed text-ink-3">{t("knowledge-kbdetail.conversation_read_only")}</p>
        ) : meeting.roomMeeting && (
          <FollowupComposer id={detail.id} auth={auth} current={current} manager={manager} onMade={meeting.madeFollowup} />
        )}
      </Card>
    </section>
  );
}

/** "Open", opening one of the meeting's conversations; `title` says which. */
function OpenLink({ route, title }: { route: Route; title: string }) {
  return (
    <a href={href(route)} onClick={(ev) => onLinkClick(ev, route)} title={title} aria-label={title}
      className="inline-flex shrink-0 items-center gap-1 whitespace-nowrap text-[12px] font-medium text-accent-ink hover:underline">
      {t("knowledge-kbdetail.conversation_open")}
      <Icon name="chevronRight" size={11} stroke={2.2} />
    </a>
  );
}

/** The divider between the room and the follow-up, opening the follow-up. */
function AfterTheMeeting({ route }: { route: Route }) {
  return (
    <div role="separator" aria-label={t("knowledge-kbdetail.conversation_after")} className="flex items-center gap-3 px-4 pt-2.5 pb-1">
      <span aria-hidden className="h-px flex-1 bg-line" />
      <span className="shrink-0 whitespace-nowrap text-[11.5px] font-medium text-ink-3">{t("knowledge-kbdetail.conversation_after")}</span>
      <span aria-hidden className="h-px flex-1 bg-line" />
      <OpenLink route={route} title={t("knowledge-kbdetail.conversation_open_followup")} />
    </div>
  );
}

/** HH:MM of an item's time, "" without one. */
function at(ts?: string): string {
  const d = ts ? new Date(ts) : null;
  if (!d || Number.isNaN(d.getTime())) return "";
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}

const VIA_ICON: Record<string, IconName> = { typed: "keyboard", spoken: "voice", owner: "person" };

function Thread({ entries, conv, asks, room = false, photos }: { entries: ThreadEntry[]; conv: MeetingConversation; asks?: ReadonlyMap<string, RecordingAsk>; room?: boolean; photos?: MeetingPhotos }) {
  // An ask asked again has a message per attempt: its state shows under the last one.
  const last = new Map<string, string>();
  for (const e of entries) if ((e.kind === "question" || e.kind === "heard") && e.ask) last.set(e.ask, e.id);
  return (
    <ol className="flex flex-col py-1">
      {entries.map((e) => {
        const ask = (e.kind === "question" || e.kind === "heard") && e.ask && last.get(e.ask) === e.id ? asks?.get(e.ask) : undefined;
        return <li key={e.id} data-thread={e.kind}><Entry entry={e} conv={conv} room={room} photos={photos} />{ask && <AskState ask={ask} />}</li>;
      })}
    </ol>
  );
}

/** Under a question asked in the room: that it is being answered, or why it was not; nothing once answered (the
 *  room's reply follows it). */
function AskState({ ask }: { ask: RecordingAsk }) {
  if (ask.state === "pending") return <p className="px-4 pb-1 pl-[39px] text-[12.5px] leading-relaxed text-ink-3">{t("knowledge-kbdetail.ask_pending")}</p>;
  if (ask.state === "failed") return <p className="px-4 pb-1 pl-[39px] text-[12.5px] leading-relaxed text-red">{askFailure(ask)}</p>;
  return null;
}

function Entry({ entry: e, conv, room, photos }: { entry: ThreadEntry; conv: MeetingConversation; room: boolean; photos?: MeetingPhotos }) {
  switch (e.kind) {
    case "joined":
      return <Muted icon="spark" text={t("knowledge-kbdetail.thread_joined")} time={at(e.ts)} />;
    case "lines":
      return <Lines label={t("knowledge-kbdetail.thread_lines", { count: e.lines.length })} lines={e.lines} time={at(e.ts)} />;
    case "ended":
      return <Muted icon="check" text={t("knowledge-kbdetail.thread_ended")} time={at(e.ts)} />;
    case "heard":
      return <Lines label={e.clock ? t("knowledge-kbdetail.thread_heard", { clock: e.clock }) : t("knowledge-kbdetail.ask_heard_empty")} lines={e.lines} time={at(e.ts)} heard />;
    case "material":
      return <Material title={e.seed ? t("knowledge-kbdetail.thread_material") : e.title || t("knowledge-kbdetail.thread_context")} text={e.text} />;
    case "question": {
      const label = t(`knowledge-kbdetail.thread_via_${e.via}`);
      return (
        <div className="flex items-start gap-2.5 px-4 pt-3 pb-0.5">
          <span title={label} className="mt-0.5 shrink-0 text-ink-3">
            <Icon name={VIA_ICON[e.via]} size={13} />
            <span className="sr-only">{label}</span>
          </span>
          <p className="min-w-0 flex-1 text-[13px] font-medium leading-relaxed break-words whitespace-pre-wrap text-ink select-text">{e.text}</p>
          {at(e.ts) && <span className="shrink-0 whitespace-nowrap pt-px text-[11.5px] text-ink-3 tabular-nums">{at(e.ts)}</span>}
        </div>
      );
    }
    case "answer":
      return (
        <div className="min-w-0 px-4 pt-1 pb-2 pl-[39px] text-[13px] leading-relaxed text-ink-2 select-text">
          {room ? <p className="break-words whitespace-pre-wrap">{e.text}</p> : <Markdown text={e.text} small mid={conv.machine_id} />}
        </div>
      );
    case "image":
      return (
        <div className="px-4 py-1 pl-[39px]">
          <img src={imageURL(conv.machine_id, conv.session_id, e.id)} alt={t("knowledge-kbdetail.thread_image")} loading="lazy"
            className="max-h-40 max-w-[240px] rounded-control bg-field object-cover" />
        </div>
      );
    case "photo":
      return <ThreadPhoto entry={e} photos={photos} />;
  }
}

/** The thumbnail's height in the thread, and the bounds of its width (the photo's own shape between them). */
const THREAD_PHOTO = { height: 144, min: 96, max: 240 };

/** A photo the room was sent, resolved by its id in `GET …/photos` (never the room's own copy of the image): its
 *  thumbnail and the room's note; "Photo removed" once a list read lacks it; only "Photo" until a list is read. */
function ThreadPhoto({ entry: e, photos }: { entry: Extract<ThreadEntry, { kind: "photo" }>; photos?: MeetingPhotos }) {
  const photo = photos?.on ? photos.list?.find((p) => p.id.toLowerCase() === e.photo) : undefined;
  const time = at(e.ts);
  if (!photo && photos?.on && photos.list) return <Muted icon="image" text={t("knowledge-kbdetail.photo_removed")} time={time} />;
  const ratio = photo && photo.width > 0 && photo.height > 0 ? photo.width / photo.height : 4 / 3;
  const width = Math.round(Math.min(THREAD_PHOTO.max, Math.max(THREAD_PHOTO.min, THREAD_PHOTO.height * ratio)));
  return (
    <div className="px-4 pt-2.5 pb-1">
      <div className="flex items-center gap-2.5 text-[12px] text-ink-3">
        <Icon name="camera" size={13} className="shrink-0" />
        <span className="min-w-0 flex-1 truncate">{photo ? t("knowledge-kbdetail.photo_label", { n: photo.n }) : t("knowledge-kbdetail.thread_image")}</span>
        {time && <span className="shrink-0 whitespace-nowrap text-[11.5px] tabular-nums">{time}</span>}
      </div>
      {photo && photos && (
        <div className="pt-1.5 pl-[23px]">
          <PhotoThumb photo={photo} images={photos.images} className="max-w-full rounded-control" style={{ width, height: THREAD_PHOTO.height }} />
          {photo.note && <p className="mt-1.5 text-[13px] leading-relaxed break-words whitespace-pre-wrap text-ink-2 select-text">{photo.note}</p>}
        </div>
      )}
    </div>
  );
}

function Muted({ icon, text, time }: { icon: IconName; text: string; time: string }) {
  return (
    <div className="flex items-center gap-2.5 px-4 py-1.5 text-[12px] text-ink-3">
      <Icon name={icon} size={13} className="shrink-0" />
      <span className="min-w-0 flex-1 truncate">{text}</span>
      {time && <span className="shrink-0 whitespace-nowrap text-[11.5px] tabular-nums">{time}</span>}
    </div>
  );
}

/** A batch or a heard question folded to one line; open, its lines at their clock in the meeting. */
function Lines({ label, lines, time, heard = false }: { label: string; lines: MeetingLine[]; time: string; heard?: boolean }) {
  const [open, setOpen] = useState(false);
  const body = useId();
  return (
    <div className={cx("px-4", heard ? "pt-3 pb-0.5" : "py-0.5")}>
      <button type="button" aria-expanded={open} aria-controls={body} disabled={!lines.length} onClick={() => setOpen((x) => !x)}
        className={cx("flex w-full min-w-0 items-center gap-2.5 rounded-control py-1 text-left enabled:hover:text-ink", heard ? "text-[13px] font-medium text-ink" : "text-[12px] text-ink-3")}>
        <Icon name={heard ? "mic" : "quote"} size={13} className="shrink-0 text-ink-3" />
        <span className="min-w-0 flex-1 truncate">{label}</span>
        {time && <span className="shrink-0 whitespace-nowrap text-[11.5px] font-normal text-ink-3 tabular-nums">{time}</span>}
        {lines.length > 0 && <Icon name="chevron" size={12} stroke={2.2} className={cx("shrink-0 text-ink-3 transition-transform duration-200", open && "rotate-180")} />}
      </button>
      <Collapse open={open} lazy>
        <ol id={body} className="mb-1 ml-[23px] rounded-control bg-field px-3 py-1.5">
          {lines.map((l, i) => (
            <li key={i} className="flex gap-3 py-0.5 text-[12.5px] leading-relaxed">
              <span className="w-[44px] shrink-0 pt-px text-[11.5px] text-ink-3 tabular-nums">{l.clock}</span>
              <span className="min-w-0 flex-1 break-words text-ink-2 select-text">{l.text}</span>
            </li>
          ))}
        </ol>
      </Collapse>
    </div>
  );
}

/** The follow-up seed's meeting material, folded. */
function Material({ title, text }: { title: string; text: string }) {
  const [open, setOpen] = useState(false);
  const body = useId();
  return (
    <div className="px-4 pt-3 pb-0.5">
      <button type="button" aria-expanded={open} aria-controls={body} onClick={() => setOpen((x) => !x)}
        className="flex w-full min-w-0 items-center gap-2.5 rounded-control py-1 text-left text-[12px] text-ink-3 hover:text-ink">
        <Icon name="book" size={13} className="shrink-0" />
        <span className="min-w-0 flex-1 truncate">{title}</span>
        <Icon name="chevron" size={12} stroke={2.2} className={cx("shrink-0 transition-transform duration-200", open && "rotate-180")} />
      </button>
      <Collapse open={open} lazy>
        <pre id={body} className="mb-1 ml-[23px] max-h-72 overflow-auto rounded-control bg-field px-3 py-2 font-sans text-[12px] leading-relaxed break-words whitespace-pre-wrap text-ink-2 select-text">{text}</pre>
      </Collapse>
    </div>
  );
}

/** Why a follow-up could not be made, in words: the meeting still running, nothing to talk about, or the hub's. */
function followupError(err: unknown): string {
  if (err instanceof ApiError && err.code === "meeting_running") return t("knowledge-kbdetail.followup_running");
  if (err instanceof ApiError && err.code === "no_material") return t("knowledge-kbdetail.followup_no_material");
  return t("knowledge-kbdetail.followup_failed", { error: errorText(err) });
}

/** The follow-up's first message (`POST …/followup`): the owner's words, then that conversation opens. A retry after a
 *  lost answer sends the same `id`, which the hub's receipt answers without sending twice; a definite answer, or other
 *  words, make the next try a new call. */
function FollowupComposer({ id, auth, current, manager, onMade }: { id: string; auth: Auth; current: () => boolean; manager: string; onMade: (f: MeetingConversation) => void }) {
  const field = useId();
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const op = useRef("");
  const sending = useRef(false);
  const send = async () => {
    const words = text.trim();
    if (!words || sending.current) return;
    sending.current = true;
    if (!op.current) op.current = uuid();
    setBusy(true); setError("");
    try {
      const r = await api<{ followup?: unknown }>(`/recordings/${enc(id)}/followup`, { method: "POST", json: { id: op.current, text: words }, ...auth });
      const made = meetingConversation(r.followup);
      if (!made) {
        // A 2xx without the follow-up (the hub's receipt: the first call is still running, or its answer is unknown):
        // not definite, so the next try sends the same id, which the receipt answers without sending twice.
        if (current()) setError(t("knowledge-kbdetail.followup_unknown"));
        return;
      }
      op.current = "";
      if (!current()) return;
      setText("");
      onMade(made);
      navigate(conversationPage(made.session_id, made.machine_id, manager));
    } catch (err) {
      if (err instanceof ApiError && err.status >= 400 && err.status < 500) op.current = "";
      if (current()) setError(followupError(err));
    } finally {
      sending.current = false;
      setBusy(false);
    }
  };
  return (
    <form className="px-4 py-3" onSubmit={(ev) => { ev.preventDefault(); void send(); }}>
      <label htmlFor={field} className="mb-1.5 block text-[12px] font-medium text-ink-2">{t("knowledge-kbdetail.followup_label")}</label>
      <textarea id={field} value={text} rows={2} disabled={busy} placeholder={t("knowledge-kbdetail.followup_placeholder")}
        onChange={(ev) => { setText(ev.target.value); op.current = ""; }}
        onKeyDown={(ev) => { if (ev.key === "Enter" && (ev.metaKey || ev.ctrlKey) && !ev.nativeEvent.isComposing) { ev.preventDefault(); void send(); } }}
        className="block w-full resize-y rounded-control bg-field px-3 py-2 text-[13px] leading-relaxed text-ink shadow-btn outline-none placeholder:text-ink-3 focus:ring-1 focus:ring-accent disabled:opacity-70" />
      {error && <p role="alert" className="mt-2 text-[12px] leading-relaxed break-words text-red">{error}</p>}
      <div className="mt-2 flex items-center justify-end gap-2">
        <span className="min-w-0 flex-1 truncate text-[11.5px] text-ink-3">{t("knowledge-kbdetail.followup_hint")}</span>
        <Button type="submit" tone="accent" icon="send" busy={busy} disabled={!text.trim()}>{t("knowledge-kbdetail.followup_send")}</Button>
      </div>
    </form>
  );
}
