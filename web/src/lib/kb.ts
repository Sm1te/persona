// The knowledge base (docs/assistant/knowledge.md "App API"): the hub's wire types and the pure logic of the "Knowledge" page —
// list grouping and ranking, search results merged by entry with the words marked, which tags a row has room for,
// the timeline, the profile without what the header shows, and the message "Ask Persona" sends. The iOS app is the
// reference (apple/Persona/Views/Knowledge/KnowledgeView.swift, apple/Shared/Knowledge.swift).
import type { TopicPoint } from "./kb-points";
import { formatDate, t } from "../i18n";
import { contextBody } from "./context-body";
import type { Session } from "./types";

export const KB_KINDS = ["projects", "topics", "chats", "people", "docs", "meetings", "events", "recordings"] as const;
export type KBKind = (typeof KB_KINDS)[number];

export const KIND_TITLE: Record<KBKind, string> = { projects: t("lib-kb.kind_projects"), topics: t("lib-kb.kind_topics"), chats: t("lib-kb.kind_chats"), people: t("lib-kb.kind_people"), docs: t("lib-kb.kind_docs"), meetings: t("lib-kb.kind_meetings"), events: t("lib-kb.kind_events"), recordings: t("lib-kb.kind_recordings") };

export function isKind(k: unknown): k is KBKind {
  return typeof k === "string" && (KB_KINDS as readonly string[]).includes(k);
}

/** The kind a route names; anything else is the default list, projects. */
export function kindOf(k?: string): KBKind {
  return isKind(k) ? k : "projects";
}

export function kindTitle(k: string): string {
  return isKind(k) ? KIND_TITLE[k] : k;
}

// ── wire types ──────────────────────────────────────────

export interface KBEntry {
  kind: string;
  id: string;
  name: string;
  one_line?: string;
  importance?: string;
  relevance: number;
  tags?: string[] | null;
  updated?: string;
  /** projects: "short · active" (kind · status); chats: dm / group…; topics: how many knowledge points, in decimal */
  extra?: string;
}

export interface KBList {
  items: KBEntry[] | null;
  tags?: string[] | null;
}

export interface KBMomentRef {
  id: string;
  author: string;
  text: string;
  at?: string;
  comments: number;
  note?: string;
  /** The name of the feed the post came from (whoever archived it says); none: Moments. */
  feed?: string;
}

/** What to call a post's feed: its archived name, else Moments. */
export function momentFeed(m: Pick<KBMomentRef, "feed">): string {
  return m.feed?.trim() || t("lib-kb.moments");
}

export interface KBDetail {
  kind: string;
  id: string;
  path: string;
  meta?: Record<string, unknown> | null;
  summary?: string;
  log?: string[] | null;
  recent?: string;
  links?: KBEntry[] | null;
  moments?: KBMomentRef[] | null;
  /** recordings: the transcript, seconds from the start (docs/protocol/protocol.md "Recording sync"). */
  segments?: KBSegment[] | null;
  /** Recordings may retain older minutes while a newer revision is pending or failed. */
  minutes?: RecordingMinutes | null;
  /** topics: the knowledge points (docs/assistant/knowledge.md "Knowledge points"). */
  points?: TopicPoint[] | null;
}

/** A topic's point count from its list entry (extra) or its metadata (points). */
export function topicPointCount(e: Pick<KBEntry, "kind" | "extra">, meta?: Record<string, unknown> | null): number | null {
  if (e.kind !== "topics") return null;
  if (typeof meta?.points === "number") return meta.points;
  const n = Number.parseInt(e.extra ?? "", 10);
  return Number.isFinite(n) ? n : null;
}

export interface MinutesPoint {
  text: string;
  at: number;
  who?: string;
  /** The meeting photo it cites instead of a transcript line (#3862 phase 3): its `n`; `at` is then the photo's. */
  photo?: number;
}

export interface MinutesAction {
  id: string;
  who?: string;
  what: string;
  by?: string;
  due_date?: string;
  at: number;
  todo_id?: string;
  /** The meeting photo it cites instead of a transcript line (#3862 phase 3): its `n`; `at` is then the photo's. */
  photo?: number;
}

export interface RecordingMinutes {
  version: number;
  revision: number;
  generated_at: string;
  title: string;
  /** The hub's fixed-format span ("2026-11-10 14:00–15:10"), shown only without `start` / `end` (an older hub). */
  when: string;
  /** The recording's span (RFC 3339, #4713), formatted here like the app's other dates (`formatSpan`). */
  start?: string;
  end?: string;
  where?: string;
  attendees?: string[];
  points?: MinutesPoint[];
  decisions?: MinutesPoint[];
  actions?: MinutesAction[];
  open?: MinutesPoint[];
  partial?: boolean;
  markdown: string;
}

export interface KBSegment {
  start: number;
  end: number;
  text: string;
}

/** One question asked in an attended meeting (docs/assistant/meeting-minutes.md "Attending a meeting", `Ask`). `state`,
 *  `question_source` and `error` are open sets: an unknown value is shown generically. */
