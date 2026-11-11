// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { initI18n, t } from "../../i18n";
import { setToken } from "../../lib/api";
import { formatAt, formatSpan, minutesKeptUntil, type KBDetailResponse } from "../../lib/kb";
import { setRosterDefault } from "../../lib/manager-choice";
import { toast } from "../../ui/atoms";
import { EntryDetail } from "./KBDetail";

vi.mock("../../ui/atoms", async (original) => ({ ...await original<typeof import("../../ui/atoms")>(), toast: vi.fn() }));
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
type Request = { url: string; init: RequestInit; resolve: (value: Response) => void };
const id = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa";
const detailPath = `/api/v1/kb/recordings/${id}`;
const minutesPath = `/api/v1/recordings/${id}/minutes`;
let requests: Request[];
let host: HTMLDivElement;
let root: Root;
const saved = vi.fn();
const scroll = vi.fn();
const clipboard = vi.fn().mockResolvedValue(undefined);
const fixture = (): KBDetailResponse => ({ detail: {
  kind: "recordings", id, path: `recordings/${id}`,
  meta: { title: "Design review recording", started_at: new Date(2026, 10, 10, 14).toISOString(), complete: true, kind: "meeting", revision: 4, minutes_state: "ready", minutes_asked: true, bookmarks: [{ at: 12, label: "Review" }] },
  summary: "A short profile.",
  segments: [{ start: 0, end: 15, text: "Review the export limit." }, { start: 20.5, end: 50, text: "Keep the cap. Add coverage." }, { start: 60, end: 80, text: "Discuss remaining questions." }],
  minutes: {
    version: 2, revision: 4, generated_at: "2026-11-10T15:00:00Z", title: "Design review", when: "2026-11-10 14:00–14:30", where: "Room 3", attendees: ["Ada", "Bo"],
    points: [{ text: "Export limit", who: "Ada", at: 12 }], decisions: [{ text: "Keep the cap", at: 20.5 }],
    actions: [{ id: "a1", who: "Bo", what: "Add coverage", by: "Friday", due_date: "2026-11-13", at: 25 }, { id: "a2", what: "Share notes", at: 40, todo_id: "todo-example" }],
    open: [{ text: "Per-entry limit?", at: 60 }], markdown: "# Design review\n\n- Add coverage",
  },
} });
const render = (recording = id) => act(() => root.render(<EntryDetail kind="recordings" id={recording} reloadKey={0} onToggleSidebar={() => {}} onBack={() => {}} onOpen={() => {}} onSaved={saved} />));
const reply = async (request: Request, body: unknown, status = 200) => act(async () => {
  request.resolve(new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } }));
});
const load = async (body = fixture()) => { render(); await reply(requests.at(-1)!, body); };
const section = () => host.querySelector<HTMLElement>("[data-recording-minutes]")!;
const button = (text: string) => [...(section()?.querySelectorAll("button") ?? [])].find((b) => b.textContent?.trim() === text)!;
const click = (text: string) => act(() => button(text).click());
const writes = () => requests.filter((r) => r.init.method === "POST");

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

