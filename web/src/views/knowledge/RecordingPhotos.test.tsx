// @vitest-environment jsdom
// A recorded meeting's photos on its detail (#3862 phase 3): only on a hub that lists meeting_photos, the list read after
// the first `…/live` read and again only when the photos' revision moves (acknowledged once a list read succeeds), the
// detail read again after each list read a change brings (until one succeeds), each image read with the detail's
// credentials and let go when its photo goes (a late one dropped), the room's photo entries resolved by id ("Photo
// removed", never the room's copy), and the minutes items that cite a photo.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { initI18n } from "../../i18n";
import { setToken } from "../../lib/api";
import type { KBDetailResponse, RecordingLive, RecordingPhoto } from "../../lib/kb";
import { setRosterDefault } from "../../lib/manager-choice";
import { getState } from "../../lib/store";
import type { Item } from "../../lib/types";
import { EntryDetail } from "./KBDetail";

const hub = vi.hoisted(() => ({ listeners: new Map<string, Set<(msg: Record<string, unknown>) => void>>() }));
vi.mock("../../lib/store", async (original) => ({
  ...await original<typeof import("../../lib/store")>(),
  onHubEvent: (type: string, l: (msg: Record<string, unknown>) => void) => {
    if (!hub.listeners.has(type)) hub.listeners.set(type, new Set());
    hub.listeners.get(type)!.add(l);
    return () => { hub.listeners.get(type)?.delete(l); };
  },
}));
vi.mock("../../ui/atoms", async (original) => ({ ...await original<typeof import("../../ui/atoms")>(), toast: vi.fn() }));
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

type Request = { url: string; init: RequestInit; resolve: (value: Response) => void };
const id = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa";
const room = "dddddddd-0000-4000-8000-000000000001";
const photoA = "33333333-3333-4333-8333-333333333333";
const photoB = "44444444-4444-4444-8444-444444444444";
const detailPath = `/api/v1/kb/recordings/${id}`;
const livePath = `/api/v1/recordings/${id}/live`;
const photosPath = `/api/v1/recordings/${id}/photos`;
const imagePath = (n: number) => `${photosPath}/${n}`;
const itemsPath = `/api/v1/sessions/server/${room}/items?turns=400&fold_work=1&lazy_images=1`;
let requests: Request[];
let host: HTMLDivElement;
let root: Root;
let urls: number;
const created = vi.fn((blob: Blob) => { void blob; return `blob:photo-${++urls}`; });
const revoked = vi.fn();
const scroll = vi.fn();
const initial = { features: getState().features };
const ready = { state: "ready", machine_id: "server", session_id: room };
const started = new Date(2026, 10, 10, 14).toISOString();

const fixture = (meta: Record<string, unknown> = {}, minutes?: KBDetailResponse["detail"]["minutes"]): KBDetailResponse => ({ detail: {
  kind: "recordings", id, path: `recordings/${id}`,
  meta: { title: "Design review", started_at: started, complete: false, kind: "meeting", revision: 2, ...(minutes ? { minutes_state: "ready" } : {}),
    attendance: "participant", updated: new Date(Date.now() - 60_000).toISOString(), room: ready, ...meta },
  segments: [{ start: 0, end: 4, text: "Let's keep the cap for this release." }, { start: 20, end: 25, text: "The budget is on the board." }],
  ...(minutes ? { minutes } : {}),
} });
const photo = (n: number, over: Partial<RecordingPhoto> = {}): RecordingPhoto => ({
  n, id: n === 1 ? photoA : photoB, at: 30 * n, mime: "image/jpeg", width: 1600, height: 1200, cited: false, forwarded: true,
  added_at: "2026-11-10T06:01:00Z", ...over,
});
const live = (over: Partial<RecordingLive> = {}): RecordingLive => ({ next: 3, fed: 3, attendance: "participant", asks: [], room: ready, revs: { room: 2, asks: 1, photos: 2 }, ...over });
let seq = 0;
const item = (over: Partial<Item>): Item => ({ id: `i${++seq}#0`, seq, role: "user", kind: "text", ts: new Date(2026, 10, 10, 14, 5).toISOString(), ...over });
const source = '<persona-context kind="message-source" title="">\n{"kind":"meeting","title":"Design review"}\n</persona-context>\n\n';
const forwarded = (photoID: string, n: number): Item[] => [
  item({ client_msg_id: `photo-${photoID}`, kind: "image", image: { mime: "image/jpeg", lazy: true }, source: { kind: "meeting" } }),
  item({ client_msg_id: `photo-${photoID}`, source: { kind: "meeting" }, text: `${source}Photo ${n} was taken in the meeting at 14:0${n}.\n\n<persona-context kind="context" title="Photo ${n}" url="">\nCaption: Whiteboard\n</persona-context>` }),
];
const roomItems = (): Item[] => [
  item({ client_msg_id: `meeting-${id}`, text: `${source}The meeting is starting.\n\n<persona-context kind="context" title="Design review" url="">\nTitle: Design review\nStarted: 2026-11-10 14:00 CST\nRole: participant\n</persona-context>` }),
  ...forwarded(photoA, 1),
  item({ role: "assistant", kind: "tool_use", tool_name: "mcp__persona__meeting_photo_note", tool_input: { photo: 1, text: "The Q3 budget table." } }),
  ...forwarded(photoB, 2),
  item({ client_msg_id: `photo-withdrawn-${photoB}`, source: { kind: "meeting" }, text: `${source}Photo 2 was withdrawn: do not describe or use it again. Write nothing in reply.` }),
];

