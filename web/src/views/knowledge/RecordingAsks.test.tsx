// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { initI18n } from "../../i18n";
import { setToken } from "../../lib/api";
import { formatSpan, type KBDetailResponse, type RecordingAsk, type RecordingLive } from "../../lib/kb";
import { setRosterDefault } from "../../lib/manager-choice";
import { getState } from "../../lib/store";
import type { AssistantInfo, Session } from "../../lib/types";
import { Icon, type IconName } from "../../ui/icons";
import { EntryDetail } from "./KBDetail";

vi.mock("../../ui/atoms", async (original) => ({ ...await original<typeof import("../../ui/atoms")>(), toast: vi.fn() }));
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
type Request = { url: string; init: RequestInit; resolve: (value: Response) => void };
const id = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa";
const detailPath = `/api/v1/kb/recordings/${id}`;
const livePath = `/api/v1/recordings/${id}/live`;
const agent = "bbbbbbbb-0000-4000-8000-000000000001";
let requests: Request[];
let host: HTMLDivElement;
let root: Root;
const initial = { features: getState().features, sessions: getState().sessions, assistant: getState().assistant };

const fixture = (meta: Record<string, unknown> = {}): KBDetailResponse => ({ detail: {
  kind: "recordings", id, path: `recordings/${id}`,
  meta: { title: "Design review", started_at: new Date(2026, 10, 10, 14).toISOString(), complete: false, kind: "meeting", revision: 2,
    minutes_state: "pending", attendance: "participant", attending_session: { machine_id: "macbook", session_id: agent }, ...meta },
  segments: [{ start: 0, end: 4, text: "Let's keep the cap for this release." }],
} });
const ask = (over: Partial<RecordingAsk> = {}): RecordingAsk => ({
  id: "11111111-1111-4111-8111-111111111111", question: "What did we decide about the export cap last time?", question_source: "typed", at: 75,
  state: "answered", answer: "You kept the 4 MB cap for this release. Bo owns the regression test.", lang: "en", speak: true,
  sources: [{ kind: "recordings", id: "cccccccc-cccc-cccc-cccc-cccccccccccc", name: "Design review", when: "2026-11-03 14:00–15:10" }],
  asked_at: "2026-11-10T06:01:15Z", answered_at: "2026-11-10T06:01:21Z", ...over,
});
const liveFixture = (over: Partial<RecordingLive> = {}): RecordingLive => ({
  next: 13, fed: 13, attendance: "participant", attending_session: { machine_id: "macbook", session_id: agent }, asks: [ask()], ...over,
});
const render = () => act(() => root.render(<EntryDetail kind="recordings" id={id} reloadKey={0} onToggleSidebar={() => {}} onBack={() => {}} onOpen={() => {}} onSaved={() => {}} />));
const reply = async (request: Request, body: unknown, status = 200) => act(async () => {
  request.resolve(new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } }));
});
const pending = (path: string) => requests.filter((r) => r.url === path);
const load = async (detail = fixture()) => { render(); await reply(pending(detailPath).at(-1)!, detail); };
const section = () => host.querySelector<HTMLElement>("[data-recording-asks]");
/** The drawing of an icon, to tell which one the status line shows. */
const iconDrawing = (name: IconName) => {
  const box = document.createElement("div");
  box.innerHTML = renderToStaticMarkup(<Icon name={name} />);
  return box.querySelector("svg")!.innerHTML;
};
const statusIcon = () => section()!.querySelector("[role=status] svg")!.innerHTML;
const factRow = (label: string) => [...host.querySelectorAll("div")].find((row) => row.firstElementChild?.textContent === label && row.children.length === 2);

