// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { initI18n, t } from "../../i18n";
import { handleUnauthorized, setToken } from "../../lib/api";
import { dayTitle, minutesKeptUntil, recordingClock, type KBDetailResponse } from "../../lib/kb";
import { setRosterDefault } from "../../lib/manager-choice";
import { toast } from "../../ui/atoms";
import { EntryDetail } from "./KBDetail";

vi.mock("../../ui/atoms", async (original) => ({ ...await original<typeof import("../../ui/atoms")>(), toast: vi.fn() }));
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
type Request = { url: string; init: RequestInit; resolve: (value: Response) => void };
const id = "dddddddd-dddd-dddd-dddd-dddddddddddd";
const meetingId = "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee";
const detailPath = `/api/v1/kb/recordings/${id}`;
const minutesPath = `/api/v1/recordings/${id}/minutes`;
// 08:00 in Tokyo: the recap and transcript read the recording's own clock, whatever zone the test runs in.
const started = "2026-11-11T08:00:00+09:00";
let requests: Request[];
let host: HTMLDivElement;
let root: Root;
const saved = vi.fn();
const opened = vi.fn();
const scroll = vi.fn();
const clipboard = vi.fn().mockResolvedValue(undefined);
const fixture = (): KBDetailResponse => ({ detail: {
  kind: "recordings", id, path: `recordings/${id}`,
  meta: {
    title: "Day · Nov 11", kind: "day", day: "2026-11-11", tz: "Asia/Tokyo", part: 1, started_at: started, complete: false, device: "watch", revision: 9, base: 1, next: 5,
    recap_state: "ready", coverage: { to: 39600, gaps: [] },
  },
  summary: "Mostly the export release.",
  segments: [
    { start: 3600, end: 3610, text: "Let's start the design review." },
    { start: 3912, end: 3920, text: "The export path drops entries over 4 MB." },
    { start: 16200, end: 16210, text: "Bo will look at the flaky upload test." },
    { start: 16500, end: 16510, text: "" },
    { start: 37000, end: 37010, text: "Wrap up the day." },
  ],
  recap: {
    version: 3, revision: 9, generated_at: "2026-11-11T10:00:41Z", day: "2026-11-11", covers: { from: 0, to: 39600 }, final: false,
    summary: "Mostly the export release.",
    timeline: [
      { from: 16200, to: 16800, kind: "outside", title: "Lunch with the platform team", points: [{ text: "Bo will look at the flaky upload test.", at: 16200, who: "Bo" }] },
      { from: 3600, to: 6900, kind: "meeting", title: "Design review", meeting: { kind: "recordings", id: meetingId, name: "Export design review" }, points: [{ text: "The export path drops entries over 4 MB.", at: 3912, who: "Ada" }] },
      { from: 37000, to: 37600, kind: "meeting", title: "Evening sync", points: [{ text: "Wrap up the day.", at: 37000 }] },
    ],
    actions: [
      { id: "a1", who: "Bo", what: "Look at the flaky upload test.", by: "tomorrow", due_date: "2026-11-12", at: 16200 },
      { id: "a2", what: "Share the review notes.", at: 3912, todo_id: "todo-example" },
    ],
    gaps: [{ from: 8400, to: 9900, reason: "interrupted" }, { from: 12000, to: 12030, reason: "paused" }, { from: 13000, to: 14000, reason: "unheard-of" }],
    partial: false,
    markdown: "# 2026-11-11\n\n## Meetings\n\n- 09:00–09:55 Design review",
  },
} });
const render = (recording = id) => act(() => root.render(<EntryDetail kind="recordings" id={recording} reloadKey={0} onToggleSidebar={() => {}} onBack={() => {}} onOpen={opened} onSaved={saved} />));
const reply = async (request: Request, body: unknown, status = 200) => act(async () => {
  request.resolve(new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } }));
});
const load = async (body = fixture()) => { render(); await reply(requests.at(-1)!, body); };
const section = () => host.querySelector<HTMLElement>("[data-recording-recap]")!;
const button = (text: string) => [...(section()?.querySelectorAll("button") ?? [])].find((b) => b.textContent?.trim() === text)!;
const click = (text: string) => act(() => button(text).click());
const writes = () => requests.filter((r) => r.init.method === "POST");
const headings = (tag: string) => [...section().querySelectorAll(tag)].map((h) => h.textContent);

