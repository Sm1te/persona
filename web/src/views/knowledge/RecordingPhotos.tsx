// A recorded meeting's photos on its Knowledge detail (#3862 phase 3, docs/assistant/meeting-minutes.md "Phase 3: photos"):
// the **Photos** section (each photo's thumbnail, its time, the owner's caption, the room's note, the text the phone
// recognized in it folded, and whether the minutes use it), the photo a minutes item cites, and the images every part
// of the page shares. Only on a hub that lists `meeting_photos`; the list is `GET /api/v1/recordings/{id}/photos`, read
// by useRecordingMeeting. An image is `GET …/photos/{n}`, which an `<img>` can't read itself (it needs the bearer token
// and the manager), so the page reads its bytes and shows them from an object URL, let go once the photo leaves the
// list or the page. Read-only: photos are taken and deleted on the phone.
import { useCallback, useEffect, useId, useMemo, useRef, useState, type CSSProperties } from "react";
import { t } from "../../i18n";
import { apiBlob, enc } from "../../lib/api";
import { recordingClock, type RecordingPhoto, type RecordingView } from "../../lib/kb";
import { Collapse, cx } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { ImageViewer } from "../chat/ImageViewer";
import { Card, SectionTitle, TonePill } from "./KBParts";

type Auth = { token: string; tokenOwner: string; headers: Record<string, string> };

/** How many images are read at once: a meeting keeps up to 50, each up to 1 MiB. */
const PARALLEL = 3;

/** The images of a recording's photos, read when a part of the page first shows one. */
export interface PhotoImages {
  /** Photo `n`'s image as an object URL once read, `false` when it couldn't be, undefined until then. */
  url: (n: number) => string | false | undefined;
  /** Reads photo `n`'s image, once (again after a failure, when the list is read again). */
  want: (n: number) => void;
}

/** A meeting's photos as the detail's parts share them. */
export interface MeetingPhotos {
  /** The hub lists `meeting_photos` and the recording is a meeting: its photos are read. */
  on: boolean;
  /** The last list read; undefined until one succeeds. A photo a list read lacks has been removed. */
  list?: RecordingPhoto[];
  images: PhotoImages;
}

/** The element a minutes item's photo jumps to. */
export function photoAnchor(n: number): string {
  return `recording-photo-${n}`;
}

/** Reads the images of `recording`'s photos for the page: at most PARALLEL at a time, with the detail's credentials and
 *  manager, each object URL let go when its photo leaves `list` (a successful read), the recording changes or the page
 *  goes. A late answer for another recording or credential, or for a photo `list` no longer has, is dropped. */
export function usePhotoImages(recording: string, list: RecordingPhoto[] | undefined, auth: Auth, current: () => boolean): PhotoImages {
  const [got, setGot] = useState<{ of: string; urls: Record<number, string | false> }>({ of: recording, urls: {} });
  const book = useRef<{ of: string; urls: Map<number, string>; started: Set<number>; failed: Set<number>; queue: number[]; running: number; ac: AbortController } | null>(null);
  const request = useRef(auth);
  request.current = auth;

  const fresh = useCallback((of: string) => ({ of, urls: new Map<number, string>(), started: new Set<number>(), failed: new Set<number>(), queue: [] as number[], running: 0, ac: new AbortController() }), []);
  const drop = useCallback(() => {
    const b = book.current;
    if (!b) return;
    b.ac.abort();
    b.urls.forEach((url) => URL.revokeObjectURL(url));
    book.current = null;
  }, []);
  // Another recording starts from nothing; the page going lets every image go.
  useEffect(() => drop, [recording, drop]);

  const pump = useCallback((b: NonNullable<typeof book.current>) => {
    while (b.running < PARALLEL && b.queue.length) {
      const n = b.queue.shift()!;
      b.running++;
      apiBlob(`/recordings/${enc(b.of)}/photos/${n}`, { ...request.current, signal: b.ac.signal })
        .then((blob) => {
          // A photo a list read no longer has left `started` there: its late answer takes nothing (nor a second answer).
          if (book.current !== b || !b.started.has(n) || b.urls.has(n) || !current()) return;
          const url = URL.createObjectURL(blob);
          b.urls.set(n, url);
          setGot((prev) => ({ of: b.of, urls: { ...(prev.of === b.of ? prev.urls : {}), [n]: url } }));
        })
        .catch(() => {
          if (book.current !== b || b.ac.signal.aborted || !b.started.has(n) || !current()) return;
          b.failed.add(n);
          setGot((prev) => ({ of: b.of, urls: { ...(prev.of === b.of ? prev.urls : {}), [n]: false } }));
        })
        .finally(() => {
          b.running--;
          if (book.current === b) pump(b);
        });
    }
  }, [current]);

  const want = useCallback((n: number) => {
    if (book.current?.of !== recording) {
      drop();
      book.current = fresh(recording);
    }
    const b = book.current!;
    if (b.started.has(n)) return;
    b.started.add(n);
    b.queue.push(n);
    pump(b);
  }, [recording, fresh, drop, pump]);

  // A list read: a photo no longer listed lets its image go, and one still being read is dropped when its answer comes;
  // one whose image failed and is still listed is read again.
  useEffect(() => {
    const b = book.current;
    if (!b || !list) return;
    const listed = new Set(list.map((p) => p.n));
    const gone: number[] = [];
    b.urls.forEach((url, n) => {
      if (listed.has(n)) return;
      URL.revokeObjectURL(url);
      b.urls.delete(n);
      gone.push(n);
    });
    const retry = [...b.failed].filter((n) => listed.has(n));
    b.failed.clear();
    for (const n of b.started) if (!listed.has(n)) b.started.delete(n);
    b.queue = [...b.queue.filter((n) => listed.has(n)), ...retry];
    pump(b);
    if (gone.length || retry.length) {
      setGot((prev) => {
        if (prev.of !== b.of) return prev;
        const urls = { ...prev.urls };
        for (const n of [...gone, ...retry]) delete urls[n];
        return { of: b.of, urls };
      });
    }
  }, [list, pump]);

  const urls = got.of === recording ? got.urls : undefined;
  return useMemo(() => ({ url: (n: number) => urls?.[n], want }), [urls, want]);
}

