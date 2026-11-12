// An all-day recording's recap (#4741, docs/assistant/day-recap.md): the day's summary, its timeline by part of the day
// on the recording's own clock (meetings marked, each point a jump into the transcript), the time nothing was recorded
// and why, and suggested to-dos that become to-dos only when confirmed. A day that outgrew one recording continues in
// further parts, each with its own recap: the page merges them by time ("Parts"). It goes through the minutes' routes
// and their write flow (RecordingMinutes); a day recording never offers minutes.
import { useCallback, useEffect, useId, useMemo, useRef, useState } from "react";
import { formatList, t } from "../../i18n";
import { tRich } from "../../i18n/rich";
import { api, ApiError, enc } from "../../lib/api";
import {
  dayClock,
  entryFromDetail,
  isKind,
  minutesKeptUntil,
  recapGaps,
  recapGroups,
  recordingClock,
  recordingView,
  type DayPart,
  type DayRecapEntry,
  type DayRecapGap,
  type KBDetail,
  type KBDetailResponse,
  type KBEntry,
  type MinutesAction,
  type RecordingView,
} from "../../lib/kb";
import { href } from "../../lib/router";
import { Button, copyText, Spinner } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { Card, KBIcon, KIND_ICON, onPlainClick, SectionTitle, TonePill } from "./KBParts";
import { ActionItems, PointList, TranscriptTime, useMinutesDays, useMinutesRequests, type ConfirmedActions, type RecordingAuth } from "./RecordingMinutes";

type Props = {
  detail: KBDetail;
  rec: RecordingView;
  auth: RecordingAuth;
  current: () => boolean;
  refresh: () => Promise<boolean>;
  onSaved: () => void;
  onJump: (id: string) => void;
  onOpen: (e: KBEntry) => void;
};

/** Gaps named past this many say how many more there are. */
const GAPS_SHOWN = 5;

const PART_TITLE: Record<DayPart, string> = {
  morning: "knowledge-kbdetail.recap_morning",
  afternoon: "knowledge-kbdetail.recap_afternoon",
  evening: "knowledge-kbdetail.recap_evening",
};

// The reasons the contract names; any other is shown without one.
const GAP_REASON: Record<string, string> = {
  paused: "knowledge-kbdetail.recap_gap_paused",
  interrupted: "knowledge-kbdetail.recap_gap_interrupted",
  stopped: "knowledge-kbdetail.recap_gap_stopped",
  pending: "knowledge-kbdetail.recap_gap_pending",
  lost: "knowledge-kbdetail.recap_gap_lost",
  erased: "knowledge-kbdetail.recap_gap_erased",
};

/** One part of the day ("Parts" in the contract): its recording (`own`: the one this page shows), its number, and its
 *  view (clock, and the transcript of the page's own part). */
type DayPartView = { detail: KBDetail; rec: RecordingView; part: number; own: boolean };
type FromPart<T> = T & { source: DayPartView };
/** A suggested action keyed by its part (`id` names it within one part's version: `ref`). */
type PartAction = FromPart<MinutesAction> & { ref: string };

/** The recap is kept while a newer one is written or failed, and after the transcript expired (then it can't be
 *  written again). */
