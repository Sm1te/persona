// One knowledge entry (/kb/<kind>/<id>): its tile, name, badges, one line and tags, "Ask Persona"; then the places a
// search found, the Agents on a project, the timeline, the profile, Moments, related entries by kind, code directories,
// a chat's last 7 days and the folder (docs/apple/ios.md "Information architecture", knowledge base). "Tag" in the header.
import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { api, credentialOwner, enc, errorText, getToken, isAbort, subscribeCredentialOwner } from "../../lib/api";
import { MANAGER_HEADER, shownManager, useChosenManager, useKnownDefault } from "../../lib/manager-choice";
import { ConfirmedFacts } from "./ConfirmedFacts";
import { RecordingMinutes } from "./RecordingMinutes";
import { RecordingAsks } from "./RecordingAsks";
import { MeetingPrivate, RecordingConversation, useRecordingMeeting } from "./RecordingConversation";
import { RecordingPhotos } from "./RecordingPhotos";
import { scrollBehavior } from "../../ui/stream/motion";
import { Relationships } from "./Relationships";
import { TopicPoints } from "./TopicPoints";
import { ago, sessionPreview, sessionTitle, shortPath, stateOf, tabLabel } from "../../lib/format";
import {
  agentPreset,
  askPersonaText,
  dayTitle,
  dirsOf,
  entryFromDetail,
  factsOf,
  formatAt,
  hitView,
  isKind,
  kindTitle,
  parseMessageLine,
  parseRecent,
  profile,
  projectInfo,
  recordingView,
  shortWhen,
  statusLabel,
  timeline,
  transcriptLineId,
  type KBDetailResponse,
  type KBEntry,
  type KBMatch,
  type KBMomentRef,
  type RecordingView,
} from "../../lib/kb";
import { machineWithHub } from "../../lib/mesh";
import { openPanel } from "../../lib/panel";
import { href, onLinkClick, type Route } from "../../lib/router";
import { conversationRoute } from "../../lib/inbox";
import { getState, useStore } from "../../lib/store";
import type { Session } from "../../lib/types";
import { Button, Collapse, copyText, cx, IconButton, Loading, StatePill, toast } from "../../ui/atoms";
import { StatusMark } from "../../ui/StatusMark";
import { Icon } from "../../ui/icons";
import { Markdown } from "../../ui/Markdown";
import { known, remember } from "./cache";
import { askPersona } from "../../lib/outbox";
import { openNewAgent } from "../../lib/new-agent";
import { DayDialog, MessageView, MomentDialog, TagEditor } from "./KBDialogs";
import { ErrorBox } from "./KBList";
import { Badges, Card, KBIcon, Marked, MoreRow, onPlainClick, SectionTitle, Tile, TonePill } from "./KBParts";
import { t } from "../../i18n";

/** Timeline lines shown before "Show all". */
const LOG_SHOWN = 6;
/** Related entries of one kind (and Moments posts) shown before "All". */
const LINKS_SHOWN = 4;
/** Search matches shown before "Show all". */
const MATCHES_SHOWN = 5;

export function EmptyDetail() {
  return (
    <div className="flex flex-1 flex-col items-center justify-center px-8 pb-[8vh] text-center" style={{ animation: `fade-in 300ms ease-out both` }}>
      <span className="mb-3 flex size-11 items-center justify-center rounded-[12px] bg-field text-ink-3">
        <Icon name="book" size={20} />
      </span>
      <p className="text-[14px] font-medium text-ink">{t("knowledge-kbdetail.empty_title")}</p>
      <p className="mt-1 max-w-[320px] text-[12.5px] leading-relaxed text-ink-3">{t("knowledge-kbdetail.empty_body")}</p>
    </div>
  );
}

type EntryDetailProps = {
  kind: string;
  id: string;
  /** Opened from a search: the places it was found come first. */
  query?: string;
  reloadKey: number;
  onToggleSidebar: () => void;
  onBack: () => void;
  onOpen: (e: KBEntry) => void;
  onSaved: () => void;
};