export interface RecordingAsk {
  id: string;
  question: string;
  question_source: string;
  at?: number;
  state: string;
  answer: string;
  lang?: string;
  speak?: boolean;
  /** An earlier meeting's `when` / `start` / `end` are its minutes' (#4713). */
  sources?: { kind: string; id: string; name: string; when?: string; start?: string; end?: string }[] | null;
  error?: string;
  asked_at: string;
  answered_at?: string;
  /** A room meeting's (#3862, "Asking the room"): how it is answered once its first attempt went out (`room` or
   *  `material`), and the lines a heard question was made of. */
  via?: string;
  heard?: KBSegment[] | null;
}

/** A room meeting's room conversation as `GET …/live` and `detail.meta` give it (docs/assistant/meeting-minutes.md
 *  "Fields on `GET …/live` and `detail.meta`", #3862). `state` and `problem` are open sets: an unknown state reads as
 *  `starting`; the ids come once it has started. */
export interface MeetingRoom {
  state: string;
  machine_id?: string;
  session_id?: string;
  problem?: string;
}

/** One of a meeting's conversations (its follow-up), by ids. */
export interface MeetingConversation {
  machine_id: string;
  session_id: string;
}

/** `GET /api/v1/recordings/{id}/live`: how far the attending session has been fed, and the meeting's questions. */
export interface RecordingLive {
  next: number;
  fed: number;
  attendance: string;
  attending_session?: { machine_id: string; session_id: string; main?: boolean };
  /** Open set: attendee_unavailable, attendee_not_allowed, live_full today. */
  problem?: string;
  asks?: RecordingAsk[] | null;
  /** The meeting conversation (#3862): the private mark (absent until chosen), a room meeting's room, the follow-up once
   *  made, and each part's revision (`room`, `asks`, `private`, `followup`; numbers that only go up). `photos` has a
   *  revision here and no field: its list is `GET …/photos` (phase 3). */
  private?: boolean;
  room?: MeetingRoom;
  followup?: MeetingConversation;
  revs?: Record<string, number>;
}

/** One of a meeting's photos as `GET /api/v1/recordings/{id}/photos` lists them (docs/assistant/meeting-minutes.md
 *  "Phase 3: photos", #3862): `n` counts from 1 per recording and is never reused, `at` is seconds from the start,
 *  `text` what the phone recognized in it, `caption` the owner's words, `note` the room's description, `cited` whether
 *  the current minutes cite it, `forwarded` whether it went into the room. Its image is `GET …/photos/{n}`. */
export interface RecordingPhoto {
  n: number;
  id: string;
  at: number;
  mime: string;
  width: number;
  height: number;
  text?: string;
  caption?: string;
  note?: string;
  cited: boolean;
  forwarded: boolean;
  added_at: string;
}

/** The photo a minutes item cites (`"photo": n`), when it names one. */
export function citedPhoto(item: { photo?: unknown }): number | undefined {
  return Number.isInteger(item.photo) && (item.photo as number) > 0 ? (item.photo as number) : undefined;
}

/** `GET …/photos`'s list read defensively, oldest first: entries without a positive whole `n` or a string `id` are left
 *  out, blank texts read as absent. */
export function recordingPhotos(v: unknown): RecordingPhoto[] {
  const raw = v && typeof v === "object" && !Array.isArray(v) ? (v as { photos?: unknown }).photos : undefined;
  if (!Array.isArray(raw)) return [];
  const out: RecordingPhoto[] = [];
  for (const x of raw as Record<string, unknown>[]) {
    if (!x || typeof x !== "object" || !Number.isInteger(x.n) || (x.n as number) <= 0 || typeof x.id !== "string" || !x.id) continue;
    const words = (k: "text" | "caption" | "note") => (typeof x[k] === "string" && (x[k] as string).trim() ? { [k]: (x[k] as string).trim() } : {});
    out.push({
      n: x.n as number, id: x.id, at: typeof x.at === "number" && Number.isFinite(x.at) ? Math.max(0, x.at) : 0,
      mime: str(x.mime) ?? "", width: Number(x.width) || 0, height: Number(x.height) || 0,
      ...words("text"), ...words("caption"), ...words("note"),
      cited: x.cited === true, forwarded: x.forwarded === true, added_at: str(x.added_at) ?? "",
    });
  }
  return out.sort((a, b) => a.n - b.n);
}

/** What `detail.meta` (or a `GET …/live`) says of a recording's meeting conversation (#3862): well-formed values only. */
export interface MeetingMeta {
  room?: MeetingRoom;
  followup?: MeetingConversation;
  private?: boolean;
}

/** The room, follow-up and private mark a recording's metadata carries, read defensively. */
export function meetingMeta(meta?: Record<string, unknown> | null): MeetingMeta {
  const out: MeetingMeta = {};
  const room = meetingRoom(meta?.room);
  if (room) out.room = room;
  const followup = meetingConversation(meta?.followup);
  if (followup) out.followup = followup;
  if (typeof meta?.private === "boolean") out.private = meta.private;
  return out;
}

/** A room as the apps read it: an object with a string state; its ids only both together, its problem only when given. */
export function meetingRoom(v: unknown): MeetingRoom | undefined {
  if (!v || typeof v !== "object" || Array.isArray(v)) return undefined;
  const r = v as Record<string, unknown>;
  if (typeof r.state !== "string") return undefined;
  return { state: r.state, ...meetingConversation(r), ...(typeof r.problem === "string" && r.problem ? { problem: r.problem } : {}) };
}