beforeEach(async () => {
  await initI18n("en");
  localStorage.clear();
  setToken("synthetic-owner-a");
  setRosterDefault("assistant");
  vi.clearAllMocks();
  requests = [];
  vi.stubGlobal("fetch", vi.fn((url: string, init: RequestInit) => new Promise<Response>((resolve) => requests.push({ url, init, resolve }))));
  vi.stubGlobal("navigator", { clipboard: { writeText: clipboard } });
  Object.defineProperty(HTMLElement.prototype, "scrollTo", { value: scroll, configurable: true });
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  delete (HTMLElement.prototype as { scrollTo?: unknown }).scrollTo;
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("an all-day recording's recap in Knowledge detail", () => {
  it("shows the summary, the timeline by part of the day in time order, and the suggested to-dos instead of minutes", async () => {
    await load();
    expect(host.querySelector("[data-recording-minutes]")).toBeNull();
    expect(host.textContent).not.toContain(t("knowledge-kbdetail.minutes_make"));
    expect(headings("h3")).toEqual(["Morning", "Afternoon", "Evening", "Suggested to-dos"]);
    expect(headings("h4")).toEqual(["Design review", "Lunch with the platform team", "Evening sync"]);
    for (const text of ["09:00–09:55", "12:30–12:40", "18:16–18:26", "Ada · The export path drops entries over 4 MB.", "Bo · Look at the flaky upload test. · tomorrow · Due 2026-11-12"]) expect(section().textContent).toContain(text);
    // The summary is the recap's: the profile does not repeat it.
    expect(host.textContent!.split("Mostly the export release.")).toHaveLength(2);
    const added = [...section().querySelectorAll("li")].find((li) => li.textContent?.includes("Share the review notes."))!;
    expect(added.textContent).toContain("Added");
    expect(added.textContent).not.toContain("Add to to-dos");
  });

  it("marks meetings, linking the meeting the knowledge base holds, and leaves the rest outside", async () => {
    await load();
    const entries = [...section().querySelectorAll<HTMLElement>("[data-recap-entry]")];
    expect(entries.map((e) => e.dataset.recapEntry)).toEqual(["meeting", "outside", "meeting"]);
    expect(entries.map((e) => e.textContent?.includes("Meeting"))).toEqual([true, false, true]);
    const link = entries[0].querySelector("a")!;
    expect(link.textContent).toBe("Export design review");
    expect(link.getAttribute("href")).toBe(`/kb/recordings/${meetingId}`);
    act(() => link.click());
    expect(opened).toHaveBeenCalledWith({ kind: "recordings", id: meetingId, name: "Export design review", relevance: 0 });
    expect(entries[2].querySelector("a")).toBeNull();
  });

  it("names the gaps with their reasons, leaving out the short ones", async () => {
    await load();
    expect(section().querySelector("[data-recap-gaps]")!.textContent).toBe("Not recorded: 10:20–10:45 (interrupted) and 11:36–11:53");
    const data = fixture();
    data.detail.recap!.gaps = Array.from({ length: 8 }, (_, i) => ({ from: 3600 * (i + 1), to: 3600 * (i + 1) + 600, reason: "paused" }));
    await act(async () => root.unmount());
    root = createRoot(host);
    await load(data);
    expect(section().querySelector("[data-recap-gaps]")!.textContent).toBe("Not recorded: 09:00–09:10 (paused), 10:00–10:10 (paused), 11:00–11:10 (paused), 12:00–12:10 (paused), 13:00–13:10 (paused), and 3 more");
  });

  it("reads every clock in the recording's zone, and in local time for a zone the browser doesn't know", async () => {
    await load();
    const time = section().querySelector<HTMLButtonElement>('[aria-label="Go to transcript at 09:05:12"]')!;
    expect(time.getAttribute("aria-controls")).toBe("recording-line-3912");
    expect(document.getElementById("recording-line-3912")!.textContent).toContain("09:05:12");
    expect(section().querySelector("[data-recap-covers]")!.textContent).toBe("So far, up to 19:00");

    const data = fixture(); data.detail.meta!.tz = "Not/A_Zone";
    await act(async () => root.unmount());
    root = createRoot(host);
    await load(data);
    const local = recordingClock(started, 3912);
    expect(section().querySelector(`[aria-label="Go to transcript at ${local}"]`)).not.toBeNull();
    expect(document.getElementById("recording-line-3912")!.textContent).toContain(local);
  });

  it("jumps to the spoken transcript line like the minutes", async () => {
    await load();
    vi.useFakeTimers();
    act(() => section().querySelector<HTMLButtonElement>('[aria-label="Go to transcript at 12:30:00"]')!.click());
    const target = document.getElementById("recording-line-16200")!;
    expect(scroll).toHaveBeenCalledOnce();
    expect(document.activeElement).toBe(target);
    expect(target.classList.contains("bg-accent-tint")).toBe(true);
    act(() => vi.advanceTimersByTime(2000));
    expect(target.classList.contains("bg-accent-tint")).toBe(false);
  });

  it("says it is an all-day recording, with its day and part", async () => {
    const data = fixture(); data.detail.meta!.part = 2;
    await load(data);
    expect(host.textContent).toContain(t("knowledge-kbdetail.recording_all_day"));
    expect(host.textContent).toContain(t("knowledge-kbdetail.recording_day_part", { date: dayTitle("2026-11-11"), part: 2 }));
  });

  it("copies the exact stored markdown", async () => {
    await load();
    await act(async () => button("Copy").click());
    expect(clipboard).toHaveBeenCalledWith(fixture().detail.recap!.markdown);
  });

  it("adds one suggested to-do with the recap version through the minutes route, then refreshes", async () => {
    await load();
    act(() => { button("Add to to-dos").click(); button("Add to to-dos").click(); });
    expect(writes()).toHaveLength(1);
    const write = writes()[0];
    expect(write.url).toBe(`${minutesPath}/todos`);
    expect(JSON.parse(write.init.body as string)).toEqual({ version: 3, actions: ["a1"] });
    expect(new Headers(write.init.headers).get("X-Persona-Manager")).toBe("assistant");
    expect(button("Update").disabled).toBe(true);
    const next = fixture(); next.detail.recap!.actions![0].todo_id = "todo-new";
    await reply(write, { todos: [{ id: "todo-new" }], recap: next.detail.recap });
    expect(requests.at(-1)!.url).toBe(detailPath);
    await reply(requests.at(-1)!, next);
    expect(button("Add to to-dos")).toBeUndefined();
    expect(button("Add all")).toBeUndefined();
    expect(saved).toHaveBeenCalledOnce();
  });

  it("adds all the remaining suggested to-dos in their order", async () => {
    const data = fixture(); data.detail.recap!.actions!.push({ id: "a3", what: "Book the retro.", at: 37000 });
    await load(data); click("Add all");
    expect(JSON.parse(writes()[0].init.body as string)).toEqual({ version: 3, actions: ["a1", "a3"] });
  });

  it("refetches a stale recap without resubmitting the chosen to-dos", async () => {
    await load(); click("Add to to-dos");
    await reply(writes()[0], { code: "minutes_stale", error: "Changed" }, 409);
    expect(toast).toHaveBeenCalledWith(t("knowledge-kbdetail.recap_stale"), "error");
    expect(requests.at(-1)!.url).toBe(detailPath);
    const next = fixture(); next.detail.recap!.version = 4; next.detail.recap!.actions![0].what = "Fix the flaky upload test.";
    await reply(requests.at(-1)!, next);
    expect(section().textContent).toContain("Fix the flaky upload test.");
    expect(writes()).toHaveLength(1);
    click("Add to to-dos");
    expect(JSON.parse(writes()[1].init.body as string)).toEqual({ version: 4, actions: ["a1"] });
  });

  it("writes the first recap with a spinner and polls only while it is pending", async () => {
    vi.useFakeTimers();
    vi.spyOn(document, "visibilityState", "get").mockReturnValue("visible");
    const data = fixture(); delete data.detail.recap; data.detail.meta!.recap_state = "pending";
    await load(data);
    await reply(requests.at(-1)!, data);
    expect(section().querySelector("[role=status]")!.textContent).toBe("Writing the recap…");
    expect(section().querySelector("[role=status] [aria-hidden]")).not.toBeNull();
    expect(button("Write now")).toBeUndefined();
    expect(button("Update")).toBeUndefined();
    const before = requests.length;
    act(() => vi.advanceTimersByTime(5000));
    expect(requests).toHaveLength(before + 1);
    await reply(requests.at(-1)!, fixture());
    expect(section().textContent).toContain("Design review");
    act(() => vi.advanceTimersByTime(10000));
    expect(requests).toHaveLength(before + 1);
  });

  it("keeps the older recap while a newer one is written", async () => {
    const data = fixture(); data.detail.meta!.recap_state = "pending";
    await load(data);
    expect(section().textContent).toContain("Updating…");
    expect(section().textContent).toContain("Design review");
    expect(button("Update").disabled).toBe(true);
  });

  it.each([false, true])("retries a failed recap (older one kept: %s) with a bodyless POST", async (retained) => {
    const data = fixture(); data.detail.meta!.recap_state = "failed"; if (!retained) delete data.detail.recap;
    await load(data);
    expect(section().textContent).toContain("Couldn’t write the recap.");
    expect(section().textContent?.includes("Design review")).toBe(retained);
    click("Retry");
    expect(writes()[0].url).toBe(minutesPath);
    expect(writes()[0].init.body).toBeUndefined();
    await reply(writes()[0], { state: "pending" }, 202);
    data.detail.meta!.recap_state = "pending";
    await reply(requests.at(-1)!, data);
    expect(section().textContent).toContain(retained ? "Updating…" : "Writing the recap…");
  });

  it("says when the recap comes before there is one, and can write it now", async () => {
    const data = fixture(); delete data.detail.recap; delete data.detail.meta!.recap_state; delete data.detail.summary;
    await load(data);
    expect(section().textContent).toContain("The recap is written in the evening.");
    click("Write now");
    expect(writes()[0].url).toBe(minutesPath);
    expect(writes()[0].init.body).toBeUndefined();
  });

  it("offers nothing to write without a transcript", async () => {
    const data = fixture(); delete data.detail.recap; delete data.detail.meta!.recap_state; data.detail.segments = [];
    await load(data);
    expect(section().textContent).toContain(t("knowledge-kbdetail.recap_no_text"));
    expect(section().querySelectorAll("button")).toHaveLength(0);
  });

  it.each([
    ["complete", "Final recap of the day"],
    ["cutoff", "Final recap. Some of the day may not have been transcribed."],
  ])("labels a final recap (%s) and offers to regenerate it", async (reason, label) => {
    const data = fixture(); data.detail.recap!.final = true; data.detail.recap!.final_reason = reason; data.detail.meta!.complete = reason === "complete";
    await load(data);
    // The day's recap is final once no other part of the day is still to come.
    expect(section().querySelector("[data-recap-covers]")!.textContent).toBe("Part 1: final");
    await reply(requests.find((r) => r.url === "/api/v1/recordings")!, { recordings: [] });
    expect(section().querySelector("[data-recap-covers]")!.textContent).toBe(label);
    expect(button("Regenerate")).toBeDefined();
    expect(button("Update")).toBeUndefined();
  });

  it("warns when the day was too long to read in full", async () => {
    const data = fixture(); data.detail.recap!.partial = true;
    await load(data);
    expect(section().textContent).toContain(t("knowledge-kbdetail.recap_partial"));
  });

  it("shows a recap with only its summary, and one whose summary was withdrawn", async () => {
    const data = fixture(); const recap = data.detail.recap!;
    delete recap.timeline; recap.actions = []; delete recap.gaps;
    await load(data);
    expect(headings("h3")).toEqual([]);
    expect(section().textContent).toContain("Mostly the export release.");
    expect(section().querySelector("[data-recap-gaps]")).toBeNull();

    const blank = fixture(); blank.detail.recap!.summary = ""; blank.detail.summary = "";
    await act(async () => root.unmount());
    root = createRoot(host);
    await load(blank);
    expect(section().textContent).not.toContain("Mostly the export release.");
    expect(headings("h4")).toEqual(["Design review", "Lunch with the platform team", "Evening sync"]);
  });

  // Past recording_days the recap stays (docs/assistant/day-recap.md "Retention and privacy"); it can't be written again.
  it("keeps a trimmed recording's recap and to-dos, says until when, and offers no rewrite", async () => {
    const data = fixture();
    data.detail.meta!.trimmed_at = "2026-11-25T02:00:00Z"; delete data.detail.segments; delete data.detail.summary;
    await load(data);
    const ask = requests.find((r) => r.url === "/api/v1/assistant")!;
    expect(section().textContent).toContain(t("knowledge-kbdetail.recap_transcript_expired"));
    await reply(ask, { manager: "assistant", recording_days: 14, minutes_days: 365 });
    expect(section().textContent).toContain(t("knowledge-kbdetail.recap_kept_until", { date: minutesKeptUntil(started, 365)! }));
    expect(section().textContent).toContain("Design review");
    expect(button("Update")).toBeUndefined();
    expect(button("Copy")).toBeDefined();
    expect(section().querySelector<HTMLButtonElement>('[aria-label="Go to transcript at 09:05:12"]')!.disabled).toBe(true);
    click("Add to to-dos");
    expect(writes().map((w) => w.url)).toEqual([`${minutesPath}/todos`]);
  });

  it("says the recap is kept until deleted when retention is forever", async () => {
    const data = fixture(); data.detail.meta!.trimmed_at = "2026-11-25T02:00:00Z"; delete data.detail.segments;
    await load(data);
    await reply(requests.find((r) => r.url === "/api/v1/assistant")!, { manager: "assistant", minutes_days: 0 });
    expect(section().textContent).toContain(t("knowledge-kbdetail.recap_kept_forever"));
  });

  it("ignores a late write after the route changed", async () => {
    await load(); click("Update"); const old = writes()[0];
    render("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb");
    const count = requests.length;
    await reply(old, { state: "pending" }, 202);
    expect(requests).toHaveLength(count);
    expect(saved).not.toHaveBeenCalled();
    expect(toast).not.toHaveBeenCalled();
  });
});

// A day that outgrew one recording continues in a next part with its own recap (docs/assistant/day-recap.md "Parts").
describe("a day in two parts", () => {
  const part2 = "ffffffff-ffff-ffff-ffff-ffffffffffff";
  const part2Path = `/api/v1/kb/recordings/${part2}`;
  const listPath = "/api/v1/recordings";
  // Part 2 holds a late morning sentence and an afternoon meeting; its own action reuses the id a1.
  const second = (): KBDetailResponse => ({ detail: {
    kind: "recordings", id: part2, path: `recordings/${part2}`,
    meta: {
      title: "Day · Nov 11", kind: "day", day: "2026-11-11", tz: "Asia/Tokyo", part: 2, started_at: started, complete: false, device: "watch", revision: 4, base: 2, next: 2,
      recap_state: "ready", coverage: { to: 32400, gaps: [] },
    },
    summary: "The beta plan.",
    segments: [{ start: 5400, end: 5410, text: "The beta waits for the upload fix." }, { start: 30000, end: 30010, text: "Ship the beta on Friday." }],
    recap: {
      version: 2, revision: 4, generated_at: "2026-11-11T08:00:41Z", day: "2026-11-11", covers: { from: 0, to: 32400 }, final: false,
      summary: "The beta plan.",
      timeline: [
        { from: 30000, to: 30600, kind: "meeting", title: "Beta go/no-go", points: [{ text: "Ship the beta on Friday.", at: 30000 }] },
        { from: 5400, to: 5700, kind: "outside", title: "Hallway chat about the beta", points: [{ text: "The beta waits for the upload fix.", at: 5400, who: "Cy" }] },
      ],
      actions: [{ id: "a1", who: "Cy", what: "Fix the upload before the beta.", at: 5400 }],
      gaps: [{ from: 8400, to: 9900, reason: "interrupted" }, { from: 31000, to: 31200, reason: "paused" }],
      partial: false,
      markdown: "# 2026-11-11 (part 2)",
    },
  } });
  // The list holds both parts (part 2's start in UTC: the same instant) and recordings that are not this day's.
  const list = { days: 14, deleted: [], recordings: [
    { id, revision: 9, complete: false, started_at: started, kind: "day", day: "2026-11-11", part: 1, next: 5, base: 1 },
    { id: part2, revision: 4, complete: false, started_at: "2026-11-10T23:00:00Z", kind: "day", day: "2026-11-11", part: 2, next: 2, base: 2 },
    { id: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", revision: 1, complete: true, started_at: "2026-11-10T08:00:00+09:00", kind: "day", day: "2026-11-10", next: 1, base: 1 },
    { id: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", revision: 1, complete: true, started_at: "2026-11-11T09:00:00+09:00", kind: "day", day: "2026-11-11", next: 1, base: 1 },
    { id: meetingId, revision: 1, complete: true, started_at: started },
  ] };
  const pendingFor = (url: string) => requests.filter((r) => r.url === url).at(-1)!;
  const loadDay = async (one = fixture(), two = second()) => {
    await load(one);
    await reply(pendingFor(listPath), list);
    await reply(pendingFor(part2Path), two);
  };
  const item = (text: string) => [...section().querySelectorAll("li")].find((li) => li.textContent?.includes(text))!;

  it("lists the other parts of the same day and reads them like the page, then merges their recaps by time", async () => {
    await load();
    const listed = pendingFor(listPath);
    expect(new Headers(listed.init.headers).get("X-Persona-Manager")).toBe("assistant");
    const count = requests.length;
    await reply(listed, list);
    // Only part 2 is read: not another day, a day recording started at another time, or a meeting.
    expect(requests.slice(count).map((r) => r.url)).toEqual([part2Path]);
    expect(new Headers(requests.at(-1)!.init.headers).get("X-Persona-Manager")).toBe("assistant");
    await reply(requests.at(-1)!, second());

    expect(headings("h3")).toEqual(["Morning", "Afternoon", "Evening", "Suggested to-dos"]);
    expect(headings("h4")).toEqual(["Design review", "Hallway chat about the beta", "Lunch with the platform team", "Beta go/no-go", "Evening sync"]);
    expect(section().textContent).toContain("Mostly the export release.");
    expect(section().textContent).toContain("The beta plan.");
    expect(section().querySelector("[data-recap-covers]")!.textContent).toBe("Part 1: so far, up to 19:00 · Part 2: so far, up to 17:00");
    // The gaps both parts name are named once.
    expect(section().querySelector("[data-recap-gaps]")!.textContent).toBe("Not recorded: 10:20–10:45 (interrupted), 11:36–11:53, and 16:36–16:40 (paused)");
    // Suggested to-dos of both parts in time order.
    const todos = [...section().querySelectorAll("h3")].find((h) => h.textContent === "Suggested to-dos")!.closest("div")!.parentElement!;
    expect([...todos.querySelectorAll("ul > li")].map((li) => li.querySelector("p")!.textContent))
      .toEqual(["Share the review notes.", "Cy · Fix the upload before the beta.", "Bo · Look at the flaky upload test. · tomorrow · Due 2026-11-12"]);
  });

  it("cites part 2's times with a link to part 2's page, and this part's with a jump into its transcript", async () => {
    await loadDay();
    const link = item("Hallway chat about the beta").querySelector<HTMLAnchorElement>("a[href]")!;
    expect(link.getAttribute("href")).toBe(`/kb/recordings/${part2}`);
    expect(link.getAttribute("aria-label")).toBe("09:30:00 in part 2: open its transcript");
    expect(link.textContent).toBe("09:30:00· Part 2");
    act(() => link.click());
    expect(opened).toHaveBeenCalledWith(expect.objectContaining({ kind: "recordings", id: part2 }));
    // Part 2's lines are not on this page; this part's still are.
    expect(document.getElementById("recording-line-5400")).toBeNull();
    expect(section().querySelector('[aria-label="Go to transcript at 09:05:12"]')!.getAttribute("aria-controls")).toBe("recording-line-3912");
    expect(item("Fix the upload before the beta.").querySelector("a[href]")!.getAttribute("href")).toBe(`/kb/recordings/${part2}`);
  });

  it("confirms an action of part 2 with part 2's version, then reads both parts back", async () => {
    await loadDay();
    act(() => [...item("Fix the upload before the beta.").querySelectorAll("button")].find((b) => b.textContent === "Add to to-dos")!.click());
    expect(writes()).toHaveLength(1);
    expect(writes()[0].url).toBe(`/api/v1/recordings/${part2}/minutes/todos`);
    expect(JSON.parse(writes()[0].init.body as string)).toEqual({ version: 2, actions: ["a1"] });
    expect(new Headers(writes()[0].init.headers).get("X-Persona-Manager")).toBe("assistant");
    const count = requests.length;
    await reply(writes()[0], { todos: [{ id: "todo-cy" }] });
    expect(requests.slice(count).map((r) => r.url).sort()).toEqual([detailPath, part2Path].sort());
    await reply(pendingFor(detailPath), fixture());
    expect(saved).not.toHaveBeenCalled(); // not read back until part 2 is
    const next = second(); next.detail.recap!.actions![0].todo_id = "todo-cy";
    await reply(pendingFor(part2Path), next);
    expect(saved).toHaveBeenCalledOnce();
    expect(item("Fix the upload before the beta.").textContent).toContain("Added");
    expect(item("Look at the flaky upload test.").textContent).toContain("Add to to-dos");
  });

  it("adds all that are left, part by part, and reads back what was written when a later part fails", async () => {
    await loadDay();
    click("Add all");
    expect(writes().map((w) => [w.url, JSON.parse(w.init.body as string)])).toEqual([[`/api/v1/recordings/${part2}/minutes/todos`, { version: 2, actions: ["a1"] }]]);
    await reply(writes()[0], { todos: [{ id: "todo-cy" }] });
    expect(writes().map((w) => [w.url, JSON.parse(w.init.body as string)]).at(-1)).toEqual([`${minutesPath}/todos`, { version: 3, actions: ["a1"] }]);
    const count = requests.length;
    await reply(writes()[1], { error: "boom" }, 500);
    expect(requests.slice(count).map((r) => r.url).sort()).toEqual([detailPath, part2Path].sort());
    await reply(pendingFor(detailPath), fixture());
    await reply(pendingFor(part2Path), second());
    expect(toast).toHaveBeenCalledWith(expect.stringContaining("Couldn’t update the recap"), "error");
  });

  it("copies every part's recap, in part order", async () => {
    await loadDay();
    await act(async () => button("Copy").click());
    expect(clipboard).toHaveBeenCalledWith(`${fixture().detail.recap!.markdown}\n\n# 2026-11-11 (part 2)`);
  });

  it("calls the day's recap final only when every part's is, and reads the parts again until then", async () => {
    vi.useFakeTimers();
    vi.spyOn(document, "visibilityState", "get").mockReturnValue("visible");
    const one = fixture(); one.detail.recap!.final = true; one.detail.recap!.final_reason = "complete";
    await loadDay(one);
    expect(section().querySelector("[data-recap-covers]")!.textContent).toBe("Part 1: final · Part 2: so far, up to 17:00");
    // This part is final, the day is not: it is read again in a minute, with its parts.
    const count = requests.length;
    act(() => vi.advanceTimersByTime(60_000));
    expect(requests.slice(count).map((r) => r.url).sort()).toEqual([detailPath, listPath].sort());
    await reply(pendingFor(detailPath), one);
    await reply(pendingFor(listPath), list);
    const two = second(); two.detail.recap!.final = true; two.detail.recap!.final_reason = "cutoff";
    await reply(pendingFor(part2Path), two);
    expect(section().querySelector("[data-recap-covers]")!.textContent).toBe("Final recap. Some of the day may not have been transcribed.");
    const settled = requests.length;
    act(() => vi.advanceTimersByTime(180_000));
    expect(requests).toHaveLength(settled);
  });

  it("calls a two-part day complete when both parts are", async () => {
    const one = fixture(); one.detail.recap!.final = true; one.detail.recap!.final_reason = "complete";
    const two = second(); two.detail.recap!.final = true; two.detail.recap!.final_reason = "complete";
    await loadDay(one, two);
    expect(section().querySelector("[data-recap-covers]")!.textContent).toBe("Final recap of the day");
  });

  it("says which part has no recap yet, and shows the other's", async () => {
    const one = fixture(); delete one.detail.recap; one.detail.meta!.recap_state = undefined;
    await loadDay(one);
    expect(section().querySelector("[data-recap-covers]")!.textContent).toBe("Part 1: not written yet · Part 2: so far, up to 17:00");
    expect(section().textContent).not.toContain("The recap is written in the evening.");
    expect(headings("h4")).toEqual(["Hallway chat about the beta", "Beta go/no-go"]);
    expect(button("Write now")).toBeDefined();
  });

  it("shows this part alone when the list can't be read", async () => {
    await load();
    await reply(pendingFor(listPath), { error: "down" }, 503);
    expect(requests.some((r) => r.url === part2Path)).toBe(false);
    expect(headings("h4")).toEqual(["Design review", "Lunch with the platform team", "Evening sync"]);
    expect(section().querySelector("[data-recap-covers]")!.textContent).toBe("So far, up to 19:00");
    expect(section().querySelector("[data-recap-parts-failed]")!.textContent).toBe("Couldn’t load the other parts of the day.·Retry");
  });

  const covers = () => section().querySelector("[data-recap-covers]")!.textContent;
  const partsFailed = () => section().querySelector<HTMLElement>("[data-recap-parts-failed]");
  const finalPart = () => { const one = fixture(); one.detail.recap!.final = true; one.detail.recap!.final_reason = "complete"; return one; };

  it("never calls the day final when its parts couldn't be listed, and lists them again a minute later while the page shows", async () => {
    vi.useFakeTimers();
    let visible: DocumentVisibilityState = "visible";
    vi.spyOn(document, "visibilityState", "get").mockImplementation(() => visible);
    await load(finalPart());
    await reply(pendingFor(listPath), { error: "boom" }, 500);
    expect(covers()).toBe("Part 1: final");
    expect(partsFailed()!.textContent).toBe("Couldn’t load the other parts of the day.·Retry");
    visible = "hidden"; act(() => { document.dispatchEvent(new Event("visibilitychange")); });
    let count = requests.length;
    act(() => vi.advanceTimersByTime(180_000));
    expect(requests).toHaveLength(count);
    visible = "visible"; act(() => { document.dispatchEvent(new Event("visibilitychange")); });
    expect(requests.slice(count).map((r) => r.url).sort()).toEqual([detailPath, listPath].sort());
    await reply(pendingFor(detailPath), finalPart());
    await reply(pendingFor(listPath), { error: "boom" }, 500);
    expect(partsFailed()).not.toBeNull();
    count = requests.length;
    act(() => vi.advanceTimersByTime(60_000));
    expect(requests.slice(count).map((r) => r.url).sort()).toEqual([detailPath, listPath].sort());
    await reply(pendingFor(detailPath), finalPart());
    await reply(pendingFor(listPath), list);
    await reply(pendingFor(part2Path), second());
    expect(partsFailed()).toBeNull();
    expect(covers()).toBe("Part 1: final · Part 2: so far, up to 17:00");
    expect(headings("h4")).toContain("Beta go/no-go");
  });

  it("never calls the day final when a part couldn't be read, and reads it again on Retry", async () => {
    vi.useFakeTimers();
    vi.spyOn(document, "visibilityState", "get").mockReturnValue("visible");
    await load(finalPart());
    await reply(pendingFor(listPath), list);
    await reply(pendingFor(part2Path), { error: "boom" }, 500);
    expect(covers()).toBe("Part 1: final");
    expect(headings("h4")).not.toContain("Beta go/no-go");
    const count = requests.length;
    act(() => partsFailed()!.querySelector("button")!.click());
    expect(requests.slice(count).map((r) => r.url).sort()).toEqual([detailPath, listPath].sort());
    // Asked again before the answer: one read at a time.
    act(() => partsFailed()!.querySelector("button")!.click());
    expect(requests).toHaveLength(count + 2);
    await reply(pendingFor(detailPath), finalPart());
    await reply(pendingFor(listPath), list);
    const two = second(); two.detail.recap!.final = true; two.detail.recap!.final_reason = "complete";
    await reply(pendingFor(part2Path), two);
    expect(partsFailed()).toBeNull();
    expect(covers()).toBe("Final recap of the day");
    expect(headings("h4")).toContain("Beta go/no-go");
    const settledCount = requests.length;
    act(() => vi.advanceTimersByTime(180_000));
    expect(requests).toHaveLength(settledCount);
  });

  it("keeps a part shown when reading it again fails, and says the parts aren't all known", async () => {
    vi.useFakeTimers();
    vi.spyOn(document, "visibilityState", "get").mockReturnValue("visible");
    await loadDay(finalPart());
    act(() => vi.advanceTimersByTime(60_000));
    await reply(pendingFor(detailPath), finalPart());
    await reply(pendingFor(listPath), list);
    await reply(pendingFor(part2Path), { error: "boom" }, 500);
    expect(headings("h4")).toContain("Beta go/no-go");
    expect(covers()).toBe("Part 1: final · Part 2: so far, up to 17:00");
    expect(partsFailed()).not.toBeNull();
  });

  it("reads part 2 once at a time when a minute's read finds this part's recap pending", async () => {
    vi.useFakeTimers();
    vi.spyOn(document, "visibilityState", "get").mockReturnValue("visible");
    await loadDay();
    let count = requests.length;
    act(() => vi.advanceTimersByTime(60_000));
    expect(requests.slice(count).map((r) => r.url).sort()).toEqual([detailPath, listPath].sort());
    await reply(pendingFor(listPath), list);
    // Part 2 is being read when this part's answer says its recap is being written: the 5 s reads take over, after it.
    const pending = fixture(); pending.detail.meta!.recap_state = "pending";
    count = requests.length;
    await reply(pendingFor(detailPath), pending);
    expect(section().textContent).toContain("Updating…");
    expect(requests).toHaveLength(count);
    await reply(pendingFor(part2Path), second());
    act(() => vi.advanceTimersByTime(5_000));
    expect(requests.slice(count).map((r) => r.url).sort()).toEqual([detailPath, part2Path].sort());
  });

  it("starts no other read while part 2's is on its way, even when this part's read failed", async () => {
    vi.useFakeTimers();
    let visible: DocumentVisibilityState = "visible";
    vi.spyOn(document, "visibilityState", "get").mockImplementation(() => visible);
    const showAgain = () => {
      visible = "hidden"; act(() => { document.dispatchEvent(new Event("visibilitychange")); });
      visible = "visible"; act(() => { document.dispatchEvent(new Event("visibilitychange")); });
    };
    await loadDay();
    act(() => vi.advanceTimersByTime(60_000));
    await reply(pendingFor(listPath), list);
    await reply(pendingFor(detailPath), { error: "boom" }, 500);
    const count = requests.length;
    showAgain();
    expect(requests).toHaveLength(count);
    await reply(pendingFor(part2Path), second());
    showAgain();
    expect(requests.slice(count).map((r) => r.url).sort()).toEqual([detailPath, listPath].sort());
  });

  it.each(["route", "credential", "manager", "unmount"])("sends no other part's to-dos once the page is retired (%s) while the first part's were on their way", async (change) => {
    await loadDay();
    click("Add all");
    expect(writes()).toHaveLength(1);
    act(() => {
      if (change === "route") render("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb");
      else if (change === "credential") setToken("synthetic-owner-b");
      else if (change === "manager") setRosterDefault("other-manager");
      else { root.unmount(); root = createRoot(host); }
    });
    await reply(writes()[0], { todos: [{ id: "todo-cy" }] });
    expect(writes()).toHaveLength(1);
    expect(saved).not.toHaveBeenCalled();
    expect(toast).not.toHaveBeenCalled();
  });

  it.each([
    ["list", "route"], ["list", "manager"], ["part", "route"], ["part", "manager"],
  ])("cancels the %s read after a %s change, and its late 401 doesn't sign the new page out", async (read, change) => {
    const unauthorized = vi.fn();
    const before = handleUnauthorized(unauthorized);
    try {
      await load();
      if (read === "part") await reply(pendingFor(listPath), list);
      const late = pendingFor(read === "list" ? listPath : part2Path);
      act(() => {
        if (change === "route") render("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb");
        else setRosterDefault("other-manager");
      });
      expect(late.init.signal?.aborted).toBe(true);
      await reply(late, { error: "Unauthorized" }, 401);
      expect(unauthorized).not.toHaveBeenCalled();
    } finally {
      handleUnauthorized(before);
    }
  });

  it.each(["list", "part"])("signs out on a 401 of the current page's %s read", async (read) => {
    const unauthorized = vi.fn();
    const before = handleUnauthorized(unauthorized);
    try {
      await load();
      if (read === "part") await reply(pendingFor(listPath), list);
      await reply(pendingFor(read === "list" ? listPath : part2Path), { error: "Unauthorized" }, 401);
      expect(unauthorized).toHaveBeenCalledOnce();
    } finally {
      handleUnauthorized(before);
    }
  });
});

// An open day's recap changes without this page asking: new text, the hourly recap, the final one (docs/assistant/day-recap.md).
describe("an open day's recap", () => {
  const listPath = "/api/v1/recordings";
  let visible: DocumentVisibilityState;
  const reads = (from: number) => requests.slice(from).map((r) => r.url).sort();
  // The day's parts are listed with each read: this one is the only one.
  const unlisted = async () => {
    const listed = requests.filter((r) => r.url === listPath).at(-1);
    if (listed) await reply(listed, { recordings: [] });
  };
  const answer = async (detail: KBDetailResponse) => {
    await reply(requests.filter((r) => r.url === detailPath).at(-1)!, detail);
    await unlisted();
  };
  beforeEach(() => {
    vi.useFakeTimers();
    visible = "visible";
    vi.spyOn(document, "visibilityState", "get").mockImplementation(() => visible);
  });

  it("is read again every minute while it shows: no transcript, then a recap, then the final one, and no more", async () => {
    const empty = fixture(); delete empty.detail.recap; delete empty.detail.meta!.recap_state; empty.detail.segments = []; delete empty.detail.summary;
    await load(empty);
    await unlisted();
    expect(section().textContent).toContain(t("knowledge-kbdetail.recap_no_text"));
    let count = requests.length;
    act(() => vi.advanceTimersByTime(59_000));
    expect(requests).toHaveLength(count);
    act(() => vi.advanceTimersByTime(1_000));
    expect(reads(count)).toEqual([detailPath, listPath].sort());
    await answer(fixture());
    expect(section().textContent).toContain("Design review");
    expect(button("Update")).toBeDefined();

    count = requests.length;
    act(() => vi.advanceTimersByTime(60_000));
    expect(reads(count)).toEqual([detailPath, listPath].sort());
    const final = fixture(); final.detail.recap!.final = true; final.detail.recap!.final_reason = "complete"; final.detail.meta!.complete = true;
    await answer(final);
    expect(section().querySelector("[data-recap-covers]")!.textContent).toBe("Final recap of the day");
    count = requests.length;
    act(() => vi.advanceTimersByTime(300_000));
    visible = "hidden"; act(() => { document.dispatchEvent(new Event("visibilitychange")); });
    visible = "visible"; act(() => { document.dispatchEvent(new Event("visibilitychange")); });
    expect(requests).toHaveLength(count);
  });

  it("is read when the page shows again, never while it is hidden", async () => {
    await load();
    await unlisted();
    visible = "hidden"; act(() => { document.dispatchEvent(new Event("visibilitychange")); });
    const count = requests.length;
    act(() => vi.advanceTimersByTime(180_000));
    expect(requests).toHaveLength(count);
    visible = "visible"; act(() => { document.dispatchEvent(new Event("visibilitychange")); });
    expect(reads(count)).toEqual([detailPath, listPath].sort());
  });

  it("is not read again once its transcript expired", async () => {
    const data = fixture(); data.detail.meta!.trimmed_at = "2026-11-25T02:00:00Z"; delete data.detail.segments;
    await load(data);
    await unlisted();
    await reply(requests.find((r) => r.url === "/api/v1/assistant")!, { manager: "assistant", minutes_days: 0 });
    const count = requests.length;
    act(() => vi.advanceTimersByTime(180_000));
    expect(requests).toHaveLength(count);
  });

  it("is not read twice at once, nor while a write is on its way", async () => {
    await load();
    await unlisted();
    let count = requests.length;
    act(() => vi.advanceTimersByTime(120_000)); // the first minute's read is not answered yet
    expect(reads(count)).toEqual([detailPath, listPath].sort());
    await answer(fixture());
    click("Update");
    count = requests.length;
    act(() => vi.advanceTimersByTime(60_000));
    expect(requests).toHaveLength(count);
  });
});