export function EntryDetail(props: EntryDetailProps) {
  const owner = useSyncExternalStore(subscribeCredentialOwner, credentialOwner, credentialOwner);
  // the current assistant's entry (an address names no assistant: docs/assistant/managers.md "Clients"): a kept pick is
  // known at once, the default once the roster says which it is
  const picked = useChosenManager();
  const knownDefault = useKnownDefault();
  const manager = picked ?? knownDefault;
  if (!manager || !getToken()) return <Loading />;
  // Retained detail and editors belong to the requested route, owner and assistant: whatever is read and written here
  // (the tags, confirmed facts, relationships, minutes, a chat's days, a post) names that one assistant, and another
  // made current opens it again.
  return <OwnedEntryDetail key={JSON.stringify([props.kind, props.id, owner, manager])} {...props} owner={owner} manager={manager} />;
}

function OwnedEntryDetail({ kind, id, query, reloadKey, onToggleSidebar, onBack, onOpen, onSaved, owner, manager }: EntryDetailProps & { owner: string; manager: string }) {
  const [token] = useState(getToken);
  const current = useCallback(() => credentialOwner() === owner && shownManager() === manager, [owner, manager]);
  const auth = useMemo(() => ({ token, tokenOwner: owner, headers: { [MANAGER_HEADER]: manager } }), [token, owner, manager]);
  const request = useRef(0);
  const [resp, setResp] = useState<KBDetailResponse | null>(null);
  const [error, setError] = useState("");
  const [retry, setRetry] = useState(0);
  const [editing, setEditing] = useState(false);
  const [asking, setAsking] = useState(false);
  const [allLog, setAllLog] = useState(false);
  const [allMatches, setAllMatches] = useState(false);
  const [allMoments, setAllMoments] = useState(false);
  const [showRecent, setShowRecent] = useState(false);
  const [day, setDay] = useState<{ day: string; focus?: number; query?: string } | null>(null);
  const [moment, setMoment] = useState<KBMomentRef | null>(null);
  const scroller = useRef<HTMLDivElement>(null);
  const [highlighted, setHighlighted] = useState("");
  const highlightTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  useEffect(() => () => clearTimeout(highlightTimer.current), []);

  const jumpToTranscript = (lineId: string) => {
    const line = document.getElementById(lineId);
    const page = scroller.current;
    if (!line || !page?.contains(line)) return;
    // Only move the detail's scroller, not the Knowledge list or outer app shell.
    page.scrollTo({ top: page.scrollTop + line.getBoundingClientRect().top - page.getBoundingClientRect().top - page.clientHeight / 3, behavior: scrollBehavior() });
    line.focus({ preventScroll: true });
    clearTimeout(highlightTimer.current);
    setHighlighted(lineId);
    highlightTimer.current = setTimeout(() => setHighlighted(""), 2000);
  };

  const load = useCallback(async (signal?: AbortSignal) => {
    const version = ++request.current;
    const valid = () => current() && version === request.current && !signal?.aborted;
    try {
      const r = await api<KBDetailResponse>(`/kb/${enc(kind)}/${enc(id)}${query ? `?q=${enc(query)}` : ""}`, { signal, ...auth });
      if (!valid()) return false;
      // The server canonicalizes source IDs (including recording UUID case); the request owns this response.
      if (r.detail.kind !== kind) throw new Error(t("knowledge-confirmedfacts.entry_changed"));
      remember(r.detail.links ?? []);
      setResp(r);
      setError("");
      return true;
    } catch (err) {
      if (!valid()) return false;
      if (!isAbort(err)) setError(errorText(err));
      throw err;
    }
  }, [kind, id, query, auth, current]);

  useEffect(() => {
    setError("");
    const ac = new AbortController();
    void load(ac.signal).catch(() => {});
    return () => { ac.abort(); request.current++; };
  }, [load, reloadKey, retry]);

  const d = resp?.detail;

  const base = known(kind, id);
  const e: KBEntry = d ? entryFromDetail(base, d) : (base ?? { kind, id, name: id, relevance: 0 });
  const status = statusLabel(projectInfo(e)?.status);

  // the main conversation opens at once with the message on its way (no main one yet: this starts it)
  const ask = async () => {
    setAsking(true);
    try {
      await askPersona(askPersonaText(e, d));
    } catch (err) {
      toast(t("knowledge-kbdetail.send_failed", { error: errorText(err) }), "error");
    } finally {
      setAsking(false);
    }
  };

  const facts = d ? factsOf(d.kind, d.meta) : null;
  const rec = d ? recordingView(d.kind, d.meta, d.segments) : null;
  // The meeting conversation (#3862): its private mark, room and follow-up, on a hub that lists meeting_conversation;
  // its photos too where the hub lists meeting_photos (after a change of the photos the minutes may have lost a deleted
  // photo's items or be made again: read them, and again on the next poll, reconnect or event if that read fails).
  const reloadDetail = useCallback(() => load(), [load]);
  const meeting = useRecordingMeeting(d, rec, auth, current, manager, reloadDetail);
  const log = timeline(d?.log);
  // A topic's summary is its points as Markdown (for search and Persona): the page shows the points themselves.
  const prof = d?.kind === "topics" ? "" : profile(d?.summary, e.one_line, e.name);
  const dirs = dirsOf(d?.meta);
  const recent = d?.kind === "chats" ? parseRecent(d.recent) : [];
  const into = typeof d?.meta?.merged_into === "string" ? d.meta.merged_into : "";
  const moments = d?.moments ?? [];

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <header className="flex h-14 shrink-0 items-center gap-1.5 border-b border-line px-3 sm:px-4">
        <IconButton icon="sidebar" label={t("knowledge-kbdetail.sidebar")} onClick={onToggleSidebar} className="lg:hidden" />
        <IconButton icon="chevronLeft" label={t("knowledge-kbdetail.back")} onClick={onBack} className="@3xl:hidden" />
        <span className="min-w-0 flex-1 truncate text-[13px] font-medium text-ink-2">{kindTitle(kind)}</span>
        <Button tone="secondary" onClick={() => setEditing(true)} disabled={!d}>
          <KBIcon name="tag" size={14} stroke={2.1} />
          {t("knowledge-kbdetail.tag")}
        </Button>
      </header>

      <div ref={scroller} className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto w-full max-w-[720px] px-4 pt-6 pb-20 sm:px-8">
          {/* Header: what it is at a glance, and the way out (handing it to Persona). */}
          <div>
            <div className="flex items-center gap-3.5">
              <Tile entry={e} size={48} />
              <div className="min-w-0 flex-1">
                <h1 className="line-clamp-3 text-[20px] font-semibold leading-snug tracking-tight text-ink">{e.name}</h1>
                <div className="mt-1.5 flex flex-wrap items-center gap-1.5">
                  <Badges entry={e} status={status} asProject />
                </div>
              </div>
            </div>
            {e.one_line && <p className="mt-3.5 text-[14px] leading-relaxed text-ink">{e.one_line}</p>}
            {e.tags && e.tags.length > 0 && (
              <div className="mt-3 flex flex-wrap gap-1.5">
                {e.tags.map((t) => (
                  <TonePill key={t}>#{t}</TonePill>
                ))}
              </div>
            )}
            <div className="mt-4 flex flex-wrap gap-2">
              <Button tone="accent" icon="spark" busy={asking} disabled={!d} onClick={ask} title={t("knowledge-kbdetail.ask_title")}>
                {t("knowledge-kbdetail.ask")}
              </Button>
              {kind === "projects" && (
                <Button
                  tone="secondary"
                  icon="plus"
                  disabled={!d}
                  onClick={() => openNewAgent(agentPreset(kind, e, d, getState().machines.filter((m) => m.online).map((m) => m.id)))}
                  title={t("knowledge-kbdetail.new_agent_title")}
                >
                  {t("knowledge-kbdetail.new_agent")}
                </Button>
              )}
            </div>
          </div>

          {!d && !error && (
            <div className="py-12">
              <Loading />
            </div>
          )}
          {error && !d && (
            <div className="mt-6">
              <ErrorBox text={error} onRetry={() => setRetry((x) => x + 1)} />
            </div>
          )}

          {d && (
            <div className="mt-7 flex flex-col gap-6" style={{ animation: "fade-in 200ms ease-out both" }}>
              {facts && (
                <section>
                  <Card>
                    {facts.at && (
                      <div className="flex items-center gap-3 px-4 py-2.5 text-[13px]">
                        <span className="flex-1 text-ink-2">{facts.whenLabel}</span>
                        <span className="whitespace-nowrap text-ink tabular-nums">{formatAt(facts.at)}</span>
                      </div>
                    )}
                    {facts.url && (
                      <button type="button" onClick={() => openPanel({ kind: "link", url: facts.url!, title: e.name })} className="flex w-full items-center gap-2 whitespace-nowrap px-4 py-2.5 text-left text-[13px] font-medium text-accent-ink hover:bg-hover">
                        <Icon name="external" size={14} />
                        {facts.openLabel}
                      </button>
                    )}
                  </Card>
                </section>
              )}

              {rec && <RecordingFacts rec={rec} roomMeeting={meeting.roomMeeting} />}
              {rec && <MeetingPrivate meeting={meeting} />}
              {rec && <RecordingMinutes
                detail={d}
                rec={rec}
                auth={auth}
                current={current}
                refresh={load}
                onSaved={onSaved}
                onJump={jumpToTranscript}
                photos={meeting.photos}
              />}
              {rec && <RecordingConversation detail={d} rec={rec} meeting={meeting} auth={auth} current={current} manager={manager} />}
              {/* A room meeting's questions are in its thread; a #2981 meeting's stay here. */}
              {rec && !meeting.roomMeeting && <RecordingAsks
                detail={d}
                rec={rec}
                auth={{ token, tokenOwner: owner, headers: { [MANAGER_HEADER]: manager } }}
                current={current}
              />}
              {rec && <RecordingPhotos rec={rec} photos={meeting.photos} highlighted={highlighted} />}

              {query && resp?.matches && (
                <Matches
                  matches={resp.matches}
                  query={query}
                  all={allMatches}
                  onToggle={() => setAllMatches((x) => !x)}
                  onOpenDay={d.kind === "chats" ? (m) => setDay({ day: m.day!, focus: m.n, query }) : undefined}
                />
              )}

              {resp?.agents && resp.agents.length > 0 && <Agents agents={resp.agents} />}

              {log.length > 0 && (
                <section>
                  <SectionTitle title={t("knowledge-kbdetail.timeline")} count={log.length} />
                  <Card>
                    <ol className="px-4 pt-1.5 pb-1">
                      {(allLog ? log : log.slice(0, LOG_SHOWN)).map((l, i, shown) => {
                        const first = i === 0;
                        const last = i === shown.length - 1 && (allLog || log.length <= LOG_SHOWN);
                        return (
                          <li key={i} className="relative flex gap-3">
                            {/* The rail joins the dots: none above the newest, none below the oldest shown. */}
                            <span aria-hidden className="relative w-3 shrink-0">
                              {!first && <span className="absolute top-0 left-1/2 h-4 w-px -translate-x-1/2 bg-line-strong" />}
                              {!last && <span className="absolute top-4 bottom-0 left-1/2 w-px -translate-x-1/2 bg-line-strong" />}
                              <span className={cx("absolute top-[13px] left-1/2 size-2 -translate-x-1/2 rounded-full", first ? "bg-accent" : "bg-ink-3 opacity-60")} />
                            </span>
                            <div className="min-w-0 flex-1 py-2">
                              {(l.when || l.source) && (
                                <div className={cx("flex min-w-0 items-baseline gap-2 text-[11.5px]", first ? "text-accent-ink" : "text-ink-3")}>
                                  {l.when && <span className="shrink-0 font-semibold tabular-nums">{shortWhen(l.when)}</span>}
                                  {l.source && <span className="truncate">{l.source}</span>}
                                </div>
                              )}
                              <p className="mt-0.5 text-[13px] leading-relaxed text-ink">{l.text}</p>
                            </div>
                          </li>
                        );
                      })}
                    </ol>
                    {log.length > LOG_SHOWN && <MoreRow open={allLog} label={t("knowledge-kbdetail.show_all_entries", { count: log.length })} onClick={() => setAllLog((x) => !x)} />}
                  </Card>
                </section>
              )}

              {isKind(kind) && <ConfirmedFacts
                kind={kind}
                id={id}
                value={typeof d.meta?.pinned === "string" ? d.meta.pinned : ""}
                auth={auth}
                current={current}
                refresh={load}
                onSaved={onSaved}
              />}

              {d.kind === "topics" && <TopicPoints detail={d} onOpen={onOpen} />}

              {prof && (
                <section>
                  <SectionTitle title={t("knowledge-kbdetail.profile")} />
                  <Card>
                    <div className="px-4 py-3.5">
                      <Markdown text={prof} small />
                    </div>
                  </Card>
                </section>
              )}

              {into && (
                <section>
                  <Card>
                    <a
                      href={href({ name: "kb", kind: "projects", id: into })}
                      onClick={(ev) => onPlainClick(ev, () => onOpen({ kind: "projects", id: into, name: into, relevance: 0 }))}
                      className="flex items-center gap-2 whitespace-nowrap px-4 py-2.5 text-[13px] font-medium text-accent-ink hover:bg-hover"
                    >
                      <Icon name="link" size={14} />
                      {t("knowledge-kbdetail.merged")}
                      <span className="flex-1" />
                      <Icon name="chevronRight" size={12} stroke={2.2} className="text-ink-3" />
                    </a>
                  </Card>
                  <p className="mt-1.5 px-1 text-[11.5px] leading-relaxed text-ink-3">{t("knowledge-kbdetail.merged_hint")}</p>
                </section>
              )}

              {moments.length > 0 && (
                <section>
                  <SectionTitle title={t("knowledge-kbdetail.moments")} count={moments.length} />
                  <Card>
                    {(allMoments ? moments : moments.slice(0, LINKS_SHOWN)).map((m) => (
                      <button key={m.id} type="button" onClick={() => setMoment(m)} className="block w-full px-4 py-2.5 text-left transition-colors hover:bg-hover">
                        <span className="flex items-center gap-2 text-[11.5px]">
                          <span className="truncate font-semibold text-ink">{m.author}</span>
                          {ago(m.at) && <span className="shrink-0 text-ink-3">{ago(m.at)}</span>}
                          <span className="flex-1" />
                          {m.comments > 0 && (
                            <span className="flex shrink-0 items-center gap-1 text-ink-3 tabular-nums" aria-label={t("knowledge-kbdetail.comments", { count: m.comments })}>
                              <Icon name="chat" size={12} />
                              {m.comments}
                            </span>
                          )}
                        </span>
                        {m.note && <span className="mt-0.5 block truncate text-[12.5px] font-medium text-accent-ink">{m.note}</span>}
                        <span className="mt-0.5 line-clamp-2 text-[12.5px] leading-snug text-ink-2">{m.text}</span>
                      </button>
                    ))}
                    {moments.length > LINKS_SHOWN && <MoreRow open={allMoments} label={t("knowledge-kbdetail.all_entries", { count: moments.length })} onClick={() => setAllMoments((x) => !x)} />}
                  </Card>
                </section>
              )}

              <Relationships
                detail={d}
                name={e.name}
                auth={auth}
                current={current}
                onOpen={onOpen}
                onRefreshed={(r) => {
                  if (!current()) return;
                  request.current++;
                  remember(r.detail.links ?? []);
                  setResp(r);
                  setError("");
                }}
                onSaved={onSaved}
              />

              {dirs.length > 0 && <Dirs dirs={dirs} />}

              {rec && <Transcript rec={rec} query={query} highlighted={highlighted} />}

              {recent.length > 0 && (
                <section>
                  <Card>
                    <button type="button" onClick={() => setShowRecent((x) => !x)} aria-expanded={showRecent} className="flex h-10 w-full items-center gap-2 px-4 text-left transition-colors hover:bg-hover">
                      <Icon name="chat" size={14} className="text-ink-3" />
                      <span className="flex-1 whitespace-nowrap text-[13px] text-ink">{t("knowledge-kbdetail.last_7_days")}</span>
                      <span className="text-[12px] text-ink-3 tabular-nums">{recent.reduce((n, x) => n + x.messages.length, 0)}</span>
                      <Icon name="chevronRight" size={12} stroke={2.2} className={cx("text-ink-3 transition-transform duration-200", showRecent && "rotate-90")} />
                    </button>
                    <Collapse open={showRecent}>
                      {showRecent && (
                        <div className="border-t border-line px-4 pt-2 pb-3">
                          {recent.map((g, i) => (
                            <div key={g.day ?? i} className="mt-2 first:mt-0">
                              {g.day && (
                                <div className="mb-1 flex items-center gap-2">
                                  <span className="text-[11.5px] font-semibold text-ink-3">{dayTitle(g.day)}</span>
                                  <span className="flex-1" />
                                  <button type="button" onClick={() => setDay({ day: g.day! })} className="whitespace-nowrap rounded-[6px] px-1.5 py-0.5 text-[11.5px] font-medium text-accent-ink hover:bg-hover">
                                    {t("knowledge-kbdetail.view_day")}
                                  </button>
                                </div>
                              )}
                              <div className="flex flex-col gap-2">
                                {g.messages.map((m, j) => (
                                  <MessageView key={j} m={m} />
                                ))}
                              </div>
                            </div>
                          ))}
                        </div>
                      )}
                    </Collapse>
                  </Card>
                </section>
              )}

              <section>
                <SectionTitle title={t("knowledge-kbdetail.directory")} />
                <Card>
                  <div className="flex items-center gap-2 px-4 py-2">
                    <span className="min-w-0 flex-1 font-mono text-[12px] break-all text-ink-2 select-all">{d.path}</span>
                    <IconButton icon="copy" label={t("knowledge-kbdetail.copy_directory")} onClick={() => void copyText(d.path)} />
                  </div>
                </Card>
                <p className="mt-1.5 px-1 text-[11.5px] leading-relaxed text-ink-3">{t("knowledge-kbdetail.directory_hint")}</p>
              </section>
            </div>
          )}
        </div>
      </div>

      {d && <TagEditor entry={e} auth={auth} open={editing} onClose={() => setEditing(false)} onSaved={onSaved} />}
      {d?.kind === "chats" && <DayDialog chat={d.id} chatName={e.name} auth={auth} day={day?.day ?? null} focus={day?.focus} query={day?.query} onClose={() => setDay(null)} />}
      <MomentDialog moment={moment} auth={auth} onClose={() => setMoment(null)} />
    </div>
  );
}