/** A conversation's ids, both non-empty strings, or nothing. */
export function meetingConversation(v: unknown): MeetingConversation | undefined {
  if (!v || typeof v !== "object" || Array.isArray(v)) return undefined;
  const r = v as Record<string, unknown>;
  return typeof r.machine_id === "string" && r.machine_id && typeof r.session_id === "string" && r.session_id
    ? { machine_id: r.machine_id, session_id: r.session_id } : undefined;
}

/** Whether a recording is a meeting as #2981 counts them (`kind: meeting` or minutes asked for): only those take the
 *  private mark (docs/assistant/meeting-minutes.md "The private mark"). */
export function countsAsMeeting(meta?: Record<string, unknown> | null): boolean {
  return meetingKind(meta) || meta?.minutes_asked === true;
}

/** Whether a recording's `kind` is `meeting`: only those take photos (#3862 phase 3, `409 not_meeting` otherwise). */
export function meetingKind(meta?: Record<string, unknown> | null): boolean {
  return str(meta?.kind)?.trim().toLowerCase() === "meeting";
}

/** A line a search found: `path` relative to the data dir, `n` its line number in that file. */
export interface KBFound {
  path: string;
  line: string;
  n?: number;
}

/** One place in an entry that matched the search it was opened from; a message comes with the ones around it. */
export interface KBMatch {
  path: string;
  n: number;
  line: string;
  day?: string;
  before?: string[] | null;
  after?: string[] | null;
}

export interface KBDetailResponse {
  detail: KBDetail;
  agents?: Session[] | null;
  matches?: KBMatch[] | null;
}

/** One entry a search found (`/kb-search?results=1`): how many lines matched and the first few. */
export interface KBResult {
  entry: KBEntry;
  count: number;
  hits?: KBFound[] | null;
  in_name?: boolean;
}

export interface KBDay {
  day: string;
  messages: { n: number; text: string }[] | null;
  prev?: string;
  next?: string;
}

/** `PUT /kb/{kind}/{id}` (the hub's kb.Update); tagging set here is locked against the automatic one. */
export interface KBUpdate {
  remove_people?: string[];
  remove_chats?: string[];
  remove_projects?: string[];
  /** Omitted preserves confirmed facts; an explicit empty string clears them. */
  pinned?: string;
  importance?: string;
  relevance?: number;
  tags?: string[];
  status?: string;
  project_kind?: string;
}

// ── tagging ─────────────────────────────────────────────

export type Tone = "red" | "orange" | "accent" | "neutral";

/** P0 red, P1 orange, P2 blue, P3 and unranked grey. */
export function importanceTone(p?: string): Tone {
  return p === "P0" ? "red" : p === "P1" ? "orange" : p === "P2" ? "accent" : "neutral";
}

/** Relevance to the user (0–100): its pill is red from 70, orange from 40, neutral below. */
export function relevanceTone(n: number): Tone {
  return n >= 70 ? "red" : n >= 40 ? "orange" : "neutral";
}

export interface ProjectInfo {
  kind: "short" | "long";
  status: string;
}

/** A project's kind and status from the list's `extra` ("long · done"); null for other kinds. */
export function projectInfo(e: Pick<KBEntry, "kind" | "extra">): ProjectInfo | null {
  if (e.kind !== "projects") return null;
  const [k, st] = (e.extra ?? "").split(" · ");
  return { kind: k?.trim() === "long" ? "long" : "short", status: st?.trim() || "active" };
}

/** "Short-term" / "Long-term", only when the hub said which. */
export function projectKindLabel(e: Pick<KBEntry, "kind" | "extra">): string | null {
  if (e.kind !== "projects" || !e.extra) return null;
  return e.extra.startsWith("long") ? t("lib-kb.project_long") : t("lib-kb.project_short");
}

const STATUS_LABEL: Record<string, string> = { paused: t("lib-kb.status_paused"), done: t("lib-kb.status_done"), ignored: t("lib-kb.status_ignored"), merged: t("lib-kb.status_merged") };

/** A project's status when it is not the usual "In progress". */
export function statusLabel(status?: string): string | null {
  return (status && STATUS_LABEL[status]) || null;
}

// ── lists ───────────────────────────────────────────────

function importanceRank(p?: string): number {
  const m = /^P([0-3])$/.exec(p ?? "");
  return m ? Number(m[1]) : 4;
}

/** The hub's kb.Rank: importance, then relevance, then the most recently updated. Stable. */
export function rankEntries<T extends Pick<KBEntry, "importance" | "relevance" | "updated">>(list: T[]): T[] {
  return list
    .map((e, i) => ({ e, i }))
    .sort((a, b) => {
      const d = importanceRank(a.e.importance) - importanceRank(b.e.importance);
      if (d) return d;
      if (a.e.relevance !== b.e.relevance) return b.e.relevance - a.e.relevance;
      const t = (Date.parse(b.e.updated ?? "") || 0) - (Date.parse(a.e.updated ?? "") || 0);
      return t || a.i - b.i;
    })
    .map((x) => x.e);
}

export interface EntrySection {
  key: string;
  title: string;
  items: KBEntry[];
  /** Folded until opened ("Not a project", the people nobody tagged yet). */
  fold?: boolean;
  footer?: string;
}

/**
 * A kind's list as the app shows it. Projects: "Focus" (P0 / P1), "Other active", "Done", then "Not a project" folded. Other kinds:
 * the ranked ones, then the rest (only a name in a group, no tagging yet) folded under "Not sorted yet"; with a tag, one
 * section of everything carrying it.
 */
