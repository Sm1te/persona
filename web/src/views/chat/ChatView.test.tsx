import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Outgoing } from "./Composer";
import type { Item, Session } from "../../lib/types";
import { getFeedState, setRoute } from "../../lib/feed";
import { toast } from "../../ui/atoms";
import { t } from "../../i18n";
import { ChatView, ComposerFooter, footerRows } from "./ChatView";

const captured = vi.hoisted(() => ({
  onSend: undefined as ((out: Outgoing) => Promise<boolean>) | undefined,
  session: { machine_id: "hub", session_id: "c1", title: "Conversation", cwd: "/code", status: "idle" } as Session,
  items: [] as Item[],
  error: "",
  loading: false,
  /** Set: the hub could not say what this chat is (useRuntimeSession's notice, no row). */
  noDetails: "",
  /** The manager a link tapped in the chat opens what it opens as (useLinkManager under ChatView). */
  linkManager: undefined as string | undefined,
}));

vi.mock("./Composer", async (importOriginal) => {
  const { useLinkManager } = await import("../../lib/manager-choice");
  return {
    ...await importOriginal<typeof import("./Composer")>(),
    Composer: ({ onSend }: { onSend: (out: Outgoing) => Promise<boolean> }) => {
      captured.onSend = onSend;
      captured.linkManager = useLinkManager();
      return <div data-testid="composer" />;
    },
  };
});
vi.mock("./useTranscript", () => ({
  useTranscript: () => ({ items: captured.items, loading: captured.loading, error: captured.error, hasMore: false, streaming: "" }),
}));
vi.mock("../../lib/store", async (importOriginal) => {
  const store = await importOriginal<typeof import("../../lib/store")>();
  return {
    ...store,
    getSession: () => captured.session,
    useStore: <T,>(select: (s: ReturnType<typeof store.getState>) => T) => select({ ...store.getState(), sessions: { hub: [captured.session] } }),
  };
});
vi.mock("../../lib/runtime-session", async (importOriginal) => {
  const real = await importOriginal<typeof import("../../lib/runtime-session")>();
  return {
    ...real,
    useRuntimeSession: (...args: Parameters<typeof real.useRuntimeSession>) => {
      const found = real.useRuntimeSession(...args);
      return captured.noDetails ? { session: undefined, detail: { loading: false, error: captured.noDetails, missing: false, legacy: true }, retry: () => {} } : found;
    },
  };
});
vi.mock("../../lib/panel", async (importOriginal) => ({
  ...await importOriginal<typeof import("../../lib/panel")>(),
  usePanel: () => null,
}));
vi.mock("../../ui/atoms", async (importOriginal) => ({
  ...await importOriginal<typeof import("../../ui/atoms")>(),
  toast: vi.fn(),
}));
vi.mock("../../lib/router", async (importOriginal) => ({
  ...await importOriginal<typeof import("../../lib/router")>(),
  useRoute: () => ({ name: "session", mid: "hub", id: "c1" }),
}));
vi.mock("../assistant/Persona", () => ({ usePersona: () => ({ name: "Persona" }) }));
vi.mock("../assistant/AssistantChat", () => ({
  AssistantFace: () => null,
  AssistantLauncherChip: () => null,
  CompactionStrip: () => null,
  useAssistantMenu: () => ({ items: [], dialogs: null }),
}));
vi.mock("../assistant/Tasks", () => ({
  DayLine: () => null,
  FeedLineView: () => null,
  QuickResultCard: () => null,
  RouteChip: () => null,
  AssistantActionBar: () => null,
  TaskDock: () => null,
  TaskQuestions: () => null,
}));

function sender(assistant = true) {
  renderToStaticMarkup(<ChatView mid="hub" sid="c1" assistant={assistant} />);
  expect(captured.onSend).toBeTypeOf("function");
  return captured.onSend!;
}

const image = { mime: "image/png", data: "aW1hZ2U=" };
let fetcher: ReturnType<typeof vi.fn>;
beforeEach(() => {
  setRoute("c1", null);
  captured.onSend = undefined;
  captured.items = [];
  captured.error = "";
  captured.loading = false;
  delete captured.session.source_kind;
  delete captured.session.capabilities;
  vi.mocked(toast).mockClear();
  vi.stubGlobal("localStorage", { getItem: () => "", setItem: vi.fn(), removeItem: vi.fn() });
  fetcher = vi.fn().mockResolvedValue({ ok: true, status: 200, text: async () => "{}" });
  vi.stubGlobal("fetch", fetcher);
});
afterEach(() => {
  setRoute("c1", null);
  vi.unstubAllGlobals();
});