/** Where the search is in this entry: each place with what is around it; a message opens its day at it. */
function Matches({ matches, query, all, onToggle, onOpenDay }: { matches: KBMatch[]; query: string; all: boolean; onToggle: () => void; onOpenDay?: (m: KBMatch) => void }) {
  const shown = all ? matches : matches.slice(0, MATCHES_SHOWN);
  return (
    <section>
      <SectionTitle title={t("knowledge-kbdetail.search_title", { query })} count={matches.length >= 40 ? "40+" : matches.length} />
      <Card>
        {!matches.length && <p className="px-4 py-2.5 text-[12.5px] text-ink-2">{t("knowledge-kbdetail.search_in_name", { query })}</p>}
        {shown.map((m) => {
          const v = hitView(m);
          const body = (
            <>
              {/* a message shows its own time below */}
              <span className="block truncate text-[11px] text-ink-3">{m.day ? t("knowledge-kbdetail.chat_history_day", { day: dayTitle(m.day) }) : v.source}</span>
              {m.day ? (
                <span className="mt-0.5 flex flex-col gap-0.5">
                  {m.before?.length ? <span className="truncate text-[11.5px] text-ink-3">{line(m.before[m.before.length - 1])}</span> : null}
                  <MessageView m={parseMessageLine(m.line)} query={query} clamp />
                  {m.after?.length ? <span className="truncate text-[11.5px] text-ink-3">{line(m.after[0])}</span> : null}
                </span>
              ) : (
                <span className="mt-0.5 line-clamp-4 text-[13px] leading-relaxed text-ink-2">
                  <Marked text={v.text} query={query} />
                </span>
              )}
            </>
          );
          return m.day && onOpenDay ? (
            <button key={`${m.path}#${m.n}`} type="button" onClick={() => onOpenDay(m)} className="block w-full px-4 py-2.5 text-left transition-colors hover:bg-hover">
              {body}
            </button>
          ) : (
            <div key={`${m.path}#${m.n}`} className="px-4 py-2.5">
              {body}
            </div>
          );
        })}
        {matches.length > MATCHES_SHOWN && <MoreRow open={all} label={t("knowledge-kbdetail.show_all_matches", { count: matches.length })} onClick={onToggle} />}
      </Card>
    </section>
  );
}