/** A photo's image, asked for when it shows: the photo's own shape until it is in, a quiet mark when it can't be read. */
export function PhotoThumb({ photo, images, className, style }: { photo: RecordingPhoto; images: PhotoImages; className?: string; style?: CSSProperties }) {
  const { want } = images;
  useEffect(() => want(photo.n), [want, photo.n]);
  const url = images.url(photo.n);
  return (
    <span data-photo-thumb={url ? "ready" : url === false ? "failed" : "loading"} style={style} className={cx("relative block shrink-0 overflow-hidden bg-field shadow-hairline", className)}>
      {url ? (
        <img src={url} alt="" decoding="async" className="size-full object-cover" />
      ) : url === false ? (
        <span className="flex size-full items-center justify-center text-ink-3"><Icon name="image" size={16} /></span>
      ) : null}
    </span>
  );
}

/** The photo's time as the minutes and the transcript give theirs: the clock time, or the offset when the start is unknown. */
export function photoClock(rec: Pick<RecordingView, "startedAt">, photo: Pick<RecordingPhoto, "at">): string {
  return recordingClock(rec.startedAt, photo.at);
}

/** **Photos**: every photo the meeting keeps, oldest first; a trimmed recording keeps only those its minutes use. */
export function RecordingPhotos({ rec, photos, highlighted }: { rec: RecordingView; photos: MeetingPhotos; highlighted: string }) {
  const heading = useId();
  const [open, setOpen] = useState<RecordingPhoto | null>(null);
  const list = photos.on ? photos.list ?? [] : [];
  if (!list.length) return null;
  const url = open ? photos.images.url(open.n) : undefined;
  return (
    <section aria-labelledby={heading} data-recording-photos>
      <SectionTitle title={<span id={heading}>{t("knowledge-kbdetail.photos_title")}</span>} count={list.length} />
      {rec.trimmedAt && <p className="mb-2 px-1 text-[12.5px] leading-relaxed text-ink-3">{t("knowledge-kbdetail.photos_trimmed")}</p>}
      <Card>
        <ol className="divide-y divide-line">
          {list.map((p) => <PhotoRow key={p.n} photo={p} rec={rec} images={photos.images} highlighted={highlighted === photoAnchor(p.n)} onOpen={() => setOpen(p)} />)}
        </ol>
      </Card>
      {open && url && (
        <ImageViewer
          images={[{ id: open.id, src: url, mime: open.mime, label: t("knowledge-kbdetail.photo_label", { n: open.n }), name: `photo-${open.n}.${open.mime === "image/png" ? "png" : "jpg"}` }]}
          start={0}
          onClose={() => setOpen(null)}
        />
      )}
    </section>
  );
}