const render = () => act(() => root.render(<EntryDetail kind="recordings" id={id} reloadKey={0} onToggleSidebar={() => {}} onBack={() => {}} onOpen={() => {}} onSaved={() => {}} />));
const reply = async (request: Request | undefined, body: unknown, status = 200) => act(async () => {
  expect(request, "the request was made").toBeDefined();
  request!.resolve(new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } }));
});
const image = async (request: Request | undefined) => act(async () => {
  expect(request, "the image was asked for").toBeDefined();
  request!.resolve(new Response(new Uint8Array([0xff, 0xd8, 0xff]), { status: 200, headers: { "Content-Type": "image/jpeg" } }));
});
const pending = (path: string) => requests.filter((r) => r.url === path);
const fire = (msg: Record<string, unknown>) => act(() => { hub.listeners.get(String(msg.type))?.forEach((l) => l(msg)); });
const photosSection = () => host.querySelector<HTMLElement>("[data-recording-photos]");
const conversation = () => host.querySelector<HTMLElement>("[data-meeting-conversation]")!;
const minutesSection = () => host.querySelector<HTMLElement>("[data-recording-minutes]")!;
const kinds = () => [...conversation().querySelectorAll("li[data-thread]")].map((li) => li.getAttribute("data-thread"));
/** The detail, its first `…/live` read and the photo list after it. */
const open = async (photos: RecordingPhoto[], detail = fixture(), liveBody = live()) => {
  render();
  await reply(pending(detailPath).at(-1), detail);
  expect(pending(photosPath), "the list waits for the first read").toHaveLength(0);
  await reply(pending(livePath)[0], liveBody);
  await reply(pending(photosPath)[0], { photos });
};