export function groupEntries(kind: KBKind, items: KBEntry[], tag?: string): EntrySection[] {
  const list = rankEntries(items);
  let out: EntrySection[];
  if (kind === "projects") {
    const st = (e: KBEntry) => projectInfo(e)?.status;
    const active = list.filter((e) => st(e) !== "done" && st(e) !== "ignored");
    const focus = active.filter((e) => e.importance === "P0" || e.importance === "P1");
    const rest = active.filter((e) => e.importance !== "P0" && e.importance !== "P1");
    out = [
      { key: "focus", title: t("lib-kb.section_focus"), items: focus },
      { key: "active", title: focus.length ? t("lib-kb.section_other_active") : t("lib-kb.status_active"), items: rest },
      { key: "done", title: t("lib-kb.status_done"), items: list.filter((e) => st(e) === "done") },
      {
        key: "ignored",
        title: t("lib-kb.status_ignored"),
        items: list.filter((e) => st(e) === "ignored"),
        fold: true,
        footer: t("lib-kb.ignored_footer"),
      },
    ];
  } else if (kind === "topics") {
    // Topics are curated as a whole: one list, the ones with the most knowledge first after any importance set by hand.
    const n = (e: KBEntry) => topicPointCount(e) ?? 0;
    const sorted = [...items].sort((a, b) => importanceRank(a.importance) - importanceRank(b.importance) || n(b) - n(a) || a.name.localeCompare(b.name));
    out = [{ key: "all", title: tag ? `#${tag}` : t("lib-kb.section_all"), items: sorted }];
  } else if (tag) {
    out = [{ key: "tag", title: `#${tag}`, items: list }];
  } else {
    const ranked = list.filter((e) => e.importance);
    const rest = list.filter((e) => !e.importance);
    out = [
      { key: "ranked", title: t("lib-kb.section_ranked"), items: ranked },
      {
        key: "rest",
        title: ranked.length ? t("lib-kb.section_unsorted") : t("lib-kb.section_all"),
        items: rest,
        fold: ranked.length > 0,
        footer: ranked.length ? t(`lib-kb.unsorted_footer_${kind}`) : undefined,
      },
    ];
  }
  return out.filter((s) => s.items.length);
}

/** 1985 → "1985" (no grouping comma, the chip is narrow), past 9999 → "10K+" (in Chinese units). */
export function countLabel(n: number): string {
  return n > 9999 ? t("lib-kb.count_over") : String(n);
}

/**
 * The tags a row shows: the list's filter tag left out (every row has it), duplicates and blanks dropped, at most
 * `max`. The row itself then shows as many of these as fit whole (a cut "#Manage…" says nothing).
 */
export function rowTags(e: Pick<KBEntry, "tags">, filter?: string, max = 3): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const raw of e.tags ?? []) {
    const t = raw.trim().replace(/^#+/, "");
    const key = t.toLowerCase();
    if (!t || seen.has(key) || (filter && key === filter.toLowerCase())) continue;
    seen.add(key);
    out.push(t);
    if (out.length >= max) break;
  }
  return out;
}

/** What the tag field holds: words split on spaces or commas, a leading # dropped, each once. */
export function parseTags(input: string): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const raw of input.split(/[\s,，、;；]+/)) { // i18n-ignore: full-width separators people type
    const t = raw.replace(/^#+/, "").trim();
    if (!t || seen.has(t.toLowerCase())) continue;
    seen.add(t.toLowerCase());
    out.push(t);
  }
  return out;
}

const SKIP_INITIAL = new Set([..."#@（(【[「『《〈\"'“‘"]); // i18n-ignore: punctuation skipped in names

/** A person's or chat's tile: the first character that says something, uppercased. */
export function initial(name: string): string {
  for (const c of Array.from(name)) {
    if (/\s/.test(c) || SKIP_INITIAL.has(c)) continue;
    return c.toUpperCase();
  }
  return "";
}

// ── search ──────────────────────────────────────────────

export function queryWords(q: string): string[] {
  return q.trim().toLowerCase().split(/\s+/).filter(Boolean);
}

export interface Part {
  text: string;
  hit: boolean;
}

/** The text cut into runs, every word searched for marked (case-insensitive; the hub matches lines with all of them). */
export function markParts(text: string, query: string): Part[] {
  const words = queryWords(query);
  if (!text || !words.length) return text ? [{ text, hit: false }] : [];
  // Lowercasing can change the length of a few characters; then the words are looked for as typed.
  const lower = text.toLowerCase();
  const hay = lower.length === text.length ? lower : text;
  const marked = new Array<boolean>(text.length).fill(false);
  for (const w of words) {
    for (let i = hay.indexOf(w); i >= 0; i = hay.indexOf(w, i + w.length)) {
      for (let j = i; j < i + w.length; j++) marked[j] = true;
    }
  }
  const out: Part[] = [];
  for (let i = 0; i < text.length; i++) {
    const last = out[out.length - 1];
    if (last && last.hit === marked[i]) last.text += text[i];
    else out.push({ text: text[i], hit: marked[i] });
  }
  return out;
}

export interface ResultGroup {
  kind: string;
  title: string;
  results: KBResult[];
}