describe("recording minutes in Knowledge detail", () => {
  it("renders the metadata and sections in wire order before the profile", async () => {
    await load();
    expect([...section().querySelectorAll("h3")].map((h) => h.textContent)).toEqual(["Design review", "Key points", "Decisions", "Action items", "Open questions"]);
    for (const text of ["2026-11-10 14:00–14:30", "Location: Room 3", "Attendees: Ada · Bo", "Ada · Export limit", "Bo · Add coverage · Friday · Due 2026-11-13", "Per-entry limit?"]) expect(section().textContent).toContain(text);
    expect(section().textContent).not.toContain("A short profile.");
    expect(host.textContent!.indexOf("Design review")).toBeLessThan(host.textContent!.indexOf("A short profile."));
    const added = [...section().querySelectorAll("li")].find((li) => li.textContent?.includes("Share notes"))!;
    expect(added.textContent).toContain("Added");
    expect(added.textContent).not.toContain("Add to to-dos");
  });

  it("says when in the app's own dates from start / end, and the hub's when only without them (#4713)", async () => {
    const body = fixture();
    const start = new Date(2026, 10, 10, 14).toISOString();
    const end = new Date(2026, 10, 10, 14, 30).toISOString();
    Object.assign(body.detail.minutes!, { start, end });
    await load(body);
    const when = section().querySelector("[data-minutes-when]")!.textContent!;
    expect(when).toBe(formatSpan(start, end));
    expect(when).toContain(formatAt(start));
    expect(when).toMatch(/14:00–14:30$/);
    expect(section().textContent).not.toContain("2026-11-10 14:00–14:30");
  });

  it("targets the spoken transcript line, scrolls only its scroller and briefly highlights it", async () => {
    await load();
    vi.useFakeTimers();
    const time = section().querySelector<HTMLButtonElement>('[aria-label="Go to transcript at 14:00:12"]')!;
    expect(time.getAttribute("aria-controls")).toBe("recording-line-0");
    const target = document.getElementById("recording-line-0")!;
    act(() => time.click());
    expect(scroll).toHaveBeenCalledOnce();
    expect(scroll.mock.contexts[0]).toBe(host.querySelector(".overflow-y-auto"));
    expect(document.activeElement).toBe(target);
    expect(target.classList.contains("bg-accent-tint")).toBe(true);
    expect(document.getElementById("recording-bookmark-12")?.classList.contains("bg-accent-tint")).toBe(false);
    act(() => vi.advanceTimersByTime(2000));
    expect(target.classList.contains("bg-accent-tint")).toBe(false);
    expect(section().querySelector('[aria-label="Go to transcript at 14:00:20"]')?.getAttribute("aria-controls")).toBe("recording-line-20.5");
  });

  it("copies the exact stored markdown", async () => {
    await load();
    await act(async () => button("Copy").click());
    expect(clipboard).toHaveBeenCalledWith(fixture().detail.minutes!.markdown);
  });

  it("adds one action with the minutes version, owner and manager, then refreshes", async () => {
    await load();
    act(() => { button("Add to to-dos").click(); button("Add to to-dos").click(); });
    expect(writes()).toHaveLength(1);
    const write = writes()[0];
    expect(write.url).toBe(`${minutesPath}/todos`);
    expect(JSON.parse(write.init.body as string)).toEqual({ version: 2, actions: ["a1"] });
    expect(new Headers(write.init.headers).get("Authorization")).toBe("Bearer synthetic-owner-a");
    expect(new Headers(write.init.headers).get("X-Persona-Manager")).toBe("assistant");
    expect(button("Regenerate").disabled).toBe(true);
    expect(button("Add all").disabled).toBe(true);
    const next = fixture(); next.detail.minutes!.actions![0].todo_id = "todo-new";
    await reply(write, { todos: [{ id: "todo-new" }], minutes: next.detail.minutes });
    expect(requests.at(-1)!.url).toBe(detailPath);
    expect(button("Add to to-dos").disabled).toBe(true);
    await reply(requests.at(-1)!, next);
    expect(button("Add to to-dos")).toBeUndefined();
    expect(button("Add all")).toBeUndefined();
    expect(saved).toHaveBeenCalledOnce();
  });

  it("adds only the remaining actions in their existing order", async () => {
    const data = fixture(); data.detail.minutes!.actions!.push({ id: "a3", what: "Review tests", at: 50 });
    await load(data); click("Add all");
    expect(JSON.parse(writes()[0].init.body as string)).toEqual({ version: 2, actions: ["a1", "a3"] });
  });

  it("shows the writing hint without minutes", async () => {
    const data = fixture(); delete data.detail.minutes; data.detail.meta!.minutes_state = "pending";
    await load(data);
    expect(section().textContent).toContain("Writing the minutes…");
    expect(button("Regenerate")).toBeUndefined();
    expect(button("Make minutes")).toBeUndefined();
  });

  it("refreshes pending minutes only while visible and stops once ready", async () => {
    vi.useFakeTimers();
    const visibility = vi.spyOn(document, "visibilityState", "get").mockReturnValue("visible");
    const data = fixture(); delete data.detail.minutes; data.detail.meta!.minutes_state = "pending";
    await load(data);
    await reply(requests.at(-1)!, data);
    const before = requests.length;
    act(() => vi.advanceTimersByTime(5000));
    expect(requests).toHaveLength(before + 1);
    await reply(requests.at(-1)!, data);
    visibility.mockReturnValue("hidden");
    act(() => document.dispatchEvent(new Event("visibilitychange")));
    act(() => vi.advanceTimersByTime(10000));
    expect(requests).toHaveLength(before + 1);
    visibility.mockReturnValue("visible");
    act(() => document.dispatchEvent(new Event("visibilitychange")));
    expect(requests).toHaveLength(before + 2);
    await reply(requests.at(-1)!, fixture());
    expect(section().textContent).toContain("Keep the cap");
    act(() => vi.advanceTimersByTime(10000));
    expect(requests).toHaveLength(before + 2);
  });

  it("retains older minutes while pending and shows the partial-transcript note", async () => {
    const data = fixture(); data.detail.meta!.minutes_state = "pending"; data.detail.meta!.revision = 5; data.detail.minutes!.partial = true;
    await load(data);
    expect(section().textContent).toContain("Updating…");
    expect(section().textContent).toContain("Keep the cap");
    expect(section().textContent).toContain("The end of this long transcript was not read");
    expect(button("Regenerate").disabled).toBe(true);
  });

  it.each([false, true])("regenerates failed minutes (retained: %s) with a bodyless POST", async (retained) => {
    const data = fixture(); data.detail.meta!.minutes_state = "failed"; if (!retained) delete data.detail.minutes;
    await load(data);
    expect(section().textContent).toContain("Couldn’t write the minutes");
    if (retained) expect(section().textContent).toContain("Keep the cap");
    click("Regenerate");
    expect(writes()[0].url).toBe(minutesPath);
    expect(writes()[0].init.body).toBeUndefined();
    await reply(writes()[0], { state: "pending" }, 202);
    data.detail.meta!.minutes_state = "pending";
    await reply(requests.at(-1)!, data);
    expect(section().textContent).toContain(retained ? "Updating…" : "Writing the minutes…");
  });

  it("offers Make minutes for a completed ordinary recording", async () => {
    const data = fixture(); delete data.detail.minutes; delete data.detail.meta!.minutes_state; data.detail.meta!.kind = "note";
    await load(data); click("Make minutes");
    expect(writes()[0].url).toBe(minutesPath);
    expect(writes()[0].init.body).toBeUndefined();
  });

  it("hides minutes on an incomplete non-meeting recording", async () => {
    const data = fixture(); data.detail.meta!.kind = "note"; data.detail.meta!.complete = false;
    await load(data); expect(section()).toBeNull();
  });

  it("disables regeneration while a meeting is incomplete", async () => {
    const data = fixture(); data.detail.meta!.complete = false;
    await load(data); expect(button("Regenerate").disabled).toBe(true);
  });

  it("omits optional metadata and empty sections and disables missing transcript targets", async () => {
    const data = fixture(); data.detail.segments = []; delete data.detail.meta!.bookmarks;
    const minutes = data.detail.minutes!;
    delete minutes.where; delete minutes.attendees; delete minutes.points; minutes.decisions = []; delete minutes.open;
    await load(data);
    expect([...section().querySelectorAll("h3")].map((h) => h.textContent)).toEqual(["Design review", "Action items"]);
    expect(section().textContent).not.toContain("Location:");
    expect(section().textContent).not.toContain("Attendees:");
    expect(section().querySelector<HTMLButtonElement>("[aria-label^='Go to transcript']")!.disabled).toBe(true);
  });

  it("refetches a stale version without silently resubmitting selected actions", async () => {
    await load(); click("Add to to-dos");
    await reply(writes()[0], { code: "minutes_stale", error: "Changed" }, 409);
    expect(toast).toHaveBeenCalledWith(expect.stringContaining("The minutes changed"), "error");
    expect(requests.at(-1)!.url).toBe(detailPath);
    const next = fixture(); next.detail.minutes!.version = 3; next.detail.minutes!.actions![0].what = "Review the new plan";
    await reply(requests.at(-1)!, next);
    expect(section().textContent).toContain("Review the new plan");
    expect(writes()).toHaveLength(1);
    click("Add to to-dos");
    expect(JSON.parse(writes()[1].init.body as string)).toEqual({ version: 3, actions: ["a1"] });
  });

  it("reports a rejected generation request without refreshing or erasing minutes", async () => {
    await load(); click("Regenerate");
    await reply(writes()[0], { error: "Transcript incomplete" }, 409);
    expect(toast).toHaveBeenCalledWith(expect.stringContaining("Transcript incomplete"), "error");
    expect(requests).toHaveLength(2);
    expect(section().textContent).toContain("Keep the cap");
    expect(button("Regenerate").disabled).toBe(false);
  });

  it("retries only the read after a successful write and failed refresh", async () => {
    await load(); click("Add to to-dos");
    await reply(writes()[0], { todos: [] });
    await reply(requests.at(-1)!, { error: "Synthetic read failure" }, 503);
    expect(toast).toHaveBeenCalledWith(expect.stringContaining("Synthetic read failure"), "error");
    expect(button("Add to to-dos").disabled).toBe(true);
    click("Retry refresh");
    expect(requests.at(-1)!.url).toBe(detailPath);
    await reply(requests.at(-1)!, fixture());
    expect(writes()).toHaveLength(1);
    expect(button("Retry refresh")).toBeUndefined();
  });

  // #3780: past recording_days only the minutes are kept; they are read and confirmed as before, never made again.
  const trimmed = () => {
    const data = fixture();
    data.detail.meta!.trimmed_at = "2026-11-25T02:00:00Z"; delete data.detail.segments; delete data.detail.summary;
    return data;
  };
  const assistantAsk = () => requests.find((r) => r.url === "/api/v1/assistant")!;

  it("keeps a trimmed recording's minutes and to-dos, says until when, and offers no regenerate", async () => {
    const data = trimmed();
    await load(data);
    expect(new Headers(assistantAsk().init.headers).get("X-Persona-Manager")).toBe("assistant");
    expect(section().textContent).toContain(t("knowledge-kbdetail.minutes_transcript_expired"));
    await reply(assistantAsk(), { manager: "assistant", recording_days: 14, minutes_days: 365 });
    const until = minutesKeptUntil(data.detail.meta!.started_at as string, 365)!;
    expect(until).toContain("2027");
    expect(section().textContent).toContain(t("knowledge-kbdetail.minutes_kept_until", { date: until }));
    expect(section().textContent).toContain("Keep the cap");
    expect(button("Regenerate")).toBeUndefined();
    expect(button("Make minutes")).toBeUndefined();
    expect(button("Copy")).toBeDefined();
    expect(section().querySelector<HTMLButtonElement>('[aria-label="Go to transcript at 14:00:12"]')!.disabled).toBe(true);
    expect(host.textContent).toContain(t("knowledge-kbdetail.transcript_expired"));
    expect(host.textContent).not.toContain(t("knowledge-kbdetail.transcript_empty"));
    click("Add to to-dos");
    expect(writes().map((w) => w.url)).toEqual([`${minutesPath}/todos`]);
  });

  it("says minutes are kept until deleted when retention is forever", async () => {
    await load(trimmed());
    await reply(assistantAsk(), { manager: "assistant", recording_days: 14, minutes_days: 0 });
    expect(section().textContent).toContain(t("knowledge-kbdetail.minutes_kept_forever"));
    expect(section().textContent).not.toContain("1970");
    expect(button("Regenerate")).toBeUndefined();
  });

  it.each([
    ["has no minutes_days", { manager: "assistant" }, 200],
    ["cannot be read", { error: "Synthetic failure" }, 503],
  ])("says only that the transcript expired when the assistant %s", async (_, body, status) => {
    await load(trimmed());
    await reply(assistantAsk(), body, status);
    expect(section().textContent).toContain(t("knowledge-kbdetail.minutes_transcript_expired"));
    expect(toast).not.toHaveBeenCalled();
  });

  it("asks nothing more of a whole recording", async () => {
    await load();
    expect(requests.some((r) => r.url === "/api/v1/assistant")).toBe(false);
    expect(section().querySelector("[data-minutes-kept]")).toBeNull();
  });

  it.each(["route", "credential", "manager"])("ignores late writes after a %s change", async (change) => {
    await load(); click("Regenerate"); const old = writes()[0];
    act(() => {
      if (change === "route") render("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb");
      else if (change === "credential") setToken("synthetic-owner-b");
      else setRosterDefault("other-manager");
    });
    const count = requests.length;
    await reply(old, { state: "pending" }, 202);
    expect(requests).toHaveLength(count);
    expect(saved).not.toHaveBeenCalled();
    expect(toast).not.toHaveBeenCalled();
  });
});