beforeEach(async () => {
  await initI18n("en");
  localStorage.clear();
  setToken("synthetic-owner-a");
  setRosterDefault("assistant");
  history.replaceState(null, "", `/kb/recordings/${id}`);
  requests = [];
  urls = 0;
  vi.clearAllMocks();
  vi.stubGlobal("fetch", vi.fn((url: string, init: RequestInit) => new Promise<Response>((resolve) => requests.push({ url, init, resolve }))));
  Object.defineProperty(URL, "createObjectURL", { value: created, configurable: true });
  Object.defineProperty(URL, "revokeObjectURL", { value: revoked, configurable: true });
  Object.defineProperty(HTMLElement.prototype, "scrollTo", { value: scroll, configurable: true });
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  Object.assign(getState(), { features: ["meeting_attendance", "meeting_conversation", "meeting_photos"] });
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  Object.assign(getState(), initial);
  delete (URL as { createObjectURL?: unknown }).createObjectURL;
  delete (URL as { revokeObjectURL?: unknown }).revokeObjectURL;
  delete (HTMLElement.prototype as { scrollTo?: unknown }).scrollTo;
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("a recorded meeting's photos", () => {
  it("lists them under Photos with their time, caption, note, folded text and citation, each image read with the detail's credentials", async () => {
    await open([
      photo(1, { caption: "Whiteboard", note: "The Q3 budget table.", text: "Q3 budget\nTravel 4k", cited: true }),
      photo(2, { mime: "image/png", forwarded: false }),
    ]);
    const list = pending(photosPath)[0];
    expect(new Headers(list.init.headers).get("Authorization")).toBe("Bearer synthetic-owner-a");
    expect(new Headers(list.init.headers).get("X-Persona-Manager")).toBe("assistant");
    const s = photosSection()!;
    expect(s.querySelector("h2")!.textContent).toBe("Photos");
    const rows = [...s.querySelectorAll<HTMLElement>("li[data-photo]")];
    expect(rows.map((r) => r.id)).toEqual(["recording-photo-1", "recording-photo-2"]);
    expect(rows[0].textContent).toContain("Photo 1");
    expect(rows[0].textContent).toContain("14:00:30");
    expect(rows[0].textContent).toContain("Whiteboard");
    expect(rows[0].textContent).toContain("Persona’s note: The Q3 budget table.");
    expect(rows[0].textContent).toContain("Used in the minutes");
    expect(rows[1].textContent).not.toContain("Used in the minutes");
    // The recognized text stays folded until asked for.
    const fold = rows[0].querySelector<HTMLButtonElement>("button[aria-expanded]")!;
    expect(fold.textContent).toBe("Text in the photo");
    expect(fold.getAttribute("aria-expanded")).toBe("false");
    expect(rows[0].textContent).not.toContain("Travel 4k");
    await act(async () => fold.click());
    expect(rows[0].textContent).toContain("Travel 4k");
    // Each image: GET …/photos/{n} with the bearer token and the manager, shown from an object URL.
    const first = pending(imagePath(1))[0];
    expect(new Headers(first.init.headers).get("Authorization")).toBe("Bearer synthetic-owner-a");
    expect(new Headers(first.init.headers).get("X-Persona-Manager")).toBe("assistant");
    expect(rows[0].querySelector("img")).toBeNull();
    await image(first);
    expect(rows[0].querySelector("img")!.getAttribute("src")).toBe("blob:photo-1");
    expect(rows[0].querySelector<HTMLButtonElement>('button[aria-label="View photo 1"]')!.disabled).toBe(false);
    expect(rows[1].querySelector<HTMLButtonElement>('button[aria-label="View photo 2"]')!.disabled).toBe(true);
    // Nothing of it in the room's own copies.
    expect(requests.some((r) => r.url.includes("/items/") && r.url.includes("/image"))).toBe(false);
  });

  it("reads the list again only when the photos' revision moves, and acknowledges one only once its list is read", async () => {
    await open([photo(1)]);
    const lists = () => pending(photosPath).length;
    fire({ type: "meeting_live", recording_id: id, changed: ["photos"], revs: { photos: 2 } });
    fire({ type: "meeting_live", recording_id: "bbbbbbbb-0000-4000-8000-000000000000", changed: ["photos"], revs: { photos: 9 } });
    fire({ type: "meeting_live", recording_id: id, manager: "m-3fa21c", changed: ["photos"], revs: { photos: 9 } });
    expect(lists()).toBe(1);
    fire({ type: "meeting_live", recording_id: id.toUpperCase(), changed: ["photos"], revs: { photos: 3 } });
    expect(lists()).toBe(2);
    // `photos` has no field on `…/live`: never `?parts=photos`.
    expect(requests.some((r) => r.url.startsWith(`${livePath}?`))).toBe(false);
    await reply(pending(photosPath)[1], { error: "the knowledge base is busy" }, 503);
    // The list shown stays; the same revision again is read again, since none was read.
    expect(photosSection()!.querySelectorAll("li[data-photo]")).toHaveLength(1);
    fire({ type: "meeting_live", recording_id: id, changed: ["photos"], revs: { photos: 3 } });
    expect(lists()).toBe(3);
    // One asked for while a read runs follows it.
    fire({ type: "meeting_live", recording_id: id, changed: ["photos"], revs: { photos: 4 } });
    expect(lists()).toBe(3);
    await reply(pending(photosPath)[2], { photos: [photo(1)] });
    expect(lists()).toBe(4);
    await reply(pending(photosPath)[3], { photos: [photo(1), photo(2)] });
    expect(photosSection()!.querySelectorAll("li[data-photo]")).toHaveLength(2);
    fire({ type: "meeting_live", recording_id: id, changed: ["photos", "asks"], revs: { photos: 4, asks: 1 } });
    expect(lists()).toBe(4);
  });

  it("reads the detail again after each list read a change of the photos brings, not after the page's first", async () => {
    await open([photo(1)]);
    expect(pending(detailPath)).toHaveLength(1);
    // The list first, then the detail: the hub may have taken a deleted photo's items out of the minutes, or be making
    // them again, whatever the change was (a note too).
    fire({ type: "meeting_live", recording_id: id, changed: ["photos"], revs: { photos: 3 } });
    expect(pending(detailPath)).toHaveLength(1);
    await reply(pending(photosPath)[1], { photos: [photo(1, { note: "A table." })] });
    expect(pending(detailPath)).toHaveLength(2);
    await reply(pending(detailPath)[1], fixture());
    fire({ type: "meeting_live", recording_id: id, changed: ["photos"], revs: { photos: 4 } });
    await reply(pending(photosPath)[2], { photos: [] });
    expect(pending(detailPath)).toHaveLength(3);
    await reply(pending(detailPath)[2], fixture());
    // An event that names nothing newer reads neither.
    fire({ type: "meeting_live", recording_id: id, changed: ["photos"], revs: { photos: 4 } });
    expect(pending(photosPath)).toHaveLength(3);
    expect(pending(detailPath)).toHaveLength(3);
  });

  it("reads the detail again after the list read an event brings before the page's first one", async () => {
    render();
    await reply(pending(detailPath)[0], fixture());
    // The phone deletes a photo after the detail was read, before the first `…/live` answers.
    fire({ type: "meeting_live", recording_id: id, changed: ["photos"], revs: { photos: 3 } });
    await reply(pending(photosPath)[0], { photos: [] });
    expect(pending(detailPath)).toHaveLength(2);
    await reply(pending(livePath)[0], live({ revs: { room: 2, asks: 1, photos: 3 } }));
    expect(pending(photosPath)).toHaveLength(1);
    expect(pending(detailPath)).toHaveLength(2);
  });

  it("reads the detail again after a reconnect only when the photos moved", async () => {
    await open([photo(1)]);
    fire({ type: "hello" });
    await reply(pending(livePath)[1], live());
    await reply(pending(photosPath)[1], { photos: [photo(1)] });
    expect(pending(detailPath)).toHaveLength(1);
    fire({ type: "hello" });
    await reply(pending(livePath)[2], live({ revs: { room: 2, asks: 1, photos: 3 } }));
    await reply(pending(photosPath)[2], { photos: [photo(1, { note: "A table." })] });
    expect(pending(detailPath)).toHaveLength(2);
  });

  it("reads the detail again on the next event, poll or reconnect while that read fails, one at a time", async () => {
    vi.useFakeTimers();
    vi.spyOn(document, "visibilityState", "get").mockReturnValue("visible");
    await open([photo(1)]);
    const same = () => fire({ type: "meeting_live", recording_id: id, changed: ["photos"], revs: { photos: 3 } });
    same();
    await reply(pending(photosPath)[1], { photos: [photo(1), photo(2)] });
    expect(pending(detailPath)).toHaveLength(2);
    same();
    expect(pending(detailPath), "not while one runs").toHaveLength(2);
    await reply(pending(detailPath)[1], { error: "busy" }, 503);
    // An event that names nothing newer.
    same();
    expect(pending(detailPath)).toHaveLength(3);
    await reply(pending(detailPath)[2], { error: "busy" }, 503);
    // A poll whose revision has not moved.
    act(() => vi.advanceTimersByTime(5000));
    await reply(pending(livePath)[1], live({ revs: { room: 2, asks: 1, photos: 3 } }));
    expect(pending(photosPath)).toHaveLength(2);
    expect(pending(detailPath)).toHaveLength(4);
    await reply(pending(detailPath)[3], { error: "busy" }, 503);
    // A reconnect: after its list read.
    fire({ type: "hello" });
    await reply(pending(livePath)[2], live({ revs: { room: 2, asks: 1, photos: 3 } }));
    expect(pending(detailPath)).toHaveLength(4);
    await reply(pending(photosPath)[2], { photos: [photo(1), photo(2)] });
    expect(pending(detailPath)).toHaveLength(5);
    await reply(pending(detailPath)[4], fixture());
    // Once one is read, nothing more.
    same();
    act(() => vi.advanceTimersByTime(5000));
    await reply(pending(livePath)[3], live({ revs: { room: 2, asks: 1, photos: 3 } }));
    expect(pending(detailPath)).toHaveLength(5);
  });

  it("lets no detail read that started before a change of the photos stand for the one after it", async () => {
    await open([photo(1)]);
    fire({ type: "meeting_live", recording_id: id, changed: ["photos"], revs: { photos: 3 } });
    await reply(pending(photosPath)[1], { photos: [photo(1), photo(2)] });
    const early = pending(detailPath)[1];
    fire({ type: "meeting_live", recording_id: id, changed: ["photos"], revs: { photos: 4 } });
    await reply(pending(photosPath)[2], { photos: [photo(2)] });
    expect(pending(detailPath)).toHaveLength(3);
    await reply(pending(detailPath)[2], { error: "busy" }, 503);
    await reply(early, fixture());
    // The earlier read's answer settled nothing: the next event reads the detail again.
    fire({ type: "meeting_live", recording_id: id, changed: ["photos"], revs: { photos: 4 } });
    expect(pending(detailPath)).toHaveLength(4);
  });

  it("reads the list after a reconnect's whole read, and after a poll's only when the revision moved", async () => {
    vi.useFakeTimers();
    vi.spyOn(document, "visibilityState", "get").mockReturnValue("visible");
    await open([photo(1)]);
    act(() => vi.advanceTimersByTime(5000));
    await reply(pending(livePath)[1], live());
    expect(pending(photosPath)).toHaveLength(1);
    act(() => vi.advanceTimersByTime(5000));
    await reply(pending(livePath)[2], live({ revs: { room: 2, asks: 1, photos: 6 } }));
    expect(pending(photosPath)).toHaveLength(2);
    await reply(pending(photosPath)[1], { photos: [photo(1)] });
    // A reconnect may have missed anything: the list is read again after the whole read, whatever its revision says.
    fire({ type: "hello" });
    expect(pending(photosPath)).toHaveLength(2);
    await reply(pending(livePath)[3], live({ revs: { room: 2, asks: 1, photos: 6 } }));
    expect(pending(photosPath)).toHaveLength(3);
    await reply(pending(photosPath)[2], { photos: [photo(1)] });
    act(() => vi.advanceTimersByTime(5000));
    await reply(pending(livePath)[4], live({ revs: { room: 2, asks: 1, photos: 6 } }));
    expect(pending(photosPath)).toHaveLength(3);
  });

  it("shows a photo the room was sent from the list, and Photo removed once the list lacks it, never the room's copy", async () => {
    await open([photo(1, { note: "The Q3 budget table." })]);
    await reply(pending(itemsPath)[0], { items: roomItems() });
    // One entry per photo; the withdrawal and the note's tool call are not shown, and nothing reads as the end.
    expect(kinds()).toEqual(["joined", "photo", "photo"]);
    const [kept, gone] = [...conversation().querySelectorAll<HTMLElement>('li[data-thread="photo"]')];
    expect(kept.textContent).toContain("Photo 1");
    expect(kept.textContent).toContain("The Q3 budget table.");
    expect(gone.textContent).toContain("Photo removed");
    expect(gone.querySelector("img, [data-photo-thumb]")).toBeNull();
    await image(pending(imagePath(1))[0]);
    expect(kept.querySelector("img")!.getAttribute("src")).toBe("blob:photo-1");
    expect(requests.some((r) => r.url.includes("/items/") && r.url.includes("/image"))).toBe(false);
    // Deleted on the phone: the entry says so, and its image is let go.
    fire({ type: "meeting_live", recording_id: id, changed: ["photos"], revs: { photos: 3 } });
    await reply(pending(photosPath)[1], { photos: [] });
    const entries = [...conversation().querySelectorAll<HTMLElement>('li[data-thread="photo"]')];
    expect(entries.map((e) => e.textContent)).toEqual([expect.stringContaining("Photo removed"), expect.stringContaining("Photo removed")]);
    expect(conversation().querySelector("img")).toBeNull();
    expect(revoked).toHaveBeenCalledWith("blob:photo-1");
    expect(photosSection()).toBeNull();
  });

  it("shows an item's cited photo in the minutes in every section, going to it, and Photo removed for one no longer listed", async () => {
    const minutes = {
      version: 1, revision: 2, generated_at: "2026-11-10T07:00:00Z", title: "Design review", when: "2026-11-10 14:00–14:30",
      points: [{ text: "The budget", at: 30, photo: 1 }, { text: "The cap", at: 0 }],
      decisions: [{ text: "Cut travel", at: 60, photo: 2 }],
      actions: [{ id: "a1", what: "Send the budget", at: 30, photo: 1 }],
      open: [{ text: "Who pays?", at: 30, photo: 1 }],
      markdown: "# Design review\n\n- The budget（照片 1）",
    };
    render();
    await reply(pending(detailPath)[0], fixture({ complete: true }, minutes));
    // Until a list is read: the number and the time, neither a thumbnail nor a jump to the transcript.
    const cites = () => [...minutesSection().querySelectorAll<HTMLElement>("[data-minutes-photo]")];
    expect(cites().map((c) => c.textContent)).toEqual(["Photo 1 · 14:00:30", "Photo 2 · 14:01:00", "Photo 1 · 14:00:30", "Photo 1 · 14:00:30"]);
    expect(minutesSection().querySelectorAll("[aria-label^='Go to transcript']")).toHaveLength(1);
    await reply(pending(livePath)[0], live());
    await reply(pending(photosPath)[0], { photos: [photo(1, { cited: true })] });
    expect(cites().map((c) => c.tagName)).toEqual(["BUTTON", "SPAN", "BUTTON", "BUTTON"]);
    expect(cites()[1].textContent).toBe("Photo removed");
    const jump = cites()[0] as HTMLButtonElement;
    expect(jump.getAttribute("aria-label")).toBe("Go to photo 1 at 14:00:30");
    expect(jump.getAttribute("aria-controls")).toBe("recording-photo-1");
    // One image for every place that shows the photo.
    expect(pending(imagePath(1))).toHaveLength(1);
    await image(pending(imagePath(1))[0]);
    expect(jump.querySelector("img")!.getAttribute("src")).toBe("blob:photo-1");
    expect(photosSection()!.querySelector("img")!.getAttribute("src")).toBe("blob:photo-1");
    act(() => jump.click());
    expect(scroll).toHaveBeenCalledOnce();
    const row = document.getElementById("recording-photo-1")!;
    expect(document.activeElement).toBe(row);
    expect(row.classList.contains("bg-accent-tint")).toBe(true);
  });

  it("keeps a trimmed recording's cited photos and says so", async () => {
    const minutes = {
      version: 1, revision: 2, generated_at: "2026-11-10T07:00:00Z", title: "Design review", when: "2026-11-10 14:00–14:30",
      points: [{ text: "The budget", at: 30, photo: 1 }, { text: "Travel", at: 60, photo: 2 }], markdown: "# Design review",
    };
    render();
    await reply(pending(detailPath)[0], fixture({ complete: true, ended_at: started, trimmed_at: new Date().toISOString(), room: { state: "deleted" } }, minutes));
    await reply(pending(livePath)[0], live({ room: { state: "deleted" } }));
    await reply(pending(photosPath)[0], { photos: [photo(1, { cited: true })] });
    expect(photosSection()!.textContent).toContain("Only the photos the minutes use are kept.");
    const cites = [...minutesSection().querySelectorAll<HTMLElement>("[data-minutes-photo]")];
    expect(cites.map((c) => c.textContent)).toEqual(["14:00:30", "Photo removed"]);
  });

  it("reads no photos on a hub without meeting_photos, or of a recording that is not a meeting", async () => {
    Object.assign(getState(), { features: ["meeting_attendance", "meeting_conversation"] });
    render();
    await reply(pending(detailPath)[0], fixture({}, {
      version: 1, revision: 2, generated_at: "2026-11-10T07:00:00Z", title: "Design review", when: "", points: [{ text: "The budget", at: 30, photo: 1 }], markdown: "",
    }));
    await reply(pending(livePath)[0], live());
    await reply(pending(itemsPath)[0], { items: roomItems() });
    expect(requests.some((r) => r.url.startsWith(photosPath))).toBe(false);
    expect(photosSection()).toBeNull();
    // The room's photo entries only say there was a photo: whether it is still there is unknown.
    expect([...conversation().querySelectorAll('li[data-thread="photo"]')].map((li) => li.textContent)).toEqual(["Photo14:05", "Photo14:05"]);
    expect(conversation().querySelector("img")).toBeNull();
    expect(minutesSection().querySelector("[data-minutes-photo]")!.textContent).toBe("Photo 1 · 14:00:30");

    act(() => root.render(null)); host.replaceChildren(); requests = [];
    Object.assign(getState(), { features: ["meeting_attendance", "meeting_conversation", "meeting_photos"] });
    render();
    await reply(pending(detailPath)[0], fixture({ kind: "note", minutes_asked: true, room: undefined, attendance: undefined }));
    await reply(pending(livePath)[0], live({ room: undefined, revs: { asks: 0 } }));
    expect(requests.some((r) => r.url.startsWith(photosPath))).toBe(false);
  });

  it("reads at most three images at once, and one that failed again with the next list", async () => {
    await open([1, 2, 3, 4].map((n) => photo(n, { id: `${n}${photoA.slice(1)}` })));
    expect(requests.filter((r) => r.url.startsWith(`${photosPath}/`)).map((r) => r.url)).toEqual([imagePath(1), imagePath(2), imagePath(3)]);
    await reply(pending(imagePath(1))[0], { error: "busy" }, 503);
    expect(pending(imagePath(4))).toHaveLength(1);
    const row = () => document.getElementById("recording-photo-1")!;
    expect(row().querySelector("[data-photo-thumb]")!.getAttribute("data-photo-thumb")).toBe("failed");
    expect(row().querySelector("img")).toBeNull();
    fire({ type: "meeting_live", recording_id: id, changed: ["photos"], revs: { photos: 3 } });
    await reply(pending(photosPath)[1], { photos: [1, 2, 3, 4].map((n) => photo(n, { id: `${n}${photoA.slice(1)}` })) });
    for (const n of [2, 3, 4]) await image(pending(imagePath(n))[0]);
    expect(pending(imagePath(1))).toHaveLength(2);
    await image(pending(imagePath(1))[1]);
    expect(row().querySelector("img")).not.toBeNull();
  });

  it("drops a late list or image after a credential or manager change, and lets every image go with the page", async () => {
    await open([photo(1), photo(2)]);
    await image(pending(imagePath(1))[0]);
    const late = pending(imagePath(2))[0];
    act(() => setToken("synthetic-owner-b"));
    expect(revoked).toHaveBeenCalledWith("blob:photo-1");
    await image(late);
    expect(created).toHaveBeenCalledTimes(1);
    expect(photosSection()).toBeNull();

    act(() => root.render(null)); host.replaceChildren(); requests = [];
    render();
    await reply(pending(detailPath)[0], fixture());
    await reply(pending(livePath)[0], live());
    const list = pending(photosPath)[0];
    act(() => setRosterDefault("other-manager"));
    await reply(list, { photos: [photo(1)] });
    expect(requests.some((r) => r.url === imagePath(1))).toBe(false);
  });

  it("takes nothing of an image that comes after a list read lacking its photo, and keeps one still listed", async () => {
    await open([photo(1), photo(2)]);
    const [first, second] = [pending(imagePath(1))[0], pending(imagePath(2))[0]];
    // Deleted on the phone while its image is still on its way.
    fire({ type: "meeting_live", recording_id: id, changed: ["photos"], revs: { photos: 3 } });
    await reply(pending(photosPath)[1], { photos: [photo(2)] });
    await image(first);
    expect(created, "a photo no longer listed gets no object URL").not.toHaveBeenCalled();
    expect(document.getElementById("recording-photo-1")).toBeNull();
    await image(second);
    expect(created).toHaveBeenCalledOnce();
    expect(document.getElementById("recording-photo-2")!.querySelector("img")!.getAttribute("src")).toBe("blob:photo-1");
    expect(pending(imagePath(1))).toHaveLength(1);
  });
});

// A newer photos revision heard while the page's first photo list is being read (a poll or a reconnect) is a change:
// the detail is read again after the list read that follows it.
describe("a change of the photos during the first list read", () => {
  it("reloads detail when a poll observes a newer revision during the first photo-list read", async () => {
    vi.useFakeTimers();
    vi.spyOn(document, "visibilityState", "get").mockReturnValue("visible");
    render();
    await reply(pending(detailPath)[0], fixture());
    await reply(pending(livePath)[0], live());
    expect(pending(photosPath)).toHaveLength(1);
    // First list snapshot is still in flight. The photo is deleted on the phone and the WebSocket event is missed.
    act(() => vi.advanceTimersByTime(5000));
    await reply(pending(livePath)[1], live({ revs: { room: 2, asks: 1, photos: 3 } }));
    await reply(pending(photosPath)[0], { photos: [photo(1)] });
    expect(pending(photosPath)).toHaveLength(2);
    await reply(pending(photosPath)[1], { photos: [] });
    expect(pending(detailPath), "the newer revision makes the detail read again").toHaveLength(2);
  });

  it("reloads detail when reconnect observes a newer revision during the first photo-list read", async () => {
    render();
    await reply(pending(detailPath)[0], fixture());
    await reply(pending(livePath)[0], live());
    fire({ type: "hello" });
    await reply(pending(livePath)[1], live({ revs: { room: 2, asks: 1, photos: 3 } }));
    await reply(pending(photosPath)[0], { photos: [photo(1)] });
    await reply(pending(photosPath)[1], { photos: [] });
    expect(pending(detailPath)).toHaveLength(2);
  });

  it("does not reload for an unchanged poll during the first photo-list read", async () => {
    vi.useFakeTimers();
    vi.spyOn(document, "visibilityState", "get").mockReturnValue("visible");
    render();
    await reply(pending(detailPath)[0], fixture());
    await reply(pending(livePath)[0], live());
    act(() => vi.advanceTimersByTime(5000));
    await reply(pending(livePath)[1], live());
    await reply(pending(photosPath)[0], { photos: [photo(1)] });
    await reply(pending(photosPath)[1], { photos: [photo(1)] });
    expect(pending(detailPath)).toHaveLength(1);
  });
});

it("never leaves a deleted photo's minutes item after a reconnect during the first list read", async () => {
  const minutes = {
    version: 1, revision: 2, generated_at: "2026-11-10T07:00:00Z", title: "Design review", when: "",
    points: [{ text: "Private board content", at: 30, photo: 1 }], markdown: "Private board content",
  };
  render();
  await reply(pending(detailPath)[0], fixture({ complete: true, room: { state: "ended" } }, minutes));
  await reply(pending(livePath)[0], live({ room: { state: "ended" } }));
  // A snapshot with P1 exists already, but the list response has not reached the browser yet.
  // P1 is deleted while disconnected; the hub's minutes are synchronously redacted.
  fire({ type: "hello" });
  await reply(pending(livePath)[1], live({ room: { state: "ended" }, revs: { room: 2, asks: 1, photos: 3 } }));
  await reply(pending(photosPath)[0], { photos: [photo(1, { cited: true })] });
  await reply(pending(photosPath)[1], { photos: [] });
  if (pending(detailPath)[1]) {
    await reply(pending(detailPath)[1], fixture({ complete: true, room: { state: "ended" } }, { ...minutes, version: 2, points: [], markdown: "" }));
  }
  // Even another reconnect now sees rev3 acknowledged; it cannot recover this lost invalidation.
  fire({ type: "hello" });
  await reply(pending(livePath)[2], live({ room: { state: "ended" }, revs: { room: 2, asks: 1, photos: 3 } }));
  await reply(pending(photosPath)[2], { photos: [] });
  expect(minutesSection().textContent).not.toContain("Private board content");
});