/** Results by kind, the kinds in the order of their best result (the hub ranks across kinds). */
export function groupResults(results: KBResult[]): ResultGroup[] {
  const by = new Map<string, KBResult[]>();
  for (const r of results) {
    const k = r.entry.kind;
    if (!by.has(k)) by.set(k, []);
    by.get(k)!.push(r);
  }
  return [...by].map(([kind, rs]) => ({ kind, title: kindTitle(kind), results: rs }));
}

/** The day of a message line ("2026-10-30"), null for the other files. */
export function hitDay(path: string): string | null {
  return /\/messages\/(\d{4}-\d{2}-\d{2})\.md$/.exec(path)?.[1] ?? null;
}

/** The line without its Markdown list marker, JSON key and quotes. */
export function hitText(line: string): string {
  let t = line.trim();
  for (const p of ["- ", "* ", "## ", "# "]) if (t.startsWith(p)) t = t.slice(p.length);
  if (t.startsWith('"')) {
    const i = t.indexOf('": ');
    if (i > 0) t = t.slice(i + 3);
  }
  return t.replace(/^[\s,"]+|[\s,"]+$/g, "");
}

export interface MessageLine {
  time: string;
  sender: string;
  text: string;
}

/** An archived message, `- 14:03 <name>: text` (continuation lines after a newline): when, who, what. */
export function parseMessageLine(raw: string): MessageLine {
  const t = raw.startsWith("- ") ? raw.slice(2) : raw;
  const m = /^(\d\d:\d\d) ([\s\S]*)$/.exec(t);
  if (m) {
    const i = m[2].indexOf(": ");
    if (i >= 0) return { time: m[1], sender: m[2].slice(0, i), text: tidy(m[2].slice(i + 2)) };
  }
  return { time: "", sender: "", text: tidy(t) };
}

function tidy(s: string) {
  return s.replace(/\n[ \t]+/g, "\n").trim();
}

/** A found line said plainly: where it is ("Profile", "Timeline", "Chat history" · day and time) and what it says. */
export function hitView(f: Pick<KBFound, "path" | "line">, now = new Date()): { source: string; text: string; day: string | null } {
  const day = hitDay(f.path);
  if (day) {
    const m = parseMessageLine(hitText(f.line));
    return { source: t("lib-kb.hit_chat", { day: `${dayTitle(day, now)}${m.time ? ` ${m.time}` : ""}` }), text: m.sender ? t("lib-kb.hit_sender", { sender: m.sender, text: m.text }) : m.text, day };
  }
  const file = f.path.split("/").pop() ?? f.path;
  const source = file === "summary.md" ? t("lib-kb.hit_profile") : file === "log.md" ? t("lib-kb.hit_timeline") : file === "transcript.md" ? t("lib-kb.hit_transcript") : file === "minutes.md" ? t("lib-kb.hit_minutes") : file.endsWith(".json") ? t("lib-kb.hit_data") : file;
  return { source, text: hitText(f.line), day: null };
}

// ── detail ──────────────────────────────────────────────

export interface LogLine {
  when: string;
  source: string;
  text: string;
}

/** A timeline line, `- 2026-10-30 23:16 · Agent · <title> · <machine> · <what was said>`: when, from where, what. */
export function parseLogLine(raw: string): LogLine {
  let t = raw.trim();
  if (t.startsWith("- ")) t = t.slice(2);
  const parts = t.split(" · ");
  if (parts.length >= 2 && /^\d/.test(parts[0])) {
    return { when: parts[0], source: parts.slice(1, -1).join(" · "), text: parts[parts.length - 1] };
  }
  return { when: "", source: "", text: t };
}

/** The timeline newest first (the hub keeps it oldest first). */
export function timeline(log?: string[] | null): LogLine[] {
  return (log ?? []).filter((l) => l.trim()).map(parseLogLine).reverse();
}

/** "2026-10-30 23:16" → "10-30 23:16"; the year stays when it is not this one. */
export function shortWhen(when: string, now = new Date()): string {
  const y = `${now.getFullYear()}-`;
  return when.startsWith(y) ? when.slice(y.length) : when;
}

export function dropTitle(md: string): string {
  if (!md.startsWith("# ")) return md;
  const i = md.indexOf("\n");
  return i < 0 ? "" : md.slice(i + 1);
}

/**
 * The profile without what the header already shows: its title, a heading that is just the name, the one line, a
 * person's generated tagging line ("importance P0 · relevance to me 95 · …"). Empty means there is nothing more to show.
 */
