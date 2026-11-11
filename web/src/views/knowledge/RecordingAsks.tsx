// "Questions in the meeting" (#2981, docs/assistant/meeting-minutes.md "Attending a meeting"): what was asked while Persona
// attended a recorded meeting, with its answers, and how far the attending session has been fed. Read-only here: asking
// is the phone's (tap to ask); the web only reads `GET /api/v1/recordings/{id}/live`.
import { useEffect, useId, useRef, useState } from "react";
import { t } from "../../i18n";
import { api, ApiError, enc, errorText } from "../../lib/api";
import { formatSpan, isKind, recordingClock, type KBDetail, type RecordingAsk, type RecordingLive, type RecordingView } from "../../lib/kb";
import { href, onLinkClick, type Route } from "../../lib/router";
import { useStore } from "../../lib/store";
import { documentPage, visiblePoll } from "../../lib/visible-poll";
import { Icon } from "../../ui/icons";
import { Card, KBIcon, KIND_ICON, SectionTitle } from "./KBParts";

/** The hub attends meetings (protocol.md "Added by the hub"): only then can a meeting without a role still have asks. */
export const MEETING_ATTENDANCE_FEATURE = "meeting_attendance";
/** How often a recording still being made reads its live state again (the minutes' detail refresh is the same). */
const LIVE_POLL_MS = 5000;

type Props = {
  detail: KBDetail;
  rec: RecordingView;
  auth: { token: string; tokenOwner: string; headers: Record<string, string> };
  current: () => boolean;
};

export function RecordingAsks({ detail, rec, auth, current }: Props) {
  const heading = useId();
  const hubAttends = useStore((s) => s.features.includes(MEETING_ATTENDANCE_FEATURE));
  // Turning attending off mid-meeting keeps its asks, so a meeting on a hub that attends is read even without a role.
  const wanted = !!rec.attendance || (hubAttends && detail.meta?.kind === "meeting");
  const [live, setLive] = useState<RecordingLive | null>(null);
  const [gone, setGone] = useState(false);
  const [error, setError] = useState("");
  const request = useRef(auth);
  request.current = auth;

  useEffect(() => {
    if (!wanted || gone) return;
    let alive = true, reading = false;
    const read = () => {
      if (reading || !current()) return;
      reading = true;
      api<RecordingLive>(`/recordings/${enc(detail.id)}/live`, request.current)
        .then((r) => { if (alive && current()) { setLive(r); setError(""); } })
        .catch((err) => {
          if (!alive || !current()) return;
          // No route (an older hub or manager), no upload, or deleted: there is nothing to show, and nothing to say about it.
          if (err instanceof ApiError && (err.status === 404 || err.status === 410)) setGone(true);
          else setError(errorText(err));
        })
        .finally(() => { reading = false; });
    };
    // A finished recording's questions no longer change: one read.
    if (!rec.partial) { read(); return () => { alive = false; }; }
    const stop = visiblePoll(read, LIVE_POLL_MS, documentPage);
    return () => { alive = false; stop(); };
  }, [wanted, gone, rec.partial, detail.id, current]);

  const asks = live?.asks ?? [];
  if (!wanted || gone || (!rec.attendance && !asks.length)) return null;
  if (!live && !error) return null;
  const status = live ? liveStatus(live, rec.partial) : "";
  // A problem is said in the present: it shows only while the recording is still being made.
  const problem = rec.partial && !!live?.problem;

  return (
    <section aria-labelledby={heading} data-recording-asks>
      <SectionTitle title={<span id={heading}>{t("knowledge-kbdetail.asks_title")}</span>} count={asks.length || undefined} />
      <Card>
        {status && (
          <p role="status" className="flex items-start gap-2 px-4 py-2.5 text-[12.5px] leading-relaxed text-ink-2">
            <Icon name={problem ? "warning" : "mic"} size={13} className="mt-0.5 shrink-0 text-ink-3" />
            <span className="min-w-0 flex-1 break-words">{status}</span>
          </p>
        )}
        {!live && error && <p role="alert" className="px-4 py-2.5 text-[12.5px] leading-relaxed break-words text-red">{t("knowledge-kbdetail.asks_load_failed", { error })}</p>}
        {live && !asks.length && <p className="px-4 py-2.5 text-[12.5px] leading-relaxed text-ink-3">{t("knowledge-kbdetail.asks_empty")}</p>}
        {asks.length > 0 && (
          <ol className="divide-y divide-line">
            {asks.map((ask) => <AskRow key={ask.id} ask={ask} startedAt={rec.startedAt} />)}
          </ol>
        )}
      </Card>
    </section>
  );
}

/** One line on how the attending session follows. While recording: a problem first (an unknown one generically), else how far
 * it is fed. Once complete, what the session was given (`fed`, and of how many when short of them); the problems, said in the
 * present, no longer apply then. */