export function RecordingDayRecap({ detail, rec, auth, current, refresh, onSaved, onJump, onOpen }: Props) {
  const heading = useId();
  const recap = detail.recap;
  const state = detail.meta?.recap_state;
  // Before the requests: the parts are listed first, and a write is read back with them.
  const { others, found, failed, discover, reread } = useDayParts(detail, rec, auth, current);
  const readAll = useCallback(() => settled(refresh(), reread()), [refresh, reread]);
  const pollAll = useCallback(() => settled(refresh(), discover()), [refresh, discover]);
  const parts: DayPartView[] = [{ detail, rec, part: rec.allDay?.part ?? 1, own: true }, ...others].sort((a, b) => a.part - b.part);
  // Until the day's parts are found and every part's recap is final (or kept past its transcript), more can come
  // without a request of this page's: a failed search for them is tried again with the reads.
  const open = !found || !parts.every((p) => p.rec.trimmedAt || (p.detail.recap?.final === true && p.detail.meta?.recap_state !== "pending"));
  const { busy, needsRefresh, run, confirm, pollNow } = useMinutesRequests({
    id: detail.id, version: recap?.version, pending: state === "pending", open, recap: true, auth, current, refresh: readAll, poll: pollAll, onSaved,
  });
  const minutesDays = useMinutesDays(rec.trimmedAt, auth, current);
  const keptUntil = rec.trimmedAt ? minutesKeptUntil(rec.startedAt, minutesDays) : undefined;

  const disabled = busy || needsRefresh;
  const spoken = rec.lines.some((line) => !line.bookmark);
  const clock = (at: number) => dayClock(rec.startedAt, at, rec.tz);
  const range = (from: number, to: number) => clock(from) === clock(to) ? clock(from) : t("knowledge-kbdetail.recap_range", { from: clock(from), to: clock(to) });
  // This part's times jump into the transcript below; another part's open that part's page, which has its transcript.
  const time = (p: DayPartView) => (at: number) => p.own ? <TranscriptTime rec={rec} at={at} onJump={onJump} /> : <PartTime part={p} at={at} onOpen={onOpen} />;
  // What asking now does: retry a failed one, bring one up to what has arrived, or write the first before the evening.
  const ask = rec.trimmedAt || (!recap && state === "pending") || (!spoken && state !== "failed") ? null
    : state === "failed" ? "knowledge-kbdetail.recap_retry"
      : recap ? (recap.final ? "knowledge-kbdetail.recap_regenerate" : "knowledge-kbdetail.recap_update")
        : "knowledge-kbdetail.recap_write_now";

  // The day's parts merged by time; a single part is shown as it is.
  const multi = parts.length > 1;
  const written = parts.filter((p) => p.detail.recap);
  const entries = written.flatMap((p) => (p.detail.recap!.timeline ?? []).map((entry): FromPart<DayRecapEntry> => ({ ...entry, source: p })));
  const actions = written.flatMap((p) => (p.detail.recap!.actions ?? []).map((a): PartAction => ({ ...a, id: `${p.detail.id}/${a.id}`, ref: a.id, source: p })));
  if (multi) actions.sort((a, b) => a.at - b.at);
  const gaps = multi ? dayGaps(written.map((p) => p.detail.recap!.gaps)) : recap?.gaps;
  const summaries = written.map((p) => p.detail.recap!.summary).filter(Boolean);
  const markdown = written.map((p) => p.detail.recap!.markdown).join("\n\n");
  // Confirmed actions go to the part that suggested them, with that part's recap version.
  const add = (ids: string[]) => {
    const writes = new Map<string, ConfirmedActions>();
    for (const action of ids.map((id) => actions.find((a) => a.id === id))) {
      if (!action) continue;
      const { detail: d } = action.source;
      const write = writes.get(d.id) ?? { id: d.id, version: d.recap!.version, actions: [] };
      write.actions.push(action.ref);
      writes.set(d.id, write);
    }
    void confirm([...writes.values()]);
  };
  const covers = !multi && !recap?.final ? t("knowledge-kbdetail.recap_so_far", { time: clock(recap?.covers?.to ?? 0) })
    // The day's recap is final only once its parts are found and every part's is; until then each part says how far
    // it is.
    : found && written.length === parts.length && written.every((p) => p.detail.recap!.final)
      ? t(written.some((p) => p.detail.recap!.final_reason === "cutoff") ? "knowledge-kbdetail.recap_final_cutoff" : "knowledge-kbdetail.recap_final")
      : parts.map((p, i) => {
        const r = p.detail.recap;
        const label = !r ? t("knowledge-kbdetail.recap_part_none", { part: p.part })
          : r.final ? t("knowledge-kbdetail.recap_part_final", { part: p.part })
            : t("knowledge-kbdetail.recap_part_so_far", { part: p.part, time: clock(r.covers?.to ?? 0) });
        return <span key={p.detail.id}>{i > 0 && " · "}<span className="whitespace-nowrap">{label}</span></span>;
      });

  return (
    <section aria-labelledby={heading} data-recording-recap>
      <SectionTitle title={<span id={heading}>{t("knowledge-kbdetail.recap")}</span>} right={
        <div className="flex shrink-0 items-center gap-1.5">
          {written.length > 0 && <Button tone="quiet" disabled={busy} onClick={() => void copyText(markdown)}>{t("knowledge-kbdetail.minutes_copy")}</Button>}
          {ask && <Button tone="secondary" disabled={disabled || state === "pending"} onClick={() => void run()}>{t(ask)}</Button>}
        </div>
      } />
      {rec.trimmedAt && <p data-recap-kept className="mb-2 px-1 text-[12.5px] leading-relaxed text-ink-3">
        {minutesDays === 0 ? t("knowledge-kbdetail.recap_kept_forever") : keptUntil ? t("knowledge-kbdetail.recap_kept_until", { date: keptUntil }) : t("knowledge-kbdetail.recap_transcript_expired")}
      </p>}
      {!rec.trimmedAt && state === "pending" && <p role="status" className="mb-2 flex items-center gap-2 px-1 text-[12.5px] leading-relaxed text-ink-3">
        <Spinner />{t(recap ? "knowledge-kbdetail.recap_updating" : "knowledge-kbdetail.recap_writing")}
      </p>}
      {!rec.trimmedAt && state === "failed" && <p role="status" className="mb-2 px-1 text-[12.5px] leading-relaxed text-ink-3">{t("knowledge-kbdetail.recap_failed")}</p>}
      {failed && <p role="status" data-recap-parts-failed className="mb-2 flex flex-wrap items-baseline gap-x-1.5 px-1 text-[12.5px] leading-relaxed text-ink-3">
        {t("knowledge-kbdetail.recap_parts_failed")}
        <span aria-hidden>·</span>
        <button type="button" disabled={busy} onClick={pollNow} className="rounded-control font-medium text-accent-ink hover:underline disabled:text-ink-3 disabled:no-underline">
          {t("knowledge-kbdetail.recap_retry")}
        </button>
      </p>}
      {!written.length && !state && !rec.trimmedAt && <p className="px-1 text-[12.5px] leading-relaxed text-ink-3">
        {t(spoken ? "knowledge-kbdetail.recap_absent" : "knowledge-kbdetail.recap_no_text")}
      </p>}
      {needsRefresh && !busy && <div className="mb-2 px-1">
        <Button tone="secondary" onClick={() => void run(undefined, true)}>{t("knowledge-kbdetail.minutes_retry_refresh")}</Button>
      </div>}
      {written.length > 0 && <Card>
        <div className="px-4 py-3.5">
          {summaries.length > 0 && <div className="space-y-1.5">
            {summaries.map((summary, i) => <p key={i} className="break-words text-[14px] leading-relaxed text-ink">{summary}</p>)}
          </div>}
          <div className={summaries.length ? "mt-1.5 space-y-1 text-[12.5px] leading-relaxed" : "space-y-1 text-[12.5px] leading-relaxed"}>
            <p data-recap-covers className="text-ink-2">{covers}</p>
            <Gaps gaps={gaps} rec={rec} />
            {written.some((p) => p.detail.recap!.partial) && <p className="text-ink-3">{t("knowledge-kbdetail.recap_partial")}</p>}
          </div>
        </div>
        {recapGroups(entries, rec.startedAt, rec.tz).map((group, i) => (
          <div key={i} className="px-4 py-3.5">
            {/* A quiet label: the part of the day divides the entries, which are the content. */}
            <h3 className="mb-1.5 text-[12px] font-medium text-ink-3">{t(group.part ? PART_TITLE[group.part] : "knowledge-kbdetail.recap_timeline")}</h3>
            <ol className="divide-y divide-line">
              {group.entries.map((entry, j) => <Entry key={j} entry={entry} range={range(entry.from, entry.to)} time={time(entry.source)} onOpen={onOpen} />)}
            </ol>
          </div>
        ))}
        <ActionItems title={t("knowledge-kbdetail.recap_actions")} actions={actions} disabled={disabled} time={(at, action) => time(action.source)(at)} onAdd={add} />
      </Card>}
    </section>
  );
}