export function profile(md: string | undefined, oneLine?: string, name?: string): string {
  if (!md) return "";
  const one = oneLine?.trim() ?? "";
  const title = name?.trim() ?? "";
  return dropTitle(md)
    .split("\n")
    .filter((line) => {
      const t = line.trim();
      if (title && /^#{1,6} /.test(t) && t.replace(/^#+ /, "").trim() === title) return false;
      // i18n-ignore: the importance line the hub writes into a profile
      return !(t.startsWith("重要级 ") && t.includes("相关性")) && (!one || t !== one);
    })
    .join("\n")
    .trim();
}

function str(v: unknown): string | undefined {
  return typeof v === "string" ? v : undefined;
}

/** The entry as last loaded: a linked row, a search hit or an address starts with only its id or name. */
export function entryFromDetail(base: KBEntry | undefined, d: KBDetail): KBEntry {
  const e: KBEntry = { ...(base ?? { kind: d.kind, id: d.id, name: d.id, relevance: 0 }) };
  const m = d.meta;
  if (!m) return e;
  const name = str(m.name) || str(m.title);
  if (name) e.name = name;
  if (str(m.importance) !== undefined) e.importance = str(m.importance);
  if (typeof m.relevance === "number") e.relevance = m.relevance;
  if (Array.isArray(m.tags)) e.tags = m.tags.filter((t): t is string => typeof t === "string");
  const line = str(m.one_line) || str(m.note);
  if (line) e.one_line = line;
  if (e.kind === "projects" && str(m.kind)) e.extra = `${str(m.kind)} · ${str(m.status) || "active"}`;
  if (e.kind === "topics" && typeof m.points === "number") e.extra = String(m.points);
  return e;
}

export interface LinkGroup {
  kind: KBKind;
  title: string;
  items: KBEntry[];
}

/** Related entries by kind, in the kinds' order. */
export function linkGroups(links?: KBEntry[] | null): LinkGroup[] {
  return KB_KINDS.map((kind) => ({ kind, title: t(`lib-kb.related_${kind}`), items: (links ?? []).filter((e) => e.kind === kind) })).filter((g) => g.items.length);
}

/** A project's code directories, `<machine>:<path>`. */
export function dirsOf(meta?: Record<string, unknown> | null): { machine: string; path: string }[] {
  const dirs = Array.isArray(meta?.dirs) ? meta.dirs : [];
  return dirs
    .filter((d): d is string => typeof d === "string" && d.trim() !== "")
    .map((d) => {
      const i = d.indexOf(":");
      return i > 0 && /^[/~]/.test(d.slice(i + 1)) ? { machine: d.slice(0, i), path: d.slice(i + 1) } : { machine: "", path: d };
    });
}

export interface Facts {
  at?: string;
  url?: string;
  whenLabel: string;
  openLabel: string;
}

/** A doc's, meeting's or event's when and link; null for the other kinds or when it has neither. */
export function factsOf(kind: string, meta?: Record<string, unknown> | null): Facts | null {
  if (kind !== "docs" && kind !== "meetings" && kind !== "events") return null;
  const at = str(meta?.at);
  const ms = at ? Date.parse(at) : NaN;
  const url = str(meta?.url)?.trim();
  const f: Facts = {
    at: Number.isFinite(ms) && ms > 0 ? at : undefined, // Go's zero time is none
    url: url || undefined,
    whenLabel: kind === "docs" ? t("lib-kb.when_doc") : kind === "meetings" ? t("lib-kb.when_meeting") : t("lib-kb.when_event"),
    openLabel: kind === "docs" ? t("lib-kb.open_doc") : kind === "meetings" ? t("lib-kb.open_meeting") : t("lib-kb.open_event"),
  };
  return f.at || f.url ? f : null;
}

export interface RecordingView {
  startedAt?: string;
  /** "1 h 2 min", "3 min 20 s" (in Chinese) */
  duration?: string;
  device: string;
  language?: string;
  /** Transcription still going (more will come). */
  partial: boolean;
  /** A line per segment and bookmark, at its clock time, in order. */
  lines: { seconds: number; clock: string; text: string; bookmark?: boolean }[];
  /** Persona attends the meeting in this role (#2981); absent when off. */
  attendance?: "listener" | "participant";
  /** The session that attends; absent: the main chat. */
  attendingSession?: { machineId: string; sessionId: string };
  /** When its transcript expired with `recording_days` and only the minutes were kept (`meta.trimmed_at`, #3780); absent
   *  while it is whole. */
  trimmedAt?: string;
}

/** Local wall-clock time shared by minutes and transcript; offsets when the start is unknown. */
export function recordingClock(startedAt: string | undefined, seconds: number): string {
  const start = Date.parse(startedAt ?? "");
  if (!Number.isFinite(start)) return formatSeconds(seconds);
  const d = new Date(start + seconds * 1000);
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

/** Keep fractional seconds in anchors; bookmarks at the same time get their own identity. */
export function transcriptLineId(seconds: number, bookmark = false): string {
  return `recording-${bookmark ? "bookmark" : "line"}-${seconds}`;
}

/** The transcript line containing an offset, or its preceding line in a gap. Never jump to a bookmark. */
export function transcriptTarget(lines: RecordingView["lines"], seconds: number): string | undefined {
  const line = lines.findLast((line) => !line.bookmark && line.seconds <= seconds) ?? lines.find((line) => !line.bookmark);
  return line ? transcriptLineId(line.seconds) : undefined;
}

/** A recording's facts and transcript (meta.json and the detail's segments); null for the other kinds. */
export function recordingView(kind: string, meta?: Record<string, unknown> | null, segments?: KBSegment[] | null): RecordingView | null {
  if (kind !== "recordings") return null;
  const started = str(meta?.started_at);
  const t0 = started ? Date.parse(started) : NaN;
  const clock = (sec: number) => recordingClock(started, sec);
  const at: { sec: number; line: RecordingView["lines"][number] }[] = [];
  for (const s of segments ?? []) {
    const sec = Number(s?.start) || 0;
    if (typeof s?.text === "string" && s.text.trim()) at.push({ sec, line: { seconds: sec, clock: clock(sec), text: s.text.trim() } });
  }
  const marks = Array.isArray(meta?.bookmarks) ? (meta.bookmarks as { at?: unknown; label?: unknown }[]) : [];
  for (const b of marks) {
    const sec = Number(b?.at) || 0;
    at.push({ sec, line: { seconds: sec, clock: clock(sec), text: typeof b?.label === "string" ? b.label.trim() : "", bookmark: true } });
  }
  at.sort((x, y) => x.sec - y.sec); // stable: a bookmark after the segment that starts with it
  const lines = at.map((x) => x.line);
  const secs = Number(meta?.duration);
  const device = str(meta?.device) === "watch" ? t("lib-kb.device_watch") : t("lib-kb.device_phone");
  const role = str(meta?.attendance);
  const attending = meta?.attending_session as { machine_id?: unknown; session_id?: unknown } | null | undefined;
  const machineId = str(attending?.machine_id), sessionId = str(attending?.session_id);
  const trimmed = str(meta?.trimmed_at);
  return {
    startedAt: Number.isFinite(t0) ? started : undefined,
    duration: Number.isFinite(secs) && secs > 0 ? durationLabel(secs) : undefined,
    device,
    language: str(meta?.language) || undefined,
    partial: meta?.complete !== true,
    lines,
    ...(role === "listener" || role === "participant" ? { attendance: role } : {}),
    // Both ids or none: anything else names no session, and the main chat attends.
    ...(machineId && sessionId ? { attendingSession: { machineId, sessionId } } : {}),
    ...(trimmed && Number.isFinite(Date.parse(trimmed)) ? { trimmedAt: trimmed } : {}),
  };
}

/** The day a trimmed recording's minutes go (#3780): `days` (the assistant's `minutes_days` in effect) after it
 *  started, as a date; undefined when either is unknown or days is 0 (forever). */
export function minutesKeptUntil(startedAt: string | undefined, days: unknown): string | undefined {
  const start = Date.parse(startedAt ?? "");
  if (!Number.isFinite(start) || typeof days !== "number" || !Number.isInteger(days) || days <= 0) return undefined;
  return yearMonthDay(new Date(start + days * 86_400_000));
}

/** 3733 → "1 h 2 min"; 200 → "3 min 20 s"; 42 → "42 s" (in Chinese). */
export function durationLabel(secs: number): string {
  const s = Math.round(secs);
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  if (h > 0) return t("lib-kb.duration_hours", { h, m });
  if (m > 0) return t("lib-kb.duration_minutes", { m, s: s % 60 });
  return t("lib-kb.duration_seconds", { s });
}

/** Seconds from the start as "1:02:03" / "2:03", when the start time is unknown. */
function formatSeconds(sec: number): string {
  const s = Math.max(0, Math.floor(sec));
  const h = Math.floor(s / 3600);
  return h > 0 ? `${h}:${pad(Math.floor((s % 3600) / 60))}:${pad(s % 60)}` : `${Math.floor(s / 60)}:${pad(s % 60)}`;
}

const pad = (n: number) => String(n).padStart(2, "0");
const monthDay = (d: Date) => formatDate(d, { month: "short", day: "numeric" });
const yearMonthDay = (d: Date) => formatDate(d, { year: "numeric", month: "short", day: "numeric" });
const weekday = (d: Date) => formatDate(d, { weekday: "short" });

/** "Fri, Oct 30 14:00" (24-hour), with the year instead of the weekday when it is not this one. */
export function formatAt(iso: string, now = new Date()): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const hm = `${pad(d.getHours())}:${pad(d.getMinutes())}`;
  return d.getFullYear() === now.getFullYear()
    ? t("lib-kb.at_this_year", { date: monthDay(d), weekday: weekday(d), time: hm })
    : t("lib-kb.at_other_year", { date: yearMonthDay(d), time: hm });
}

/** A span the hub sends as `start` / `end` (RFC 3339) in this app's dates, as `formatAt` says a time (#4713): "Fri, Oct 30
 *  14:00–15:10", with the end's date too when it is another day. `fallback` (the hub's fixed-format `when`) when either
 *  is missing or unreadable: a hub from before them. */
export function formatSpan(start: string | undefined, end: string | undefined, fallback = "", now = new Date()): string {
  const s = new Date(start ?? "");
  const e = new Date(end ?? "");
  if (!start || !end || Number.isNaN(s.getTime()) || Number.isNaN(e.getTime())) return fallback;
  const sameDay = s.getFullYear() === e.getFullYear() && s.getMonth() === e.getMonth() && s.getDate() === e.getDate();
  return sameDay ? `${formatAt(start, now)}–${pad(e.getHours())}:${pad(e.getMinutes())}` : `${formatAt(start, now)} – ${formatAt(end, now)}`;
}

/** "2026-10-30" → "Fri, Oct 30", with the year when it is not this one. */
export function dayTitle(day: string, now = new Date()): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(day);
  if (!m) return day;
  const d = new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]));
  return t(d.getFullYear() === now.getFullYear() ? "lib-kb.day_this_year" : "lib-kb.day_other_year", { date: d.getFullYear() === now.getFullYear() ? monthDay(d) : yearMonthDay(d), weekday: weekday(d) });
}