function liveStatus(live: RecordingLive, recording: boolean): string {
  if (!recording) {
    if (live.attendance === "off") return "";
    return live.fed < live.next
      ? t("knowledge-kbdetail.live_attended_partial", { fed: live.fed, next: live.next })
      : t("knowledge-kbdetail.live_attended", { count: live.fed });
  }
  if (live.problem) {
    const known = ["attendee_unavailable", "attendee_not_allowed", "live_full"].includes(live.problem);
    return t(known ? `knowledge-kbdetail.live_problem_${live.problem}` : "knowledge-kbdetail.live_problem_other");
  }
  if (live.attendance === "off") return t("knowledge-kbdetail.live_off");
  if (live.fed < live.next) return t("knowledge-kbdetail.live_catching_up", { fed: live.fed, next: live.next });
  return live.next ? t("knowledge-kbdetail.live_following", { count: live.next }) : t("knowledge-kbdetail.live_waiting");
}

// A room meeting's (#3862, "Asking the room") add no_answer and room_unavailable.
const ASK_ERRORS = ["model_unavailable", "model_error", "model_timeout", "interrupted", "nothing_heard", "no_answer", "room_unavailable"];

/** Why a question was not answered, in words; an unknown reason generically. */
export function askFailure(ask: RecordingAsk): string {
  return t(ask.error && ASK_ERRORS.includes(ask.error) ? `knowledge-kbdetail.ask_failed_${ask.error}` : "knowledge-kbdetail.ask_failed");
}

export function AskRow({ ask, startedAt }: { ask: RecordingAsk; startedAt?: string }) {
  const heard = ask.question_source === "heard";
  // Spoken: transcribed on the phone from what the owner said to Persona (#3862).
  const spoken = ask.question_source === "spoken";
  const source = t(heard ? "knowledge-kbdetail.ask_heard" : spoken ? "knowledge-kbdetail.ask_spoken" : "knowledge-kbdetail.ask_typed");
  // At its place in the recording; a typed question sent without one at the clock time it was asked.
  const clock = typeof ask.at === "number" ? recordingClock(startedAt, ask.at) : Number.isFinite(Date.parse(ask.asked_at)) ? recordingClock(ask.asked_at, 0) : "";
  const sources = ask.state === "answered" ? ask.sources ?? [] : [];
  return (
    <li className="px-4 py-3" data-ask={ask.id}>
      <div className="flex items-start gap-2.5">
        <span title={source} className="mt-0.5 shrink-0 text-ink-3">
          <Icon name={heard ? "mic" : spoken ? "voice" : "keyboard"} size={13} />
          <span className="sr-only">{source}</span>
        </span>
        <p className="min-w-0 flex-1 break-words text-[13px] font-medium leading-relaxed text-ink">{ask.question.trim() || t("knowledge-kbdetail.ask_heard_empty")}</p>
        {clock && <span className="shrink-0 whitespace-nowrap pt-px text-[11.5px] text-ink-3 tabular-nums">{clock}</span>}
      </div>
      <div className="mt-1 pl-[23px]">
        {ask.state === "answered" ? (
          <div className="flex items-start gap-2">
            <p className="min-w-0 flex-1 break-words text-[13px] leading-relaxed text-ink-2 select-text">{ask.answer}</p>
            {ask.speak && (
              <span title={t("knowledge-kbdetail.ask_speak")} className="mt-0.5 shrink-0 text-ink-3">
                <Icon name="volume" size={13} />
                <span className="sr-only">{t("knowledge-kbdetail.ask_speak")}</span>
              </span>
            )}
          </div>
        ) : ask.state === "pending" ? (
          <p className="text-[12.5px] leading-relaxed text-ink-3">{t("knowledge-kbdetail.ask_pending")}</p>
        ) : ask.state === "failed" ? (
          <p className="text-[12.5px] leading-relaxed text-red">{askFailure(ask)}</p>
        ) : null}
        {sources.length > 0 && (
          <div className="mt-2 flex flex-wrap items-center gap-1.5 text-[11.5px] leading-relaxed text-ink-3">
            <span>{t("knowledge-kbdetail.ask_sources")}</span>
            {sources.map((s) => {
              const to: Route = { name: "kb", kind: s.kind, id: s.id };
              const when = formatSpan(s.start, s.end, s.when);
              // Each opens its knowledge entry (an earlier meeting, or a project).
              return (
                <a key={`${s.kind}/${s.id}`} href={href(to)} onClick={(ev) => onLinkClick(ev, to)}
                  className="inline-flex min-w-0 max-w-full items-center gap-1 rounded-full bg-field px-2 py-0.5 transition-colors hover:bg-hover">
                  <KBIcon name={isKind(s.kind) ? KIND_ICON[s.kind] : "book"} size={11} stroke={2} className="shrink-0" />
                  <span className="truncate font-medium text-accent-ink">{s.name || s.id}</span>
                  {when && <span className="shrink-0 whitespace-nowrap tabular-nums">{when}</span>}
                </a>
              );
            })}
          </div>
        )}
      </div>
    </li>
  );
}