function line(raw: string) {
  const m = parseMessageLine(raw);
  return m.sender ? t("knowledge-kbdetail.message_line", { sender: m.sender, text: m.text }) : m.text;
}

/** The Agents working in the project's code directories, live from the store when it has them. */
function Agents({ agents }: { agents: Session[] }) {
  const sessions = useStore((s) => s.sessions);
  const machines = useStore((s) => s.machines);
  const live = agents.map((a) => (sessions[a.machine_id] ?? []).find((x) => x.session_id === a.session_id) ?? a);
  return (
    <section>
      <SectionTitle title={t("knowledge-kbdetail.agents")} count={live.length} />
      <Card>
        {live.map((s) => {
          const to = { name: "session" as const, mid: s.machine_id, sid: s.session_id };
          const st = stateOf(s);
          const preview = sessionPreview(s);
          return (
            <a key={`${s.machine_id}/${s.session_id}`} href={href(to)} onClick={(ev) => onLinkClick(ev, to)} className="flex items-center gap-3 px-4 py-2.5 transition-colors hover:bg-hover">
              <span className="flex w-3 shrink-0 justify-center">
                <StatusMark state={st} />
              </span>
              <span className="min-w-0 flex-1">
                <span className="block truncate text-[13px] font-medium text-ink">{sessionTitle(s)}</span>
                <span className="block truncate text-[11.5px] text-ink-3">
                  {[machineWithHub(s.machine_id, machines), s.space_name, tabLabel(s)].filter(Boolean).join(" › ")}
                  {preview ? ` · ${preview}` : ""}
                </span>
              </span>
              <StatePill state={st} />
            </a>
          );
        })}
      </Card>
    </section>
  );
}

