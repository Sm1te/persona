// @vitest-environment jsdom
// A localhost link clicked on an agent's page (docs/web/preview.md): a port of the agent's computer, shown in the
// preview panel, also when it names that computer by its host name; but this hub's own pages are hub pages, even
// when this page is opened at localhost (a hub on the agent's computer, iOS #2696).
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { openLink, opensInApp, previewTarget } from "./links";
import { closePanel, getPanel } from "./panel";
import { getState } from "./store";
import type { Machine, Session } from "./types";

describe("a localhost link on an agent's page", () => {
  beforeEach(() => {
    Object.assign(getState(), {
      features: ["preview"],
      sessions: { m1: [{ machine_id: "m1", session_id: "s1", cwd: "/w/site" } as Session] },
      machines: [{ id: "m1", name: "Studio", hostname: "studio.local", online: true } as Machine],
    });
    history.replaceState(null, "", "/s/m1/s1");
    closePanel();
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    Object.assign(getState(), { features: [], sessions: {}, machines: [] });
    history.replaceState(null, "", "/");
  });

  it("is that port of the agent's computer, by localhost or by the computer's name", () => {
    expect(previewTarget("http://localhost:5173/x")).toEqual({ mid: "m1", sid: "s1", cwd: "/w/site", port: 5173, path: "/x" });
    expect(previewTarget("http://studio:8000/docs")).toEqual({ mid: "m1", sid: "s1", cwd: "/w/site", port: 8000, path: "/docs" });
    expect(previewTarget("http://elsewhere.local:8000/")).toBeNull();
  });

  it.each([
    ["http://localhost:80/app?x=1#top", 80, "/app?x=1#top"],
    ["https://studio.local:443/docs", 443, "/docs"],
  ] as const)("opens the explicit default port in the existing preview for %s", (url, port, path) => {
    openLink(url);
    expect(getPanel()).toMatchObject({ kind: "preview", mid: "m1", sid: "s1", cwd: "/w/site", port, path });
  });

  it("keeps session, folder and capability requirements for explicit default ports", () => {
    history.replaceState(null, "", "/");
    expect(previewTarget("http://localhost:80/app")).toBeNull();
    history.replaceState(null, "", "/s/m1/missing");
    expect(previewTarget("http://localhost:80/app")).toBeNull();
    history.replaceState(null, "", "/s/m1/s1");
    Object.assign(getState(), { sessions: { m1: [{ machine_id: "m1", session_id: "s1" } as Session] } });
    expect(previewTarget("https://localhost:443/app")).toBeNull();
    Object.assign(getState(), { features: [], sessions: { m1: [{ machine_id: "m1", session_id: "s1", cwd: "/w/site" } as Session] } });
    expect(previewTarget("http://localhost:80/app")).toBeNull();
  });

  it("is not one of this hub's own pages: an agent's chat at this page's origin opens as its page (#5096)", () => {
    const own = `${location.origin}/s/m1/s9`;
    expect(new URL(own).hostname).toBe("localhost"); // jsdom serves the page at localhost, as a hub on that computer would
    expect(previewTarget(own)).toBeNull();
    expect(opensInApp(own)).toBe(true);
    openLink(own);
    expect(getPanel()).toBeNull();
    expect(location.pathname).toBe("/s/m1/s9");
  });

  it("keeps the hub's own pages ahead of an explicit default-port preview", () => {
    vi.stubGlobal("location", { origin: "http://localhost", pathname: "/s/m1/s1", search: "" });
    const push = vi.spyOn(history, "pushState");
    const own = "http://localhost:80/s/m1/s9";
    expect(previewTarget(own)).toBeNull();
    expect(opensInApp(own)).toBe(true);
    openLink(own);
    expect(getPanel()).toBeNull();
    expect(push.mock.calls.at(-1)?.[2]).toBe("/s/m1/s9");
    push.mockRestore();
  });

  it("is nothing on a hub without previews", () => {
    Object.assign(getState(), { features: ["sessions_delta"] });
    expect(previewTarget("http://localhost:5173/")).toBeNull();
  });
});