describe("ChatView composer task routing", () => {
  it.each(["Look at this", ""])("rejects a routed image with text %j without delivering to Persona or dropping the route", async (text) => {
    setRoute("c1", "task1");
    const out = { text, images: [image] };
    expect(await sender()(out)).toBe(false);
    expect(fetcher).not.toHaveBeenCalled();
    expect(getFeedState().routes.c1).toBe("task1");
    expect(out).toEqual({ text, images: [image] });
    expect(toast).toHaveBeenCalledWith(t("chat-chatview.route_no_images"), "error");
  });

  it("sends a text-only message to the selected task", async () => {
    setRoute("c1", "task1");
    expect(await sender()({ text: "Continue", images: [] })).toBe(true);
    expect(fetcher).toHaveBeenCalledTimes(1);
    const [url, init] = fetcher.mock.calls[0];
    expect(url).toBe("/api/v1/assistant/conversations/c1/route");
    expect(JSON.parse(init.body)).toEqual({ todo_id: "task1", text: "Continue" });
  });

  it.each([true, false])("still delivers an unrouted image to its conversation (assistant=%j)", async (assistant) => {
    expect(await sender(assistant)({ text: "Look at this", images: [image] })).toBe(true);
    expect(fetcher).toHaveBeenCalledTimes(1);
    const [url, init] = fetcher.mock.calls[0];
    expect(url).toBe("/api/v1/sessions/hub/c1/messages");
    expect(JSON.parse(init.body)).toMatchObject({ text: "Look at this", images: [image] });
  });
});

describe("ChatView message context availability", () => {
  // Forward (#3899) goes from any chat, Persona's included, even one that can't send here
  it.each([
    [false, true, true, true],
    [true, true, true, true],
    [false, false, true, false],
  ])("uses the chat's actual send capability (assistant=%s, send=%s)", (assistant, send, forward, context) => {
    captured.items = [{ id: "reply#0", seq: 1, kind: "text", role: "assistant", text: "A reply" }];
    if (!send) {
      captured.session.source_kind = "codex_history";
      captured.session.capabilities = { read: true };
    }
    const html = renderToStaticMarkup(<ChatView mid="hub" sid="c1" assistant={assistant} />);
    expect(html.includes('aria-label="转发"')).toBe(forward);
    expect(html.includes('aria-label="交给 Persona"')).toBe(false);
    expect(html.includes('aria-label="作为上下文"')).toBe(context);
  });
});

describe("Agent context usage", () => {
  it("leaves the header and appears in the row below the composer, with no empty row when unknown", () => {
    const was = captured.session;
    try {
      captured.session = { ...was, agent: "claude", context_tokens: 78000, context_window: 200000 };
      const html = renderToStaticMarkup(<ChatView mid="hub" sid="c1" />);
      const header = html.slice(html.indexOf("<header"), html.indexOf("</header>"));
      expect(header).not.toContain("61%");
      expect(html).toContain('data-composer-usage');
      expect(html).toContain('aria-valuenow="61"');
      expect(html.indexOf('data-testid="composer"')).toBeLessThan(html.indexOf("data-composer-usage"));
      captured.session = { ...was, agent: "claude", context_tokens: undefined, context_window: undefined };
      expect(renderToStaticMarkup(<ChatView mid="hub" sid="c1" />)).not.toContain('data-composer-usage');
    } finally {
      captured.session = was;
    }
  });
});

describe("links in the chat", () => {
  it("open what they open as the chat's manager (docs/assistant/managers.md \"Grouping\")", () => {
    const was = captured.session;
    try {
      captured.session = { ...was, manager: "m-2" };
      renderToStaticMarkup(<ChatView mid="hub" sid="c1" />);
      expect(captured.linkManager).toBe("m-2");
      captured.session = { ...was, manager: undefined };
      renderToStaticMarkup(<ChatView mid="hub" sid="c1" assistant />);
      expect(captured.linkManager).toBeUndefined();
    } finally {
      captured.session = was;
    }
  });
});