/** A recording's when, how long, where it was made; and whether its transcript is still coming. A room meeting (#3862) is
 *  attended by its own room, which its Conversation section shows, not by a session named here. */
function RecordingFacts({ rec, roomMeeting }: { rec: RecordingView; roomMeeting: boolean }) {
  const rows: [string, string][] = [];
  if (rec.startedAt) rows.push([t("knowledge-kbdetail.recording_started"), formatAt(rec.startedAt)]);
  if (rec.duration) rows.push([t("knowledge-kbdetail.recording_duration"), rec.duration]);
  rows.push([t("knowledge-kbdetail.recording_device"), rec.device]);
  if (rec.language) rows.push([t("knowledge-kbdetail.recording_language"), rec.language]);
  if (rec.attendance) rows.push([t("knowledge-kbdetail.recording_persona"), t(rec.attendance === "participant" ? "knowledge-kbdetail.recording_participant" : "knowledge-kbdetail.recording_listener")]);
  return (
    <section>
      <Card>
        {rows.map(([k, v]) => (
          <div key={k} className="flex items-center gap-3 px-4 py-2.5 text-[13px]">
            <span className="flex-1 text-ink-2">{k}</span>
            <span className="whitespace-nowrap text-ink tabular-nums">{v}</span>
          </div>
        ))}
        {rec.attendance && !roomMeeting && (
          <div className="flex items-center gap-3 px-4 py-2.5 text-[13px]">
            <span className="flex-1 whitespace-nowrap text-ink-2">{t("knowledge-kbdetail.recording_attending")}</span>
            <AttendingSession session={rec.attendingSession} />
          </div>
        )}
      </Card>
      {rec.partial && <p className="mt-1.5 px-1 text-[11.5px] leading-relaxed text-ink-3">{t("knowledge-kbdetail.recording_partial")}</p>}
    </section>
  );
}