beforeEach(async () => {
  await initI18n("en");
  localStorage.clear();
  setToken("synthetic-owner-a");
  setRosterDefault("assistant");
  requests = [];
  vi.stubGlobal("fetch", vi.fn((url: string, init: RequestInit) => new Promise<Response>((resolve) => requests.push({ url, init, resolve }))));
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  Object.assign(getState(), initial);
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("an attended meeting in Knowledge detail", () => {
  it("names the role and links the attending Agent by its title", async () => {
    Object.assign(getState(), { sessions: { macbook: [{ machine_id: "macbook", session_id: agent, title: "Export cap review", cwd: "/home/example/app" } as Session] } });
    await load();
    expect(factRow("Persona")?.lastElementChild?.textContent).toBe("Participant");
    const link = factRow("Attending session")!.querySelector("a")!;
    expect(link.textContent).toBe("Export cap review");
    expect(link.getAttribute("href")).toBe(`/s/macbook/${agent}`);
  });

  it.each([
    ["the main chat when one is known", { machine_id: "server", main: "main-1", conversations: [] }, "/persona/main-1"],
    ["plain text without assistant info", null, null],
  ])("names %s", async (_, assistant, path) => {
    Object.assign(getState(), { assistant: assistant as AssistantInfo | null });
    await load(fixture({ attendance: "listener", attending_session: undefined }));
    const row = factRow("Attending session")!;
    expect(factRow("Persona")?.lastElementChild?.textContent).toBe("Listener");
    expect(row.lastElementChild!.textContent).toBe("Main chat");
    expect(row.querySelector("a")?.getAttribute("href") ?? null).toBe(path);
  });

  it("opens a Persona conversation in Persona, and an unknown session names its computer", async () => {
    Object.assign(getState(), { assistant: { machine_id: "server", main: "main-1", conversations: [{ machine_id: "server", session_id: "conv-2", title: "Planning" } as Session] } });
    await load(fixture({ attending_session: { machine_id: "server", session_id: "conv-2" } }));
    expect(factRow("Attending session")!.querySelector("a")!.getAttribute("href")).toBe("/persona/conv-2");
    act(() => root.render(null));
    host.replaceChildren();
    await load(fixture({ attending_session: { machine_id: "studio", session_id: "gone" } }));
    expect(factRow("Attending session")!.textContent).toContain("A session on studio");
  });

  it("reads the live state with the detail's owner and manager and lists the questions", async () => {
    await load();
    const read = pending(livePath)[0];
    expect(new Headers(read.init.headers).get("Authorization")).toBe("Bearer synthetic-owner-a");
    expect(new Headers(read.init.headers).get("X-Persona-Manager")).toBe("assistant");
    expect(read.init.method ?? "GET").toBe("GET");
    await reply(read, liveFixture({ asks: [
      ask(),
      ask({ id: "22222222-2222-4222-8222-222222222222", question: "Who owns the regression test?", question_source: "heard", at: 120, state: "pending", answer: "", sources: [], speak: false }),
      ask({ id: "33333333-3333-4333-8333-333333333333", question: "", question_source: "heard", state: "failed", answer: "", sources: [], error: "nothing_heard" }),
      ask({ id: "44444444-4444-4444-8444-444444444444", question: "And the date?", state: "failed", answer: "", sources: [], error: "future_error", at: undefined }),
    ] }));
    const rows = [...section()!.querySelectorAll("li[data-ask]")];
    expect(section()!.querySelector("h2")!.textContent).toBe("Questions in the meeting");
    expect(section()!.querySelector("h2")!.nextElementSibling!.textContent).toBe("4");
    expect(rows[0].textContent).toContain("What did we decide about the export cap last time?");
    expect(rows[0].textContent).toContain("You kept the 4 MB cap for this release.");
    expect(rows[0].textContent).toContain("14:01:15");
    expect(rows[0].querySelector('[title="Typed on the phone"]')).not.toBeNull();
    expect(rows[0].querySelector('[title="Read aloud on the phone"]')).not.toBeNull();
    expect(rows[0].querySelector("a")!.getAttribute("href")).toBe("/kb/recordings/cccccccc-cccc-cccc-cccc-cccccccccccc");
    expect(rows[0].textContent).toContain("2026-11-03 14:00–15:10");
    expect(rows[1].textContent).toContain("Answering…");
    expect(rows[1].querySelector('[title="Heard in the room"]')).not.toBeNull();
    expect(rows[1].querySelector('[title="Read aloud on the phone"]')).toBeNull();
    expect(rows[2].textContent).toContain("What was just said");
    expect(rows[2].textContent).toContain("Nothing was said just before the tap.");
    expect(rows[3].textContent).toContain("Couldn’t answer this question.");
    expect(section()!.querySelector("[role=status]")!.textContent).toBe("Following the meeting live · 13 lines passed on");
  });

  it.each([
    [{ fed: 9 }, "Catching up · 9 of 13 lines passed on"],
    [{ next: 0, fed: 0 }, "Following the meeting live · waiting for the first lines"],
    [{ problem: "attendee_not_allowed" }, "This session can’t be given other people’s words. Questions still work."],
    [{ problem: "attendee_unavailable", fed: 2 }, "The attending session can’t take messages right now. Lines are kept and passed on once it can."],
    [{ problem: "live_full" }, "This meeting reached the limit for live lines. Questions still work."],
    [{ problem: "something_new" }, "Persona can’t follow this meeting right now."],
  ])("shows the live state %j", async (over, text) => {
    await load();
    await reply(pending(livePath)[0], liveFixture(over));
    expect(section()!.querySelector("[role=status]")!.textContent).toBe(text);
  });

  // Once complete: what the session was given (`fed`), not what the hub holds (`next`), and no problem said in the present.
  it.each([
    [{}, "Attended · 13 lines passed on"],
    [{ next: 1, fed: 1 }, "Attended · 1 line passed on"],
    [{ next: 57, fed: 40 }, "Attended · 40 of 57 lines passed on"],
    [{ problem: "attendee_not_allowed", fed: 0 }, "Attended · 0 of 13 lines passed on"],
    [{ problem: "attendee_unavailable", fed: 2 }, "Attended · 2 of 13 lines passed on"],
    [{ problem: "live_full" }, "Attended · 13 lines passed on"],
    [{ problem: "something_new", fed: 9 }, "Attended · 9 of 13 lines passed on"],
  ])("shows a finished meeting's state %j", async (over, text) => {
    await load(fixture({ complete: true, minutes_state: "ready" }));
    await reply(pending(livePath)[0], liveFixture(over));
    expect(section()!.querySelector("[role=status]")!.textContent).toBe(text);
    expect(statusIcon()).toBe(iconDrawing("mic"));
  });

  it("dates an earlier meeting it drew on in the app's own format when the hub sends start / end (#4713)", async () => {
    await load();
    const start = new Date(2026, 10, 3, 14).toISOString();
    const end = new Date(2026, 10, 3, 15, 10).toISOString();
    await reply(pending(livePath)[0], liveFixture({ asks: [ask({ sources: [{ kind: "recordings", id: "cccccccc-cccc-cccc-cccc-cccccccccccc", name: "Design review",
      when: "2026-11-03 14:00–15:10", start, end }] })] }));
    const link = section()!.querySelector("li[data-ask] a")!;
    expect(link.textContent).toContain(formatSpan(start, end));
    expect(link.textContent).toMatch(/14:00–15:10$/);
    expect(link.textContent).not.toContain("2026-11-03 14:00–15:10");
  });

  it("marks a problem with a warning only while recording", async () => {
    await load();
    await reply(pending(livePath)[0], liveFixture({ problem: "attendee_unavailable" }));
    expect(statusIcon()).toBe(iconDrawing("warning"));
  });

  it("says no questions yet while attending without any", async () => {
    await load();
    await reply(pending(livePath)[0], liveFixture({ asks: [] }));
    expect(section()!.textContent).toContain("No questions yet.");
  });

  it.each([[404, { error: "Not found" }], [404, { error: "No upload", code: "recording_not_found" }], [410, { error: "Deleted", code: "recording_deleted" }]])(
    "stays silent and stops reading after %i", async (status, body) => {
      vi.useFakeTimers();
      vi.spyOn(document, "visibilityState", "get").mockReturnValue("visible");
      await load();
      await reply(pending(livePath)[0], body, status);
      expect(section()).toBeNull();
      act(() => vi.advanceTimersByTime(20000));
      expect(pending(livePath)).toHaveLength(1);
    });

  it("shows a failed first read and keeps the last state on a later failure", async () => {
    vi.useFakeTimers();
    vi.spyOn(document, "visibilityState", "get").mockReturnValue("visible");
    await load();
    await reply(pending(livePath)[0], { error: "Synthetic outage" }, 500);
    expect(section()!.querySelector("[role=alert]")!.textContent).toContain("Synthetic outage");
    act(() => vi.advanceTimersByTime(5000));
    await reply(pending(livePath)[1], liveFixture());
    expect(section()!.querySelector("[role=alert]")).toBeNull();
    act(() => vi.advanceTimersByTime(5000));
    await reply(pending(livePath)[2], { error: "Synthetic outage" }, 500);
    expect(section()!.textContent).toContain("What did we decide about the export cap last time?");
    expect(section()!.querySelector("[role=alert]")).toBeNull();
  });

  it("polls only while recording and visible, and reads a finished meeting once", async () => {
    vi.useFakeTimers();
    const visibility = vi.spyOn(document, "visibilityState", "get").mockReturnValue("visible");
    await load(fixture({ minutes_state: undefined }));
    await reply(pending(livePath)[0], liveFixture());
    act(() => vi.advanceTimersByTime(5000));
    expect(pending(livePath)).toHaveLength(2);
    await reply(pending(livePath)[1], liveFixture());
    visibility.mockReturnValue("hidden");
    act(() => document.dispatchEvent(new Event("visibilitychange")));
    act(() => vi.advanceTimersByTime(15000));
    expect(pending(livePath)).toHaveLength(2);
    visibility.mockReturnValue("visible");
    act(() => document.dispatchEvent(new Event("visibilitychange")));
    expect(pending(livePath)).toHaveLength(3);
    await reply(pending(livePath)[2], liveFixture());

    act(() => root.render(null));
    host.replaceChildren();
    requests = [];
    await load(fixture({ complete: true, minutes_state: undefined }));
    expect(pending(livePath)).toHaveLength(1);
    await reply(pending(livePath)[0], liveFixture());
    expect(section()!.querySelector("[role=status]")!.textContent).toBe("Attended · 13 lines passed on");
    act(() => vi.advanceTimersByTime(20000));
    expect(pending(livePath)).toHaveLength(1);
  });

  it("reads a meeting without a role only on a hub that attends, and shows it only with questions", async () => {
    const plain = fixture({ attendance: undefined, attending_session: undefined, complete: true, minutes_state: undefined });
    await load(plain);
    expect(pending(livePath)).toHaveLength(0);
    expect(factRow("Persona")).toBeUndefined();
    expect(factRow("Attending session")).toBeUndefined();
    act(() => root.render(null));
    host.replaceChildren();

    Object.assign(getState(), { features: ["meeting_attendance"] });
    await load(plain);
    await reply(pending(livePath)[0], { attendance: "off", next: 4, fed: 4, asks: [] });
    expect(section()).toBeNull();
    act(() => root.render(null));
    host.replaceChildren();
    requests = [];

    await load(plain);
    await reply(pending(livePath)[0], { attendance: "off", next: 4, fed: 4, asks: [ask()] });
    expect(section()!.textContent).toContain("You kept the 4 MB cap for this release.");
    expect(section()!.querySelector("[role=status]")).toBeNull();
  });

  it("reads nothing for a note", async () => {
    Object.assign(getState(), { features: ["meeting_attendance"] });
    await load(fixture({ kind: "note", attendance: undefined, attending_session: undefined }));
    expect(pending(livePath)).toHaveLength(0);
  });
});