/** Both reads, each to its end: the page's own result, or the first failure. */
async function settled(own: Promise<boolean>, parts: Promise<void>): Promise<boolean> {
  const [a, b] = await Promise.allSettled([own, parts]);
  if (a.status === "rejected") throw a.reason;
  if (b.status === "rejected") throw b.reason;
  return a.value;
}

/** The day's other parts: the recordings listed (`GET /recordings`) with kind day, the same day and the same
 *  started_at, each read like this page (`GET /kb/recordings/{id}`, the same credential and assistant), without its
 *  transcript, which is on its own page. `discover` lists them again (a part may begin while the page shows), `reread`
 *  reads the shown ones again (after a write). A failed read leaves what was shown, except a part gone since; `found`
 *  says the last search listed the parts and read every one, `failed` that it did not (the page says so and tries again).
 *  Answers after the page's route, credential or assistant changed, or after a newer read began, are dropped: the
 *  page's requests are cancelled when it goes, and a late 401 is not the current page's to act on. */
function useDayParts(detail: KBDetail, rec: RecordingView, auth: RecordingAuth, current: () => boolean) {
  const [others, setOthers] = useState<KBDetail[]>([]);
  const [search, setSearch] = useState<"searching" | "found" | "failed">("searching");
  const shown = useRef<KBDetail[]>([]);
  const generation = useRef(0);
  const alive = useRef(true);
  const lifetime = useRef<AbortController | null>(null);
  useEffect(() => {
    alive.current = true;
    const page = new AbortController();
    lifetime.current = page;
    return () => { alive.current = false; page.abort(); };
  }, []);
  const own = detail.id;
  const day = rec.allDay?.day ?? "";
  const start = Date.parse(rec.startedAt ?? "");
  // Without its day or start a recording has no parts to find.
  const searchable = !!day && Number.isFinite(start);
  const sameDay = useCallback((kind: unknown, d: unknown, startedAt: unknown) =>
    kind === "day" && d === day && typeof startedAt === "string" && Date.parse(startedAt) === start, [day, start]);
  const latest = useCallback((n: number) => alive.current && current() && n === generation.current, [current]);
  const options = useCallback((n: number) => ({ ...auth, signal: lifetime.current?.signal, isAuthContextCurrent: () => latest(n) }), [auth, latest]);

  const read = useCallback(async (ids: string[], n: number) => {
    const read = await Promise.allSettled(ids.map((id) => api<KBDetailResponse>(`/kb/recordings/${enc(id)}`, options(n)).then((r) => r.detail, (err) => {
      if (err instanceof ApiError && err.status === 404) return null; // deleted since: the day goes on without it
      throw err;
    })));
    if (!latest(n)) return;
    // A part read is shown as it is now; one that failed as it was shown, if it was.
    const parts = read.flatMap((r, i) => {
      if (r.status === "rejected") return shown.current.filter((d) => d.id === ids[i]);
      const d = r.value;
      return d && d.kind === "recordings" && d.id !== own && sameDay(d.meta?.kind, d.meta?.day, d.meta?.started_at) ? [{ ...d, segments: null }] : [];
    });
    shown.current = parts;
    setOthers(parts);
    const failure = read.find((r) => r.status === "rejected");
    if (failure) throw failure.reason;
  }, [own, sameDay, latest, options]);

  const discover = useCallback(async () => {
    if (!searchable) return;
    const n = ++generation.current;
    try {
      const list = await api<{ recordings?: { id?: unknown; kind?: unknown; day?: unknown; started_at?: unknown }[] | null }>("/recordings", options(n));
      if (!latest(n)) return;
      const ids = (list.recordings ?? []).filter((r) => typeof r?.id === "string" && r.id !== own && sameDay(r.kind, r.day, r.started_at)).map((r) => r.id as string);
      await read(ids, n);
      if (latest(n)) setSearch("found");
    } catch (err) {
      if (latest(n)) setSearch("failed");
      throw err;
    }
  }, [searchable, own, sameDay, latest, options, read]);

  const reread = useCallback(async () => {
    if (shown.current.length) await read(shown.current.map((d) => d.id), ++generation.current);
  }, [read]);

  useEffect(() => { void discover().catch(() => {}); }, [discover]);
  const views = useMemo(() => others.map((d): DayPartView => {
    const view = recordingView(d.kind, d.meta, null)!;
    return { detail: d, rec: view, part: view.allDay?.part ?? 1, own: false };
  }), [others]);
  return { others: views, found: !searchable || search === "found", failed: searchable && search === "failed", discover, reread };
}