export interface RecentDay {
  /** null when the hub sent text without day headings. */
  day: string | null;
  messages: MessageLine[];
}

/** A chat's last 7 days (`recent`: "## 2026-10-30" then "- 10:15 <name>: …" lines, continuations indented) by day. */
export function parseRecent(text?: string): RecentDay[] {
  const out: RecentDay[] = [];
  let raw: string[] = [];
  let cur: RecentDay | null = null;
  const flush = () => {
    if (cur && raw.length) cur.messages.push(parseMessageLine(raw.join("\n")));
    raw = [];
  };
  for (const line of (text ?? "").split("\n")) {
    const h = /^## (\d{4}-\d{2}-\d{2})\s*$/.exec(line);
    if (h) {
      flush();
      cur = { day: h[1], messages: [] };
      out.push(cur);
    } else if (line.startsWith("- ")) {
      flush();
      if (!cur) out.push((cur = { day: null, messages: [] }));
      raw = [line];
    } else if (/^\s/.test(line) && raw.length) {
      raw.push(line);
    } else if (line.trim() && line.trim() !== "…") {
      flush();
      if (!cur) out.push((cur = { day: null, messages: [] }));
      raw = [line];
    }
  }
  flush();
  return out.filter((d) => d.messages.length);
}

// ── Ask Persona ────────────────────────────────────────