/** The session attending a meeting, opening its chat: a Persona conversation, an Agent, or the main chat when none was
 *  named (docs/assistant/meeting-minutes.md "Attendance fields"). */
function AttendingSession({ session }: { session?: RecordingView["attendingSession"] }) {
  const sessions = useStore((s) => s.sessions);
  const assistant = useStore((s) => s.assistant);
  const machines = useStore((s) => s.machines);
  let to: Route | null = null;
  let label = t("knowledge-kbdetail.recording_main_chat");
  if (session) {
    const { machineId: mid, sessionId: sid } = session;
    const row = (sessions[mid] ?? []).find((x) => x.session_id === sid)
      ?? (assistant?.machine_id === mid ? assistant.conversations.find((c) => c.session_id === sid) : undefined);
    to = conversationRoute(mid, sid, sessions, assistant);
    label = row ? sessionTitle(row) : t("knowledge-kbdetail.recording_attending_on", { machine: machineWithHub(mid, machines) });
  } else if (assistant?.main) {
    to = { name: "persona", sid: assistant.main };
  }
  if (!to) return <span className="min-w-0 truncate text-ink">{label}</span>;
  const route = to;
  return (
    <a href={href(route)} onClick={(ev) => onLinkClick(ev, route)} title={label} className="min-w-0 truncate font-medium text-accent-ink hover:underline">
      {label}
    </a>
  );
}