function PhotoRow({ photo: p, rec, images, highlighted, onOpen }: { photo: RecordingPhoto; rec: RecordingView; images: PhotoImages; highlighted: boolean; onOpen: () => void }) {
  const label = t("knowledge-kbdetail.photo_label", { n: p.n });
  const clock = photoClock(rec, p);
  const loaded = !!images.url(p.n);
  return (
    <li id={photoAnchor(p.n)} tabIndex={-1} data-photo={p.n} className={cx("flex items-start gap-3 px-4 py-3 outline-none transition-colors", highlighted && "bg-accent-tint")}>
      <button type="button" disabled={!loaded} onClick={onOpen} aria-label={t("knowledge-kbdetail.photo_open", { n: p.n })}
        className="shrink-0 rounded-control enabled:cursor-zoom-in">
        <PhotoThumb photo={p} images={images} className="size-16 rounded-control" />
      </button>
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
          <span className="text-[13px] font-medium text-ink">{label}</span>
          <span className="text-[11.5px] text-ink-3 tabular-nums">{clock}</span>
          {p.cited && <TonePill tone="accent">{t("knowledge-kbdetail.photo_cited")}</TonePill>}
        </div>
        {p.caption && <p className="mt-1 text-[13px] leading-relaxed break-words text-ink select-text">{p.caption}</p>}
        {p.note && (
          <p className="mt-1 flex gap-1.5 text-[12.5px] leading-relaxed text-ink-2">
            <Icon name="spark" size={12} className="mt-[3px] shrink-0 text-ink-3" />
            <span className="min-w-0 flex-1 break-words select-text"><span className="sr-only">{t("knowledge-kbdetail.photo_note")} </span>{p.note}</span>
          </p>
        )}
        {p.text && <Recognized text={p.text} />}
      </div>
    </li>
  );
}

/** The text the phone recognized in a photo, folded: other people's material, often long. */
function Recognized({ text }: { text: string }) {
  const [open, setOpen] = useState(false);
  const body = useId();
  return (
    <div className="mt-1">
      <button type="button" aria-expanded={open} aria-controls={body} onClick={() => setOpen((x) => !x)}
        className="touch-hit flex min-w-0 items-center gap-1.5 rounded-control py-0.5 text-left text-[12px] text-ink-3 hover:text-ink">
        <Icon name="read" size={12} className="shrink-0" />
        <span className="min-w-0 truncate">{t("knowledge-kbdetail.photo_text")}</span>
        <Icon name="chevron" size={12} stroke={2.2} className={cx("shrink-0 transition-transform duration-200", open && "rotate-180")} />
      </button>
      <Collapse open={open} lazy>
        <pre id={body} className="mt-1 max-h-60 overflow-auto rounded-control bg-field px-3 py-2 font-sans text-[12px] leading-relaxed break-words whitespace-pre-wrap text-ink-2 select-text">{text}</pre>
      </Collapse>
    </div>
  );
}

/** A minutes item's photo, in place of its transcript time: its thumbnail and time, going to it under **Photos**;
 *  "Photo removed" once a list read lacks it; the number and time alone while no list has been read. */
export function PhotoCite({ n, at, rec, photos, onJump }: { n: number; at: number; rec: RecordingView; photos?: MeetingPhotos; onJump: (id: string) => void }) {
  const photo = photos?.on ? photos.list?.find((p) => p.n === n) : undefined;
  const clock = recordingClock(rec.startedAt, photo?.at ?? at);
  if (photo && photos) {
    return (
      <button type="button" aria-label={t("knowledge-kbdetail.minutes_photo_jump", { n, time: clock })} aria-controls={photoAnchor(n)} onClick={() => onJump(photoAnchor(n))}
        data-minutes-photo={n} className="touch-hit inline-flex shrink-0 items-center gap-1.5 whitespace-nowrap rounded-control text-[11.5px] text-accent-ink tabular-nums hover:bg-hover">
        <PhotoThumb photo={photo} images={photos.images} className="size-7 rounded-chip" />
        {clock}
      </button>
    );
  }
  if (photos?.on && photos.list) {
    return (
      <span data-minutes-photo={n} data-removed className="inline-flex shrink-0 items-center gap-1 whitespace-nowrap text-[11.5px] text-ink-3">
        <Icon name="image" size={12} />{t("knowledge-kbdetail.photo_removed")}
      </span>
    );
  }
  return (
    <span data-minutes-photo={n} className="inline-flex shrink-0 items-center gap-1 whitespace-nowrap text-[11.5px] text-ink-3 tabular-nums">
      <Icon name="image" size={12} />{t("knowledge-kbdetail.minutes_photo", { n, time: clock })}
    </span>
  );
}