describe("cached main conversation refresh notice", () => {
  it.each(["assistant", "transcript"])("keeps the transcript and offers Reload when the %s refresh fails", (source) => {
    captured.items = [{ id: "cached", seq: 1, role: "assistant", kind: "text", text: "Cached reply" }];
    if (source === "transcript") captured.error = "Unavailable";
    const html = renderToStaticMarkup(<ChatView mid="hub" sid="c1" assistant mainCache={{ scope: "test-scope" }} refreshError={source === "assistant" ? "Unavailable" : undefined} />);
    expect(html).toContain("Cached reply");
    expect(html).toContain(t("chat-chatview.refresh_failed"));
    expect(html).toContain(t("chat-chatview.not_live_reload"));
    expect(html).toContain('title="Unavailable"');
  });
});

it("keeps a remembered Agent chat on screen when its refresh fails", () => {
  captured.items = [{ id: "kept", seq: 1, role: "assistant", kind: "text", text: "Kept reply" }];
  captured.error = "Unavailable";
  const html = renderToStaticMarkup(<ChatView mid="hub" sid="c1" />);
  expect(html).toContain("Kept reply");
  expect(html).toContain(t("chat-chatview.refresh_failed"));
  expect(html).not.toContain(t("chat-chatview.retry"));
});

describe("composer footer rows (#3533)", () => {
  it("lists the folder in full, labelled; the launcher and model are the model pill's (#5032)", () => {
    const s = { machine_id: "m", session_id: "s", launcher: "claude", model: "anthropic/claude-opus-5-5", cwd: "/Users/me/code/weather" } as Session;
    expect(footerRows(s).map((r) => [r.key, r.label, r.hint])).toEqual([["folder", "~/code/weather", t("chat-sessiondialogs.folder")]]);
  });
  it("an Agent's footer keeps the permission chip and the folder, without the launcher, the model or an effort chip (#5032)", () => {
    const s = { machine_id: "m", session_id: "s", status: "idle", agent: "claude", launcher: "gateway-cc", model: "claude-opus-5-5", effort: "high", permission_mode: "plan", cwd: "/Users/me/code/weather" } as Session;
    const html = renderToStaticMarkup(<ComposerFooter session={s} />);
    expect(html).toContain(`title="${t("chat-sessiondialogs.permission_mode")}"`);
    expect(html).not.toContain(`title="${t("chat-sessiondialogs.effort")}"`);
    expect(html).toContain("~/code/weather");
    expect(html).not.toContain("gateway-cc");
    expect(html).not.toContain("opus");
    // a Codex thread (effort only): its effort is on the pill, the footer has no chip at all
    const codex = renderToStaticMarkup(<ComposerFooter session={{ ...s, agent: "codex", source_kind: "codex_app_server", capabilities: { send: true }, efforts: ["low", "high"] } as Session} />);
    expect(codex).not.toContain("<button type=\"button\" title=");
    // a Persona conversation keeps its effort chip
    expect(renderToStaticMarkup(<ComposerFooter session={{ ...s, assistant: true }} assistant />)).toContain(`title="${t("chat-sessiondialogs.effort")}"`);
  });
  it("leaves out what is missing, and in a Persona conversation the folder too", () => {
    const s = { machine_id: "m", session_id: "s", launcher: "claude", model: "opus", cwd: "/code" } as Session;
    expect(footerRows(s, true)).toEqual([]);
    expect(footerRows(s).every((r) => r.wrap)).toBe(true);
    expect(footerRows({ machine_id: "m", session_id: "s" } as Session)).toEqual([]);
  });
});

describe("ChatView opening line over an empty chat (#2450)", () => {
  const opening = t("chat-chatview.started_in", { path: "/code" });
  const markup = () => renderToStaticMarkup(<ChatView mid="hub" sid="c1" />);

  it("shows where the chat started once an empty first page has loaded", () => {
    expect(markup()).toContain(opening);
  });

  it("stays away while the first page is loading or failed to load", () => {
    captured.loading = true;
    expect(markup()).not.toContain(opening);
    captured.loading = false;
    captured.error = "boom";
    expect(markup()).not.toContain(opening);
  });

  it("stays away when the chat could not start", () => {
    captured.session.start_error = "no such folder";
    try {
      expect(markup()).not.toContain(opening);
    } finally {
      delete captured.session.start_error;
    }
  });

  it("stays away under the notice that the hub has no details for the chat", () => {
    captured.noDetails = t("runtime-session.metadata_unavailable");
    try {
      const html = markup();
      expect(html).toContain("role=\"alert\"");
      expect(html).not.toContain(t("chat-chatview.chat_start"));
      expect(html).not.toContain(opening);
    } finally {
      captured.noDetails = "";
    }
  });
});