/** The whole transcript, a line per segment at its clock time; bookmarks inline. */
function Transcript({ rec, query, highlighted }: { rec: RecordingView; query?: string; highlighted: string }) {
  return (
    <section>
      <SectionTitle title={t("knowledge-kbdetail.transcript")} count={rec.lines.filter((l) => !l.bookmark).length} />
      <Card>
        {/* Trimmed: the bookmarks (in its metadata) are all that is left of it. */}
        {(rec.trimmedAt || !rec.lines.length) && <p className="px-4 py-2.5 text-[12.5px] text-ink-3">{t(rec.trimmedAt ? "knowledge-kbdetail.transcript_expired" : "knowledge-kbdetail.transcript_empty")}</p>}
        <ol className="px-4 py-1.5">
          {rec.lines.map((l, i) => (
            <li key={i} id={transcriptLineId(l.seconds, l.bookmark)} tabIndex={-1} className={cx("flex gap-3 rounded-control py-1.5 text-[13px] leading-relaxed transition-colors", highlighted === transcriptLineId(l.seconds, l.bookmark) && "bg-accent-tint")}>
              <span className={cx("w-[60px] shrink-0 pt-px text-[11.5px] tabular-nums", l.bookmark ? "font-semibold text-accent-ink" : "text-ink-3")}>{l.clock}</span>
              {l.bookmark ? (
                <span className="min-w-0 flex-1 font-medium text-accent-ink">
                  {t("knowledge-kbdetail.bookmark")}
                  {l.text ? ` · ${l.text}` : ""}
                </span>
              ) : (
                <span className="min-w-0 flex-1 text-ink select-text">{query ? <Marked text={l.text} query={query} /> : l.text}</span>
              )}
            </li>
          ))}
        </ol>
      </Card>
    </section>
  );
}

function Dirs({ dirs }: { dirs: { machine: string; path: string }[] }) {
  const machines = useStore((s) => s.machines);
  return (
    <section>
      <SectionTitle title={t("knowledge-kbdetail.code_dirs")} count={dirs.length} />
      <Card>
        {dirs.map((x) => (
          <div key={`${x.machine}:${x.path}`} className="flex items-center gap-2.5 px-4 py-2">
            <Icon name="folder" size={14} className="shrink-0 text-ink-3" />
            <span className="min-w-0 flex-1 truncate font-mono text-[12px] text-ink" title={x.path}>
              {shortPath(x.path)}
            </span>
            {x.machine && <span className="shrink-0 whitespace-nowrap text-[11.5px] text-ink-3">{machineWithHub(x.machine, machines)}</span>}
            <IconButton icon="copy" label={t("knowledge-kbdetail.copy_path")} size={24} iconSize={13} onClick={() => void copyText(x.path)} />
          </div>
        ))}
      </Card>
    </section>
  );
}