/**
 * `<persona-context kind="…" title="…" url="…">text</persona-context>`, attributes in this order: the app's parser
 * (ContextRef) expects all three, so the card shows in the conversation on the phone too.
 */
export function contextXml(c: { kind: string; title: string; url?: string; text: string }): string {
  const esc = (s: string) => s.replace(/&/g, "&amp;").replace(/"/g, "&quot;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
  return `<persona-context kind="${esc(c.kind)}" title="${esc(c.title)}" url="${esc(c.url ?? "")}">\n${contextBody(c.text)}\n</persona-context>`;
}

/** An entry as context: its profile, recent timeline and folder. */
function entryText(e: Pick<KBEntry, "one_line">, d?: Pick<KBDetail, "summary" | "log" | "path"> | null): string {
  let text = d?.summary || e.one_line || "";
  if (d?.log?.length) text += `\n\n${t("lib-kb.context_timeline")}\n${d.log.slice(-10).join("\n")}`;
  if (d?.path) text += `\n\n${t("lib-kb.context_folder", { path: d.path })}`;
  return text.trim();
}

/** What "Ask Persona" sends to the main conversation: the entry's profile, recent timeline and folder, then the ask. */
export function askPersonaText(e: Pick<KBEntry, "name" | "one_line">, d?: Pick<KBDetail, "summary" | "log" | "path"> | null): string {
  return `${contextXml({ kind: "context", title: t("lib-kb.context_title", { name: e.name }), url: "", text: entryText(e, d) })}\n\n${t("lib-kb.ask_persona", { name: e.name })}`;
}

/**
 * What "Start agent" starts a new agent with (KBEntryView.agentPrompt): the entry as context, the ask left to write; and the
 * entry's first code folder on a computer that is online, if it has one.
 */
export function agentPreset(
  kind: string,
  e: Pick<KBEntry, "name" | "one_line">,
  d: Pick<KBDetail, "summary" | "log" | "path" | "meta"> | null | undefined,
  online: string[],
): { prompt: string; machineId?: string; cwd?: string } {
  const prompt = `${contextXml({ kind: "context", title: `${kindTitle(kind)} · ${e.name}`, url: "", text: entryText(e, d) })}\n\n`;
  const dir = dirsOf(d?.meta).find((x) => x.machine && online.includes(x.machine));
  return dir ? { prompt, machineId: dir.machine, cwd: dir.path } : { prompt };
}

// ── Tagging ──────────────────────────────────────────────

export const IMPORTANCE = ["P0", "P1", "P2", "P3"] as const;
export const PROJECT_KINDS = [
  { value: "short", label: t("lib-kb.project_short_detail") },
  { value: "long", label: t("lib-kb.project_long_detail") },
] as const;
export const PROJECT_STATUSES = [
  { value: "active", label: t("lib-kb.status_active") },
  { value: "paused", label: t("lib-kb.status_paused") },
  { value: "done", label: t("lib-kb.status_done") },
  { value: "ignored", label: t("lib-kb.status_ignored") },
] as const;

export interface TagForm {
  importance: string;
  relevance: number;
  tags: string;
  projectKind: "short" | "long";
  status: string;
}

export function tagForm(e: KBEntry): TagForm {
  const p = projectInfo(e);
  return {
    importance: IMPORTANCE.includes(e.importance as (typeof IMPORTANCE)[number]) ? e.importance! : "P2",
    relevance: Math.max(0, Math.min(100, Math.round(e.relevance || 0))),
    tags: (e.tags ?? []).join(" "),
    projectKind: p?.kind ?? "short",
    status: p?.status === "merged" ? "active" : (p?.status ?? "active"),
  };
}

/** The PUT body; a project's kind and status only for projects. */
export function tagUpdate(kind: string, f: TagForm): KBUpdate {
  const u: KBUpdate = { importance: f.importance, relevance: Math.max(0, Math.min(100, Math.round(f.relevance))), tags: parseTags(f.tags) };
  if (kind === "projects") {
    u.status = f.status;
    u.project_kind = f.projectKind;
  }
  return u;
}