/** The clock time an item of another part cites: its transcript is on that part's page, which this opens. */
function PartTime({ part, at, onOpen }: { part: DayPartView; at: number; onOpen: (e: KBEntry) => void }) {
  const clock = recordingClock(part.rec.startedAt, at, part.rec.tz);
  const { detail } = part;
  return (
    <a
      href={href({ name: "kb", kind: "recordings", id: detail.id })}
      aria-label={t("knowledge-kbdetail.recap_part_jump", { time: clock, part: part.part })}
      onClick={(ev) => onPlainClick(ev, () => onOpen(entryFromDetail(undefined, detail)))}
      className="inline-flex shrink-0 items-center gap-1 whitespace-nowrap rounded-control text-[11.5px] text-accent-ink tabular-nums hover:bg-hover"
    >
      <Icon name="clock" size={12} />{clock}
      <span className="text-ink-3">· {t("knowledge-kbdetail.recap_part", { part: part.part })}</span>
    </a>
  );
}

/** The gaps the parts name, each once: a later part holds the day's coverage as an earlier one did (they share
 *  started_at). */
function dayGaps(lists: (DayRecapGap[] | null | undefined)[]): DayRecapGap[] {
  const seen = new Set<string>();
  return lists.flatMap((gaps) => gaps ?? []).filter((g) => {
    const key = JSON.stringify([g?.from, g?.to, g?.reason]);
    if (!g || seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}

/** One stretch of the day: its title and clock range, the meeting it was in, then what was said. */
function Entry({ entry, range, time, onOpen }: { entry: DayRecapEntry; range: string; time: (at: number) => React.ReactNode; onOpen: (e: KBEntry) => void }) {
  const meeting = entry.kind === "meeting";
  // A meeting the knowledge base holds opens there: one of the user's meeting recordings (its minutes), or the item.
  const ref = meeting && entry.meeting?.id ? entry.meeting : null;
  const name = entry.meeting?.name?.trim() || entry.title;
  return (
    <li data-recap-entry={meeting ? "meeting" : "outside"} className="py-2.5 first:pt-0 last:pb-0">
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-0.5">
        <div className="flex min-w-0 flex-1 items-baseline gap-2">
          <h4 className="min-w-0 break-words text-[13px] font-semibold leading-relaxed text-ink">{entry.title}</h4>
          {meeting && <TonePill tone="accent">{t("knowledge-kbdetail.recap_meeting")}</TonePill>}
        </div>
        <span className="shrink-0 whitespace-nowrap text-[11.5px] text-ink-3 tabular-nums">{range}</span>
      </div>
      {ref && isKind(ref.kind) && (
        <a
          href={href({ name: "kb", kind: ref.kind, id: ref.id })}
          onClick={(ev) => onPlainClick(ev, () => onOpen({ kind: ref.kind, id: ref.id, name, relevance: 0 }))}
          className="mt-0.5 inline-flex max-w-full items-center gap-1 rounded-control text-[12px] font-medium text-accent-ink hover:underline"
        >
          <KBIcon name={KIND_ICON[ref.kind]} size={12} stroke={2} className="shrink-0" />
          <span className="truncate">{name}</span>
          <Icon name="chevronRight" size={11} stroke={2.2} className="shrink-0 text-ink-3" />
        </a>
      )}
      {!!entry.points?.length && <div className="mt-1.5"><PointList items={entry.points} time={time} /></div>}
    </li>
  );
}

/** "Not recorded: 10:20–10:45 (interrupted), …": the gaps a minute or longer inside what the recap read. */
function Gaps({ gaps, rec }: { gaps?: DayRecapGap[] | null; rec: RecordingView }) {
  const shown = recapGaps(gaps, rec.startedAt, rec.tz);
  if (!shown.length) return null;
  const items = shown.slice(0, GAPS_SHOWN).map((g) => {
    const range = g.from === g.to ? g.from : t("knowledge-kbdetail.recap_range", { from: g.from, to: g.to });
    return Object.hasOwn(GAP_REASON, g.reason) ? t("knowledge-kbdetail.recap_gap", { range, reason: t(GAP_REASON[g.reason]) }) : range;
  });
  if (shown.length > GAPS_SHOWN) items.push(t("knowledge-kbdetail.recap_gaps_more", { count: shown.length - GAPS_SHOWN }));
  // Joined the language's way, each item kept on one line ("13:40–14:30" never splits at the dash).
  const list = formatList(items.map((_, i) => `{${i}}`)).split(/(\{\d+\})/g).map((piece, i) => {
    const m = /^\{(\d+)\}$/.exec(piece);
    return m ? <span key={i} className="whitespace-nowrap">{items[Number(m[1])]}</span> : piece;
  });
  return <p data-recap-gaps className="break-words text-ink-3">{tRich("knowledge-kbdetail.recap_gaps", { gaps: list })}</p>;
}
